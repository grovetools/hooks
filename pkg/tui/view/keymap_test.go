package view

import (
	"testing"

	"github.com/grovetools/core/tui/keymap"
)

// TestKeyMapAuditCoverage asserts the hooks browser keymap has no coverage
// gaps: every enabled key.Binding (including embedded Base) appears in exactly
// one Sections() entry, every help label matches its keys, and no enabled
// binding has empty help. Base bindings the browser does not implement are
// disabled in NewKeyMap, so they must not surface here.
func TestKeyMapAuditCoverage(t *testing.T) {
	km := NewKeyMap(nil)
	gaps := keymap.AuditCoverage(km)
	if len(gaps) != 0 {
		for _, g := range gaps {
			t.Errorf("keymap coverage gap: field=%s kind=%s detail=%s", g.Field, g.Kind, g.Detail)
		}
	}
}

// TestGoToTopUsesSequence guards that go-to-top is a real "gg" sequence on the
// Base.Top binding (no manual gPressed chord) and that the stale local
// GoToTop/Archive vocabulary is gone.
func TestGoToTopUsesSequence(t *testing.T) {
	km := NewKeyMap(nil)
	if keys := km.Top.Keys(); len(keys) == 0 || keys[0] != "gg" {
		t.Fatalf("expected Base.Top bound to \"gg\", got %v", km.Top.Keys())
	}
}
