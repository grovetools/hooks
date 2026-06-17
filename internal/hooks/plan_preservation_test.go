package hooks

import (
	"os"
	"testing"
)

// TestResolveWorkingDir_SessionCwdWinsOverPWD verifies the authoritative
// session cwd from the hook payload outranks $PWD, preventing the
// daemon-vs-interactive cwd divergence where $PWD reflects the shell that
// launched the daemon rather than the actual session working directory.
func TestResolveWorkingDir_SessionCwdWinsOverPWD(t *testing.T) {
	t.Setenv("PWD", "/bogus/daemon/launch/dir")

	sessionCwd := "/real/session/working/dir"
	if got := resolveWorkingDir(sessionCwd); got != sessionCwd {
		t.Fatalf("resolveWorkingDir(%q) = %q, want session cwd to win over $PWD", sessionCwd, got)
	}
}

// TestResolveWorkingDir_FallsBackToGetwd verifies that when no session cwd is
// provided, os.Getwd() is preferred over $PWD.
func TestResolveWorkingDir_FallsBackToGetwd(t *testing.T) {
	t.Setenv("PWD", "/bogus/daemon/launch/dir")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() failed: %v", err)
	}

	if got := resolveWorkingDir(""); got != wd {
		t.Fatalf("resolveWorkingDir(\"\") = %q, want os.Getwd() %q (not $PWD)", got, wd)
	}
}
