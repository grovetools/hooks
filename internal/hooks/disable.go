package hooks

import (
	"os"
	"path/filepath"

	"github.com/grovetools/core/pkg/paths"
)

// repoSlug derives a marker-dir-safe slug from the repo working directory's
// basename. Uses the same slugification rules as hook names so the layout is
// consistent.
func repoSlug(workingDir string) string {
	abs, err := filepath.Abs(workingDir)
	if err != nil {
		abs = workingDir
	}
	return slugifyHookName(filepath.Base(abs))
}

// HookMarkerDir returns the directory holding disable-marker files for the
// given repo working directory.
func HookMarkerDir(workingDir string) string {
	return filepath.Join(paths.StateDir(), "hooks", "disabled", repoSlug(workingDir))
}

// HookMarkerPath returns the marker-file path for a single hook.
func HookMarkerPath(workingDir, hookName string) string {
	return filepath.Join(HookMarkerDir(workingDir), slugifyHookName(hookName))
}

// IsHookDisabledByMarker reports whether the marker file for the named hook
// exists. Any stat error other than not-exist is treated as "not disabled" —
// we never want a transient FS error to silently skip a hook.
func IsHookDisabledByMarker(workingDir, hookName string) bool {
	if workingDir == "" || hookName == "" {
		return false
	}
	_, err := os.Stat(HookMarkerPath(workingDir, hookName))
	return err == nil
}

// DisableHook creates the marker file for the named hook. Reason may be empty.
func DisableHook(workingDir, hookName, reason string) error {
	dir := HookMarkerDir(workingDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := HookMarkerPath(workingDir, hookName)
	return os.WriteFile(path, []byte(reason), 0o644)
}

// EnableHook removes the marker file for the named hook. Idempotent.
func EnableHook(workingDir, hookName string) error {
	path := HookMarkerPath(workingDir, hookName)
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// HookDisableReason returns the contents of the marker file (the reason),
// trimmed of trailing newlines. Returns empty string if not disabled or if
// the file is empty.
func HookDisableReason(workingDir, hookName string) string {
	b, err := os.ReadFile(HookMarkerPath(workingDir, hookName))
	if err != nil {
		return ""
	}
	out := string(b)
	for len(out) > 0 && (out[len(out)-1] == '\n' || out[len(out)-1] == '\r') {
		out = out[:len(out)-1]
	}
	return out
}
