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

// shouldForwardSubagentStop reports whether a SubagentStop payload is a
// genuine subagent completion worth forwarding to the daemon. It mirrors the
// Start-side guard (shouldForwardSubagentStart): without it, phantom stops
// leak into the workflow tree as ad-hoc agent rows (a completion with no
// matching start).
//
// While a backgrounded Workflow() runs, the MAIN session fires a SubagentStop
// at each turn boundary. Those carry the session's background_tasks[] (an entry
// with type == "workflow") and an empty agent_type, and an agent_transcript_path
// pointing at a per-agent transcript Claude Code never actually wrote. They are
// not subagent completions, so they are dropped.
//
// Genuine completions are kept: a real workflow subagent's
// agent_transcript_path embeds the wf_<runId> dir (so it attributes to its run,
// not the ad-hoc bucket), and a real Agent/Task spawn carries a non-empty
// agent_type (the subagent type name).
func shouldForwardSubagentStop(data SubagentStopInput) bool {
	if data.AgentTranscriptPath != nil && extractWorkflowRunID(*data.AgentTranscriptPath) != "" {
		return true
	}
	if data.AgentType == "" && extractWorkflowName(data.BackgroundTasks) != "" {
		return false
	}
	return true
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

// terminalChildStatuses are the background-task/cron status values that mark a
// child as done. Any other status — or an absent/non-string status — counts as
// live. This is the contract's set (job 45 addendum (a)); values like
// "cancelled"/"killed" are unverified, but the count is live-biased so a miss
// only briefly over-counts. The harness's background_tasks[] list is itself
// already live-biased (one entry per still-live child), so this filter is
// belt-and-braces against entries that do carry a terminal status.
var terminalChildStatuses = map[string]bool{
	"completed": true,
	"failed":    true,
	"errored":   true,
}

// childEntryLive reports whether a background_tasks/session_crons entry
// represents a still-running child: live iff its "status" key is absent,
// non-string, or not in terminalChildStatuses.
func childEntryLive(entry map[string]any) bool {
	status, ok := entry["status"].(string)
	if !ok {
		return true // absent or non-string → live
	}
	return !terminalChildStatuses[status]
}

// countLiveChildren counts the live background children a session still owns at
// a turn boundary: live BackgroundTasks entries plus live SessionCrons entries
// whose "id" is not already represented in BackgroundTasks (dedupe by id). The
// rule is deliberately type-agnostic — it does not matter whether bash /
// subagent / workflow / cron children carry type:"workflow", type:"cron", or
// anything else. R5 note: real payloads observed so far only carry
// type:"workflow" and type:"cron" entries (see wfBackgroundTasks in
// workflow_forward_test.go); whether spawned subagents/bash appear as their own
// entries is unverified, but the count stays contract-compliant either way
// (transient within-turn subagents would simply not be present).
func countLiveChildren(tasks, crons []map[string]any) int {
	count := 0
	ids := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		if !childEntryLive(t) {
			continue
		}
		count++
		if id, ok := t["id"].(string); ok && id != "" {
			ids[id] = true
		}
	}
	for _, c := range crons {
		if !childEntryLive(c) {
			continue
		}
		if id, ok := c["id"].(string); ok && id != "" && ids[id] {
			continue // already counted as a background task
		}
		count++
	}
	return count
}

// workflowChildrenSnapshotEvent builds the children_snapshot wire event for a
// SubagentStop payload: a point-in-time live-background-child count for the
// session, carried on a dedicated kind that mints no agent/run rows on the
// daemon. AgentID stays empty by design (this is a session-level snapshot, not
// an agent lifecycle delta); JobID rides GROVE_FLOW_JOB_ID like the other
// builders so the daemon can key the owning session, falling back to
// ClaudeSessionID. LiveBashChildren carries the still-live background bash jobs
// (type=="shell" entries) so the daemon can reconcile bash liveness (start new,
// clear absent) against this authoritative turn-boundary view (F6).
func workflowChildrenSnapshotEvent(data SubagentStopInput, now time.Time) models.WorkflowEvent {
	return models.WorkflowEvent{
		Kind:             models.WorkflowChildrenSnapshot,
		JobID:            os.Getenv("GROVE_FLOW_JOB_ID"),
		ClaudeSessionID:  data.SessionID,
		LiveChildren:     countLiveChildren(data.BackgroundTasks, data.SessionCrons),
		LiveBashChildren: liveBashChildren(data.BackgroundTasks),
		Timestamp:        now,
		Source:           models.WorkflowSourceHooks,
	}
}

