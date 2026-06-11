package hooks

import (
	"context"
	"log"
	"os"
	"regexp"
	"time"

	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
)

// WorkflowForwardingHookName is the marker-file slug that disables
// hooks → daemon workflow-event forwarding for a repo
// (`grove hooks disable workflow-forwarding`).
const WorkflowForwardingHookName = "workflow-forwarding"

// workflowForwardTimeout bounds the daemon publish so forwarding never
// meaningfully delays a hook (the 5s daemon-client default is far too long
// for a blocking hook path).
const workflowForwardTimeout = 1 * time.Second

// workflowRunIDRe extracts the wf_<runId> directory name from an
// agent_transcript_path. Probe-confirmed shapes (CC v2.1.172):
//
//	<slug>/<session-id>/subagents/workflows/wf_<runId>/agent-<id>.jsonl  (session subdir)
//	<slug>/subagents/workflows/wf_<runId>/agent-<id>.jsonl               (directly under slug)
//
// Ad-hoc Agent-tool spawns land under .../subagents/agent-<id>.jsonl (no
// workflows component) and yield no run id.
var workflowRunIDRe = regexp.MustCompile(`/subagents/workflows/(wf_[^/]+)/`)

// extractWorkflowRunID returns the workflow run id embedded in an agent
// transcript path, or "" when the path has no workflow run component
// (ad-hoc Agent-tool spawn, or an unrecognized layout).
func extractWorkflowRunID(transcriptPath string) string {
	if transcriptPath == "" {
		return ""
	}
	m := workflowRunIDRe.FindStringSubmatch(transcriptPath)
	if m == nil {
		return ""
	}
	return m[1]
}

// extractWorkflowName returns the workflow name from the first
// background_tasks[] entry with type == "workflow", or "" when absent.
// Probe-confirmed element keys: id, type, status, description, name.
func extractWorkflowName(backgroundTasks []map[string]any) string {
	for _, task := range backgroundTasks {
		if t, _ := task["type"].(string); t != "workflow" {
			continue
		}
		if name, _ := task["name"].(string); name != "" {
			return name
		}
	}
	return ""
}

// workflowEventFromSubagentStart builds the agent_started wire event for a
// SubagentStart payload. Start payloads are minimal: no per-agent transcript
// path and no run attribution (RunID stays empty; the daemon enriches from
// the journal).
func workflowEventFromSubagentStart(data SubagentStartInput, now time.Time) models.WorkflowEvent {
	return models.WorkflowEvent{
		Kind:            models.WorkflowAgentStarted,
		JobID:           os.Getenv("GROVE_FLOW_JOB_ID"),
		ClaudeSessionID: data.SessionID,
		AgentID:         data.AgentID,
		AgentType:       data.AgentType,
		Timestamp:       now,
		Source:          models.WorkflowSourceHooks,
	}
}

// workflowEventFromSubagentStop builds the agent_completed wire event for a
// SubagentStop payload. RunID is extracted from agent_transcript_path when
// it matches the .../subagents/workflows/wf_*/... shape; an empty RunID
// means an ad-hoc Agent-tool spawn. All enrichment fields are best-effort —
// minimal payload variants carry none of them.
func workflowEventFromSubagentStop(data SubagentStopInput, now time.Time) models.WorkflowEvent {
	// Prefer the real agent_id over the legacy subagent_id, mirroring the
	// events.jsonl record.
	agentID := data.AgentID
	if agentID == "" {
		agentID = data.SubagentID
	}

	ev := models.WorkflowEvent{
		Kind:            models.WorkflowAgentCompleted,
		JobID:           os.Getenv("GROVE_FLOW_JOB_ID"),
		ClaudeSessionID: data.SessionID,
		AgentID:         agentID,
		AgentType:       data.AgentType,
		Timestamp:       now,
		Source:          models.WorkflowSourceHooks,
	}
	if data.AgentTranscriptPath != nil {
		ev.TranscriptPath = *data.AgentTranscriptPath
		ev.RunID = extractWorkflowRunID(*data.AgentTranscriptPath)
	}
	if data.LastAssistantMessage != nil {
		ev.LastMessage = *data.LastAssistantMessage
	}
	return ev
}

// forwardWorkflowEvent publishes a workflow event to the daemon,
// best-effort: it never fails the hook, never writes to stdout (hook
// response contracts must stay pristine — errors go to stderr via log), and
// waits at most workflowForwardTimeout. Forwarding is skipped when the
// repo-scoped marker file disables the "workflow-forwarding" hook or when
// the event has no agent id to key on.
func forwardWorkflowEvent(client daemon.Client, workingDir string, ev models.WorkflowEvent) {
	if client == nil || ev.AgentID == "" {
		return
	}
	if IsHookDisabledByMarker(workingDir, WorkflowForwardingHookName) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), workflowForwardTimeout)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := client.PublishWorkflowEvent(ctx, ev); err != nil {
			log.Printf("workflow forwarding failed (best-effort): %v", err)
		}
	}()

	// Bound the wait: the hook process must not block past the timeout, and
	// returning without waiting at all would let process exit kill the send.
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// forwardingWorkingDir resolves the repo working directory used to scope the
// marker-file disable check: the session's cwd from the hook payload when
// present, else the hook process's own environment.
func forwardingWorkingDir(cwd string) string {
	if cwd != "" {
		return cwd
	}
	return getWorkingDirFromEnv()
}
