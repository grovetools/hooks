package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeProviderSessionStatus(t *testing.T) {
	tests := []struct {
		raw    string
		want   string
		wantOK bool
	}{
		{"busy", "running", true},
		{"retry", "running", true},
		{"running", "running", true},
		{"idle", "idle", true},
		{"pending_user", "pending_user", true},
		{"completed", "", false}, // terminal transitions go through stop/session-end
		{"failed", "", false},
		{"", "", false},
		{"DROP TABLE sessions", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, ok := NormalizeProviderSessionStatus(tt.raw)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("NormalizeProviderSessionStatus(%q) = (%q, %t), want (%q, %t)", tt.raw, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestResolveRegisteredSessionID(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("GROVE_HOME", "")
	t.Setenv("XDG_STATE_HOME", stateHome)

	sessionsDir := filepath.Join(stateHome, "grove", "hooks", "sessions")

	// Flow-managed session: directory named by the native opencode id,
	// metadata session_id carries the flow job id.
	nativeID := "ses_abc123"
	flowDir := filepath.Join(sessionsDir, nativeID)
	if err := os.MkdirAll(flowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := `{"session_id": "flow-job-42", "claude_session_id": "ses_abc123", "provider": "opencode"}`
	if err := os.WriteFile(filepath.Join(flowDir, "metadata.json"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := resolveRegisteredSessionID(nativeID); got != "flow-job-42" {
		t.Errorf("resolveRegisteredSessionID(%q) = %q, want %q", nativeID, got, "flow-job-42")
	}

	// No metadata: fall back to the given id.
	if got := resolveRegisteredSessionID("ses_unknown"); got != "ses_unknown" {
		t.Errorf("resolveRegisteredSessionID fallback = %q, want %q", got, "ses_unknown")
	}

	// Empty id passes through.
	if got := resolveRegisteredSessionID(""); got != "" {
		t.Errorf("resolveRegisteredSessionID(\"\") = %q, want \"\"", got)
	}

	// Metadata without session_id: fall back to the given id.
	bareID := "ses_bare"
	bareDir := filepath.Join(sessionsDir, bareID)
	if err := os.MkdirAll(bareDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bareDir, "metadata.json"), []byte(`{"provider": "opencode"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveRegisteredSessionID(bareID); got != bareID {
		t.Errorf("resolveRegisteredSessionID(%q) = %q, want %q", bareID, got, bareID)
	}
}
