package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectArtifact(t *testing.T) {
	embedded := []byte(`// plugin
export const GROVE_PLUGIN_VERSION = "2.0.0";
const body = 1;
`)

	write := func(t *testing.T, content []byte) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "grove-integration.ts")
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("not installed", func(t *testing.T) {
		status := inspectArtifact(filepath.Join(t.TempDir(), "missing.ts"), embedded)
		if status.Verdict != "not-installed" || status.Installed {
			t.Errorf("got verdict %q installed=%t, want not-installed", status.Verdict, status.Installed)
		}
		if status.EmbeddedVersion != "2.0.0" {
			t.Errorf("embedded version = %q, want 2.0.0", status.EmbeddedVersion)
		}
	})

	t.Run("current", func(t *testing.T) {
		status := inspectArtifact(write(t, embedded), embedded)
		if status.Verdict != "current" {
			t.Errorf("verdict = %q, want current", status.Verdict)
		}
		if status.InstalledVersion != "2.0.0" {
			t.Errorf("installed version = %q, want 2.0.0", status.InstalledVersion)
		}
	})

	t.Run("stale older version", func(t *testing.T) {
		old := []byte(`export const GROVE_PLUGIN_VERSION = "1.0.0";`)
		status := inspectArtifact(write(t, old), embedded)
		if status.Verdict != "stale" {
			t.Errorf("verdict = %q, want stale", status.Verdict)
		}
		if status.InstalledVersion != "1.0.0" {
			t.Errorf("installed version = %q, want 1.0.0", status.InstalledVersion)
		}
	})

	t.Run("stale unstamped install", func(t *testing.T) {
		old := []byte(`// pre-versioning plugin without a stamp`)
		status := inspectArtifact(write(t, old), embedded)
		if status.Verdict != "stale" {
			t.Errorf("verdict = %q, want stale", status.Verdict)
		}
		if status.InstalledVersion != "" {
			t.Errorf("installed version = %q, want empty", status.InstalledVersion)
		}
	})

	t.Run("modified same version", func(t *testing.T) {
		edited := append([]byte{}, embedded...)
		edited = append(edited, []byte("\n// local edit\n")...)
		status := inspectArtifact(write(t, edited), embedded)
		if status.Verdict != "modified" {
			t.Errorf("verdict = %q, want modified", status.Verdict)
		}
	})
}
