package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Command recorder: streams the actual shell commands an agent runs in a job to
// commands.jsonl next to accessed_files.jsonl in the job's .artifacts/ dir.
//
// Both PreToolUse (the attempt) and PostToolUse (the outcome) are captured for
// the Bash tool and linked at write-time: each phase appends its own row and the
// viewer collapses pre/post rows sharing a link_id. A pre row with no matching
// post row is a blocked (or denied) attempt — the viewer derives the "blocked"
// outcome from that.
//
// IMPORTANT: the link key is NOT the Claude payload tool_use_id. Claude Code does
// not send tool_use_id on the PreToolUse hook payload (only PostToolUse carries
// it), so it cannot bridge pre↔post. Instead the recorder generates its own
// link_id at PreToolUse, stashes it in a per-session temp slot (see
// storeCommandLinkID/getCommandLinkID), and reads it back at PostToolUse — the
// same single-slot bridge the storage path uses via storeToolID/getStoredToolID.
// This slot is per-session, so genuinely concurrent Bash calls in one session can
// race (a later pre overwrites the slot before an earlier post reads it); that
// matches the existing storeToolID limitation and is acceptable for now.

const (
	cmdPhasePre  = "pre"
	cmdPhasePost = "post"

	cmdOutcomePending  = "pending"   // pre row: outcome not yet known
	cmdOutcomeRanOK    = "ran_ok"    // post row: tool_error == nil
	cmdOutcomeRanError = "ran_error" // post row: tool_error != nil
	cmdOutcomeBlocked  = "blocked"   // viewer-derived: pre seen, no post
)

// commandEntry is one JSONL row recording a Bash command attempt or outcome.
type commandEntry struct {
	Timestamp string `json:"timestamp"`
	Phase     string `json:"phase"` // pre | post
	// LinkID is a recorder-generated id that bridges the pre and post rows of one
	// command. It is NOT the Claude tool_use_id (absent at PreToolUse).
	LinkID string `json:"link_id"`
	// ToolUseID is the real Claude payload id, informational only and present on
	// post rows (PreToolUse does not provide it).
	ToolUseID   string   `json:"tool_use_id,omitempty"`
	Command     string   `json:"command"`
	Cwd         string   `json:"cwd,omitempty"`
	Subcommands []string `json:"subcommands,omitempty"`
	Outcome     string   `json:"outcome"`
	DurationMs  int64    `json:"duration_ms,omitempty"`
}

// extractBashCommand pulls the command string from a tool input that may be
// typed as map[string]any (PreToolUse) or any (PostToolUse, decoded from JSON).
func extractBashCommand(toolInput any) (string, bool) {
	m, ok := toolInput.(map[string]any)
	if !ok {
		return "", false
	}
	cmd, ok := m["command"].(string)
	if !ok || cmd == "" {
		return "", false
	}
	return cmd, true
}

// commandSubcommands splits a compound shell command into its subcommands using
// the same naive splitter the permission matcher uses (&&, ||, ;, |), dropping
// empty segments.
func commandSubcommands(cmd string) []string {
	var subs []string
	for _, s := range splitShellCommand(cmd) {
		s = strings.TrimSpace(s)
		if s != "" {
			subs = append(subs, s)
		}
	}
	return subs
}

// buildPreCommandEntry constructs the "pre" row for a Bash PreToolUse event. It
// returns ok=false for non-Bash tools or inputs with no command string. Pre
// rows are always recorded as "pending"; the viewer marks them "blocked" when
// no matching post row ever arrives.
func buildPreCommandEntry(toolName string, toolInput any, linkID, cwd string, now time.Time) (commandEntry, bool) {
	if toolName != "Bash" {
		return commandEntry{}, false
	}
	cmd, ok := extractBashCommand(toolInput)
	if !ok {
		return commandEntry{}, false
	}
	return commandEntry{
		Timestamp:   now.Format(time.RFC3339),
		Phase:       cmdPhasePre,
		LinkID:      linkID,
		Command:     cmd,
		Cwd:         cwd,
		Subcommands: commandSubcommands(cmd),
		Outcome:     cmdOutcomePending,
	}, true
}

// buildPostCommandEntry constructs the "post" row for a Bash PostToolUse event.
// It returns ok=false for non-Bash tools or inputs with no command string. The
// outcome is derived from tool_error: ran_error when present, ran_ok otherwise.
func buildPostCommandEntry(data PostToolUseInput, linkID string, now time.Time) (commandEntry, bool) {
	if data.ToolName != "Bash" {
		return commandEntry{}, false
	}
	cmd, ok := extractBashCommand(data.ToolInput)
	if !ok {
		return commandEntry{}, false
	}
	outcome := cmdOutcomeRanOK
	if data.ToolError != nil {
		outcome = cmdOutcomeRanError
	}
	return commandEntry{
		Timestamp:   now.Format(time.RFC3339),
		Phase:       cmdPhasePost,
		LinkID:      linkID,
		ToolUseID:   data.ToolUseID,
		Command:     cmd,
		Cwd:         data.Cwd,
		Subcommands: commandSubcommands(cmd),
		Outcome:     outcome,
		DurationMs:  data.ToolDurationMs,
	}, true
}

// Command link-id bridge: PreToolUse generates a link id and stashes it in a
// per-session temp file; PostToolUse reads it back so the two rows share a
// link_id. This mirrors storeToolID/getStoredToolID but uses its own slot so it
// never collides with the storage path's tool id.
func commandLinkPath(sessionID string) string {
	return filepath.Join(os.TempDir(), "claude-cmd-link-"+sessionID+".json")
}

func storeCommandLinkID(sessionID, linkID string) {
	data, _ := json.Marshal(map[string]string{"link_id": linkID})
	_ = os.WriteFile(commandLinkPath(sessionID), data, 0o644)
}

func getCommandLinkID(sessionID string) string {
	data, err := os.ReadFile(commandLinkPath(sessionID))
	if err != nil {
		return ""
	}
	var stored map[string]string
	_ = json.Unmarshal(data, &stored)
	return stored["link_id"]
}

func clearCommandLinkID(sessionID string) {
	_ = os.Remove(commandLinkPath(sessionID))
}

// newCommandLinkID generates a session-scoped, monotonically-unique link id.
func newCommandLinkID(sessionID string) string {
	return fmt.Sprintf("%s_%d", sessionID, time.Now().UnixNano())
}

// appendCommandEntries streams command rows to an append-only JSONL file at
// .artifacts/<job-name>/commands.jsonl within the active plan directory,
// reusing resolveFileAccessTarget (the same job→artifacts binding as
// accessed_files.jsonl). Each row is marshaled then written in a single Write
// of line+newline so the append stays atomic per line under concurrent hook
// processes (O_APPEND + one write syscall).
func appendCommandEntries(sessionID string, entries []commandEntry) {
	if len(entries) == 0 {
		return
	}

	planDir, jobName := resolveFileAccessTarget(sessionID)
	if planDir == "" {
		return
	}

	artifactsDir := filepath.Join(planDir, ".artifacts", jobName)
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return
	}

	jsonlPath := filepath.Join(artifactsDir, "commands.jsonl")
	f, err := os.OpenFile(jsonlPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		_, _ = f.Write(append(line, '\n'))
	}
}