// liveBashChildren extracts the live background *bash* jobs from a SubagentStop
// background_tasks[] list: the entries whose type is "shell" (probe-confirmed
// value for a `run_in_background: true` Bash) and whose status is not terminal.
// Each yields a BashChildRef{ID, Command}. Returns nil when none — the daemon
// treats a nil/empty list on a hook-sourced snapshot as "no live bash", which
// clears any it was tracking for the session.
func liveBashChildren(tasks []map[string]any) []models.BashChildRef {
	var out []models.BashChildRef
	for _, t := range tasks {
		if typ, _ := t["type"].(string); typ != "shell" {
			continue
		}
		if !childEntryLive(t) {
			continue
		}
		id, _ := t["id"].(string)
		if id == "" {
			continue
		}
		cmd, _ := t["command"].(string)
		out = append(out, models.BashChildRef{ID: id, Command: cmd})
	}
	return out
}

// workflowEventFromBashStart builds the bash_started wire event for a
// backgrounded Bash tool-use. The background task id (from PostToolUse
// tool_response.backgroundTaskId) is the AgentID; the command is the Name (the
// render title). JobID rides GROVE_FLOW_JOB_ID, falling back to the claude
// session id, like the other builders.
func workflowEventFromBashStart(sessionID, backgroundTaskID, command string, now time.Time) models.WorkflowEvent {
	return models.WorkflowEvent{
		Kind:            models.WorkflowBashStarted,
		JobID:           os.Getenv("GROVE_FLOW_JOB_ID"),
		ClaudeSessionID: sessionID,
		AgentID:         backgroundTaskID,
		Name:            command,
		Timestamp:       now,
		Source:          models.WorkflowSourceHooks,
	}
}

// extractBackgroundTaskID returns the backgroundTaskId a PostToolUse Bash
// tool_response carries when the command was launched with run_in_background
// (probe-confirmed field on the Bash result object). Returns "" for a
// foreground command or any other shape. tool_response decodes from JSON as
// map[string]any.
func extractBackgroundTaskID(resp any) string {
	m, ok := resp.(map[string]any)
	if !ok {
		return ""
	}
	id, _ := m["backgroundTaskId"].(string)
	return id
}

// workflowEventForwardable reports whether a workflow event carries enough
// keying to be worth forwarding. For WorkflowChildrenSnapshot the count keys
// on the owning session, so a job id OR claude session id suffices (AgentID is
// empty by design). Every other kind requires a non-empty AgentID — byte-
// equivalent to the historical `ev.AgentID == ""` early-return.
func workflowEventForwardable(ev models.WorkflowEvent) bool {
	if ev.Kind == models.WorkflowChildrenSnapshot {
		return ev.JobID != "" || ev.ClaudeSessionID != ""
	}
	return ev.AgentID != ""
}

// forwardWorkflowEvent publishes a workflow event to the daemon,
// best-effort: it never fails the hook, never writes to stdout (hook
// response contracts must stay pristine — errors go to stderr via log), and
// waits at most workflowForwardTimeout. Forwarding is skipped when the
// repo-scoped marker file disables the "workflow-forwarding" hook or when
// the event lacks the keying workflowEventForwardable requires.
func forwardWorkflowEvent(client daemon.Client, workingDir string, ev models.WorkflowEvent) {
	if client == nil || !workflowEventForwardable(ev) {
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
