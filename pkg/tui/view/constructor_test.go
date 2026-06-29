package view

import "testing"

// TestIsTerminalJobStatus locks in which job-file statuses are treated as
// authoritative over a stale daemon session status during the session/job
// merge in defaultGetAllSessions. Parked-but-live states (pending_user, idle,
// hold, todo) must NOT be terminal, otherwise the merge would clobber a live
// daemon session that the daemon still owns.
func TestIsTerminalJobStatus(t *testing.T) {
	terminal := []string{"completed", "failed", "abandoned"}
	for _, s := range terminal {
		if !isTerminalJobStatus(s) {
			t.Errorf("expected %q to be terminal", s)
		}
	}

	nonTerminal := []string{"running", "idle", "pending_user", "hold", "todo", "pending", "queued", "", "error"}
	for _, s := range nonTerminal {
		if isTerminalJobStatus(s) {
			t.Errorf("expected %q to be non-terminal", s)
		}
	}
}

// TestDefaultFilterPreferencesActiveOnly pins the active-only status-filter
// default so a future edit can't silently re-enable the noisy terminal/parked
// states (completed/failed/error/hold/todo/abandoned) that the browser hides
// by default.
func TestDefaultFilterPreferencesActiveOnly(t *testing.T) {
	prefs := DefaultFilterPreferences()

	wantOn := []string{"running", "idle", "pending_user", "interrupted"}
	for _, s := range wantOn {
		if !prefs.StatusFilters[s] {
			t.Errorf("expected status filter %q to default ON", s)
		}
	}

	wantOff := []string{"completed", "failed", "error", "hold", "todo", "abandoned"}
	for _, s := range wantOff {
		if prefs.StatusFilters[s] {
			t.Errorf("expected status filter %q to default OFF", s)
		}
	}

	// All known job types should remain visible by default.
	for typ, on := range prefs.TypeFilters {
		if !on {
			t.Errorf("expected type filter %q to default ON", typ)
		}
	}
}
