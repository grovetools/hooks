package hooks

import "testing"

// TestIsWaitingNotification pins the Notification-message classifier to the
// strings observed in the live hook event log. The key correctness property:
// real permission / plan-approval / attention prompts (and AskUserQuestion,
// which emits "Claude needs your permission") flip the session to pending_user,
// while the generic post-Stop idle nudge "Claude is waiting for your input"
// does NOT (it would wrongly mark every idle turn as pending_user).
func TestIsWaitingNotification(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		// Blocked-on-user — must match.
		{"Claude needs your permission", true},
		{"Claude Code needs your approval for the plan", true},
		{"Claude Code needs your attention", true},
		{"CLAUDE NEEDS YOUR PERMISSION", true}, // case-insensitive
		// AskUserQuestion surfaces as a permission/attention prompt (probe: 104/113).
		// Covered by the two cases above.

		// Ordinary idle nudge — must NOT match (fires ~60s after every Stop).
		{"Claude is waiting for your input", false},
		// Unrelated notifications — must NOT match.
		{"Test notification", false},
		{"build done & green: committed abc123", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isWaitingNotification(c.msg); got != c.want {
			t.Errorf("isWaitingNotification(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}
