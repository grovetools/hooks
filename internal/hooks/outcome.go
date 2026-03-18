package hooks

// StopContext holds the inputs needed to determine a session's final status.
type StopContext struct {
	SessionType string
	Provider    string
	ExitReason  string
}

// SessionOutcome holds the determined final state for a session.
type SessionOutcome struct {
	Status     string // "completed", "idle", "failed"
	IsComplete bool   // true if the session is fully terminated
}

// DetermineOutcome calculates the final session state based on hook inputs.
// This is a pure function (no side effects) for easy testing.
func DetermineOutcome(ctx StopContext) SessionOutcome {
	if ctx.SessionType == "oneshot_job" {
		// Oneshot jobs always complete when the stop hook fires
		return SessionOutcome{Status: "completed", IsComplete: true}
	}

	if ctx.Provider == "opencode" {
		// OpenCode sessions stay running after each turn. The stop hook fires at the
		// end of each assistant response, but the process is still alive waiting for
		// user input. Only mark as failed on actual errors.
		if ctx.ExitReason == "error" || ctx.ExitReason == "killed" || ctx.ExitReason == "interrupted" {
			return SessionOutcome{Status: "failed", IsComplete: true}
		}
		// For opencode, "completed" exit_reason just means the assistant finished
		// responding. Set to idle, NOT complete. User must explicitly complete via
		// TUI 'c' key or `flow plan complete`.
		return SessionOutcome{Status: "idle", IsComplete: false}
	}

	// Regular claude/codex sessions: use exit reason to determine status
	if ctx.ExitReason == "completed" || ctx.ExitReason == "error" || ctx.ExitReason == "interrupted" || ctx.ExitReason == "killed" {
		return SessionOutcome{Status: "completed", IsComplete: true}
	}

	// Normal end-of-turn stop (empty exit_reason or other) - set to idle
	return SessionOutcome{Status: "idle", IsComplete: false}
}
