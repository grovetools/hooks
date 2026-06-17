package hooks

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/pkg/sessions"
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

// spawnAgentIDRe matches a genuine subagent spawn id: the literal 'a'
// followed by exactly 16 hex digits (17 chars total), e.g.
// "a62124203bfeb94f0". Claude Code mints this id for every real Task/Agent
// spawn and writes its transcript at <session>/subagents/agent-<id>.jsonl.
//
// At session init the harness ALSO fires SubagentStart once per registered
// agent *definition* (e.g. Explore, Plan, which have .claude/agents/*.md
// definition files; built-ins like general-purpose do not) — a type
// registration, not a spawn. Those carry a SHORT id (the literal 'a' + ~6
// hex, e.g. "a03e225") and never write a per-agent transcript. The id form
// is the reliable discriminator: an empty session_id is NOT one (real spawns
// also log it empty at this layer), and the transcript file does not yet
// exist when SubagentStart fires for a real spawn either.
var spawnAgentIDRe = regexp.MustCompile(`^a[0-9a-f]{16}$`)

// isSpawnAgentID reports whether agentID is a genuine spawn id (see
// spawnAgentIDRe) rather than a phantom type-registration id.
func isSpawnAgentID(agentID string) bool {
	return spawnAgentIDRe.MatchString(agentID)
}

// shouldForwardSubagentStart reports whether a SubagentStart payload is a
// genuine Task/Agent spawn worth forwarding to the daemon. Phantom
// type-registration events (short, non-spawn agent_id) are dropped so they
// never pollute the subagent/workflow tree with idle, transcript-less agents.
func shouldForwardSubagentStart(data SubagentStartInput) bool {
	return isSpawnAgentID(data.AgentID)
}

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

// agentMeta is the JSON shape of agent-<id>.meta.json files persisted by
// Claude Code alongside agent transcripts. Simple/ad-hoc subagents (Agent
// tool) carry a rich description; workflow subagents carry only agentType.
type agentMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
	ToolUseID   string `json:"toolUseId"`
}

// readAgentMetaDescription reads the agent-<id>.meta.json file at the given
// path and returns its "description" field. Returns "" on any error (missing
// file, unreadable, malformed JSON) — enrichment is best-effort and must
// never fail the hook.
func readAgentMetaDescription(metaPath string) string {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return ""
	}
	var meta agentMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	return meta.Description
}

// resolveAgentMetaPathFromTranscript derives the meta.json path from an
// agent transcript path. Transcript paths have the shape:
//
//	.../subagents/agent-<id>.jsonl              (simple/ad-hoc)
//	.../subagents/workflows/wf_<runId>/agent-<id>.jsonl (workflow)
//
// The corresponding meta.json is always a sibling: agent-<id>.meta.json.
func resolveAgentMetaPathFromTranscript(transcriptPath string) string {
	if transcriptPath == "" {
		return ""
	}
	dir := filepath.Dir(transcriptPath)
	base := filepath.Base(transcriptPath)
	// agent-<id>.jsonl → agent-<id>.meta.json
	if len(base) > 6 && base[len(base)-6:] == ".jsonl" {
		return filepath.Join(dir, base[:len(base)-6]+".meta.json")
	}
	return ""
}

// findAgentMetaPathForStart locates the agent-<id>.meta.json file for a
// SubagentStart event where no transcript path is available. It uses
// sessions.ResolveClaudeSessionDirs to find all candidate session directories
// and globs each for subagents/agent-<id>.meta.json, returning the first hit.
// Returns "" if no meta file can be found — enrichment is best-effort.
func findAgentMetaPathForStart(sessionID, agentID string) string {
	if sessionID == "" || agentID == "" {
		return ""
	}
	dirs, err := sessions.ResolveClaudeSessionDirs(sessionID)
	if err != nil || len(dirs) == 0 {
		return ""
	}
	metaFile := "agent-" + agentID + ".meta.json"
	for _, dir := range dirs {
		// Check direct subagents/ path (simple/ad-hoc agents)
		candidate := filepath.Join(dir, "subagents", metaFile)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		// Check workflows subdirs (workflow subagents)
		matches, _ := filepath.Glob(filepath.Join(dir, "subagents", "workflows", "*", metaFile))
		if len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

// workflowEventFromSubagentStart builds the agent_started wire event for a
// SubagentStart payload. Start payloads are minimal: no per-agent transcript
// path and no run attribution (RunID stays empty; the daemon enriches from
// the journal). Name is enriched from the sibling agent-<id>.meta.json when
// available (best-effort; missing/unreadable meta files leave Name empty).
func workflowEventFromSubagentStart(data SubagentStartInput, now time.Time) models.WorkflowEvent {
	ev := models.WorkflowEvent{
		Kind:            models.WorkflowAgentStarted,
		JobID:           os.Getenv("GROVE_FLOW_JOB_ID"),
		ClaudeSessionID: data.SessionID,
		AgentID:         data.AgentID,
		AgentType:       data.AgentType,
		Timestamp:       now,
		Source:          models.WorkflowSourceHooks,
	}
	// Enrich Name from meta.json if available
	if metaPath := findAgentMetaPathForStart(data.SessionID, data.AgentID); metaPath != "" {
		ev.Name = readAgentMetaDescription(metaPath)
	}
	return ev
}

// workflowEventFromSubagentStop builds the agent_completed wire event for a
// SubagentStop payload. RunID is extracted from agent_transcript_path when
// it matches the .../subagents/workflows/wf_*/... shape; an empty RunID
// means an ad-hoc Agent-tool spawn. All enrichment fields are best-effort —
// minimal payload variants carry none of them. Name is enriched from the
// sibling agent-<id>.meta.json when AgentTranscriptPath is available.
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
		// Enrich Name from sibling meta.json
		if metaPath := resolveAgentMetaPathFromTranscript(*data.AgentTranscriptPath); metaPath != "" {
			ev.Name = readAgentMetaDescription(metaPath)
		}
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
	return resolveWorkingDir("")
}
