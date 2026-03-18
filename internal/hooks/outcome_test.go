package hooks

import "testing"

func TestDetermineOutcome(t *testing.T) {
	tests := []struct {
		name       string
		ctx        StopContext
		wantStatus string
		wantDone   bool
	}{
		// Oneshot jobs
		{
			name:       "oneshot_job always completes",
			ctx:        StopContext{SessionType: "oneshot_job", Provider: "claude", ExitReason: "completed"},
			wantStatus: "completed",
			wantDone:   true,
		},
		{
			name:       "oneshot_job completes even without exit reason",
			ctx:        StopContext{SessionType: "oneshot_job", Provider: "claude", ExitReason: ""},
			wantStatus: "completed",
			wantDone:   true,
		},

		// OpenCode sessions
		{
			name:       "opencode error marks failed",
			ctx:        StopContext{SessionType: "interactive_agent", Provider: "opencode", ExitReason: "error"},
			wantStatus: "failed",
			wantDone:   true,
		},
		{
			name:       "opencode killed marks failed",
			ctx:        StopContext{SessionType: "interactive_agent", Provider: "opencode", ExitReason: "killed"},
			wantStatus: "failed",
			wantDone:   true,
		},
		{
			name:       "opencode interrupted marks failed",
			ctx:        StopContext{SessionType: "interactive_agent", Provider: "opencode", ExitReason: "interrupted"},
			wantStatus: "failed",
			wantDone:   true,
		},
		{
			name:       "opencode completed means idle (end-of-turn)",
			ctx:        StopContext{SessionType: "interactive_agent", Provider: "opencode", ExitReason: "completed"},
			wantStatus: "idle",
			wantDone:   false,
		},
		{
			name:       "opencode empty exit reason means idle",
			ctx:        StopContext{SessionType: "interactive_agent", Provider: "opencode", ExitReason: ""},
			wantStatus: "idle",
			wantDone:   false,
		},

		// Regular claude/codex sessions
		{
			name:       "claude completed marks complete",
			ctx:        StopContext{SessionType: "claude_session", Provider: "claude", ExitReason: "completed"},
			wantStatus: "completed",
			wantDone:   true,
		},
		{
			name:       "claude error marks complete",
			ctx:        StopContext{SessionType: "claude_session", Provider: "claude", ExitReason: "error"},
			wantStatus: "completed",
			wantDone:   true,
		},
		{
			name:       "claude interrupted marks complete",
			ctx:        StopContext{SessionType: "claude_session", Provider: "claude", ExitReason: "interrupted"},
			wantStatus: "completed",
			wantDone:   true,
		},
		{
			name:       "claude killed marks complete",
			ctx:        StopContext{SessionType: "claude_session", Provider: "claude", ExitReason: "killed"},
			wantStatus: "completed",
			wantDone:   true,
		},
		{
			name:       "claude empty exit reason means idle",
			ctx:        StopContext{SessionType: "claude_session", Provider: "claude", ExitReason: ""},
			wantStatus: "idle",
			wantDone:   false,
		},
		{
			name:       "codex completed marks complete",
			ctx:        StopContext{SessionType: "claude_session", Provider: "codex", ExitReason: "completed"},
			wantStatus: "completed",
			wantDone:   true,
		},

		// Interactive agent (non-opencode)
		{
			name:       "interactive_agent claude completed marks complete",
			ctx:        StopContext{SessionType: "interactive_agent", Provider: "claude", ExitReason: "completed"},
			wantStatus: "completed",
			wantDone:   true,
		},
		{
			name:       "interactive_agent claude idle on empty exit",
			ctx:        StopContext{SessionType: "interactive_agent", Provider: "claude", ExitReason: ""},
			wantStatus: "idle",
			wantDone:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetermineOutcome(tt.ctx)
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tt.wantStatus)
			}
			if got.IsComplete != tt.wantDone {
				t.Errorf("IsComplete = %v, want %v", got.IsComplete, tt.wantDone)
			}
		})
	}
}
