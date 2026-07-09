package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Pending-title bridge (F5): the parent's PreToolUse hook for an Agent/Task
// spawn carries the child's `description` (the 3–5 word summary) in tool_input,
// but the child's own SubagentStart payload — which is where the daemon learns
// a live child exists — carries no description, and the agent-<id>.meta.json
// that would hold it is frequently not written yet at SubagentStart time. So a
// running child renders with no title until it completes.
//
// This bridges the gap the same way command_recorder.go bridges pre↔post: the
// parent's PreToolUse stashes each spawn's description in a per-session on-disk
// FIFO, and the child's SubagentStart pops the oldest entry to use as its live
// title. Correlation is by session + spawn order only — Claude Code sends no
// tool_use_id at PreToolUse and no parent linkage on SubagentStart, so a parent
// that spawns several children of different types in one turn may pair a title
// with the wrong sibling. That mispairing is bounded (still a real, plausible
// title from the same turn) and the AgentType fallback guarantees a line
// regardless; the queue is a best-effort enrichment, never a correctness
// dependency. Genuinely concurrent spawns can also race the file (a later push
// may interleave with a pop), matching command_recorder's accepted single-file
// race.

const (
	// pendingTitleCap bounds the FIFO so a parent that pushes titles which never
	// get consumed (denied tool-uses, phantom starts) cannot grow the file
	// without limit; the oldest entries are dropped past the cap.
	pendingTitleCap = 32
	// pendingTitleMaxAge drops stale entries: a title with no consuming
	// SubagentStart within this window is assumed orphaned and skipped, so it
	// never mistitles an unrelated spawn many turns later.
	pendingTitleMaxAge = 10 * time.Minute
)

// pendingTitle is one queued spawn description with the push time (for staleness).
type pendingTitle struct {
	Description string `json:"description"`
	TsNano      int64  `json:"ts_nano"`
}

func pendingTitlesPath(sessionID string) string {
	return filepath.Join(os.TempDir(), "claude-pending-titles-"+sessionID+".json")
}

func readPendingTitles(sessionID string) []pendingTitle {
	data, err := os.ReadFile(pendingTitlesPath(sessionID))
	if err != nil {
		return nil
	}
	var titles []pendingTitle
	if err := json.Unmarshal(data, &titles); err != nil {
		return nil
	}
	return titles
}

func writePendingTitles(sessionID string, titles []pendingTitle) {
	if len(titles) == 0 {
		_ = os.Remove(pendingTitlesPath(sessionID))
		return
	}
	data, err := json.Marshal(titles)
	if err != nil {
		return
	}
	_ = os.WriteFile(pendingTitlesPath(sessionID), data, 0o644) //nolint:gosec // G306: non-secret temp state
}

// pushPendingTitle appends a spawn description to the session's FIFO, pruning
// stale entries and capping the length (oldest-first). Empty session/description
// are ignored.
func pushPendingTitle(sessionID, description string, now time.Time) {
	if sessionID == "" || description == "" {
		return
	}
	titles := prunePendingTitles(readPendingTitles(sessionID), now)
	titles = append(titles, pendingTitle{Description: description, TsNano: now.UnixNano()})
	if len(titles) > pendingTitleCap {
		titles = titles[len(titles)-pendingTitleCap:]
	}
	writePendingTitles(sessionID, titles)
}

// popPendingTitle removes and returns the oldest non-stale queued description
// for the session, or "" when the queue is empty. It rewrites the queue with the
// remainder so each spawn consumes exactly one entry (keeping the queue aligned
// 1:1 with genuine spawns).
func popPendingTitle(sessionID string, now time.Time) string {
	titles := prunePendingTitles(readPendingTitles(sessionID), now)
	if len(titles) == 0 {
		writePendingTitles(sessionID, nil)
		return ""
	}
	head := titles[0]
	writePendingTitles(sessionID, titles[1:])
	return head.Description
}

// isAgentSpawnTool reports whether a tool name spawns a subagent whose
// PreToolUse input carries a `description` worth queuing as a live title. Both
// the FleetView "Agent" tool and the classic "Task" tool qualify (probed: the
// Agent tool_input is {description, prompt, run_in_background}).
func isAgentSpawnTool(toolName string) bool {
	return toolName == "Agent" || toolName == "Task"
}

// stringField returns a non-empty string value for key in a tool_input map, or
// "" when absent, non-string, or the map is nil.
func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// prunePendingTitles drops entries older than pendingTitleMaxAge.
func prunePendingTitles(titles []pendingTitle, now time.Time) []pendingTitle {
	if len(titles) == 0 {
		return nil
	}
	cutoff := now.Add(-pendingTitleMaxAge).UnixNano()
	out := titles[:0]
	for _, t := range titles {
		if t.TsNano >= cutoff {
			out = append(out, t)
		}
	}
	return out
}
