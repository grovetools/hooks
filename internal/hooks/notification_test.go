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

// TestIsIdleNudgeNotification pins the F1 un-stick signal: the generic
// post-turn idle nudge ("Claude is waiting for your input") is the one
// notification RunNotificationHook uses to clear a session stranded at
// pending_user by a Stop-less deny. It must match ONLY that nudge — never a
// blocked-on-user prompt (those legitimately set pending_user and must stay
// loud) and never an unrelated notification.
func TestIsIdleNudgeNotification(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		// The idle nudge — must match (case-insensitive).
		{"Claude is waiting for your input", true},
		{"CLAUDE IS WAITING FOR YOUR INPUT", true},
		// Blocked-on-user prompts — must NOT match (they set pending_user).
		{"Claude needs your permission", false},
		{"Claude Code needs your approval for the plan", false},
		{"Claude Code needs your attention", false},
		// Unrelated — must NOT match.
		{"Test notification", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isIdleNudgeNotification(c.msg); got != c.want {
			t.Errorf("isIdleNudgeNotification(%q) = %v, want %v", c.msg, got, c.want)
		}
		// The two classifiers must be mutually exclusive: a message that sets
		// pending_user must never also be treated as the clear signal.
		if isWaitingNotification(c.msg) && isIdleNudgeNotification(c.msg) {
			t.Errorf("message %q matched BOTH classifiers — they must be disjoint", c.msg)
		}
	}
}
