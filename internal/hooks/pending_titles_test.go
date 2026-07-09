package hooks

import (
	"os"
	"testing"
	"time"
)

func TestPendingTitlesFIFO(t *testing.T) {
	sess := "test-pending-fifo"
	defer os.Remove(pendingTitlesPath(sess))
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	pushPendingTitle(sess, "first task", now)
	pushPendingTitle(sess, "second task", now.Add(time.Second))

	if got := popPendingTitle(sess, now.Add(2*time.Second)); got != "first task" {
		t.Fatalf("pop 1 = %q, want %q", got, "first task")
	}
	if got := popPendingTitle(sess, now.Add(2*time.Second)); got != "second task" {
		t.Fatalf("pop 2 = %q, want %q", got, "second task")
	}
	if got := popPendingTitle(sess, now.Add(2*time.Second)); got != "" {
		t.Fatalf("pop empty = %q, want empty", got)
	}
	// The backing file must be gone once drained.
	if _, err := os.Stat(pendingTitlesPath(sess)); !os.IsNotExist(err) {
		t.Fatalf("expected drained queue file to be removed, err=%v", err)
	}
}

func TestPendingTitlesStaleSkipped(t *testing.T) {
	sess := "test-pending-stale"
	defer os.Remove(pendingTitlesPath(sess))
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	pushPendingTitle(sess, "old task", now)
	// Pop far past the max age: the stale entry must be skipped (returns empty),
	// never mistitling an unrelated later spawn.
	if got := popPendingTitle(sess, now.Add(pendingTitleMaxAge+time.Minute)); got != "" {
		t.Fatalf("stale pop = %q, want empty", got)
	}
}

func TestPendingTitlesCap(t *testing.T) {
	sess := "test-pending-cap"
	defer os.Remove(pendingTitlesPath(sess))
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	// Push more than the cap; the oldest must be dropped so the newest survive.
	total := pendingTitleCap + 5
	for i := 0; i < total; i++ {
		pushPendingTitle(sess, string(rune('A'+i%26))+"-task", now.Add(time.Duration(i)*time.Millisecond))
	}
	titles := readPendingTitles(sess)
	if len(titles) != pendingTitleCap {
		t.Fatalf("queue length = %d, want capped at %d", len(titles), pendingTitleCap)
	}
	// The first surviving entry must be the (total-cap)th pushed, not the 0th.
	wantFirst := string(rune('A'+(total-pendingTitleCap)%26)) + "-task"
	if titles[0].Description != wantFirst {
		t.Fatalf("oldest surviving = %q, want %q (older dropped)", titles[0].Description, wantFirst)
	}
}

func TestPendingTitlesEmptyIgnored(t *testing.T) {
	sess := "test-pending-empty"
	defer os.Remove(pendingTitlesPath(sess))
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	pushPendingTitle(sess, "", now) // ignored
	pushPendingTitle("", "x", now)  // ignored
	if got := popPendingTitle(sess, now); got != "" {
		t.Fatalf("pop after empty pushes = %q, want empty", got)
	}
}

func TestIsAgentSpawnTool(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"Agent", true},
		{"Task", true},
		{"Bash", false},
		{"Read", false},
	} {
		if got := isAgentSpawnTool(tc.name); got != tc.want {
			t.Errorf("isAgentSpawnTool(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
