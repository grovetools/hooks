package commands

import (
	"bytes"
	"fmt"
	"os"

	grovelogging "github.com/grovetools/core/logging"

	"github.com/grovetools/hooks/internal/pluginversion"
)

// artifactStatus describes an installed provider-integration artifact (the
// opencode plugin / pi extension TS file) relative to the copy embedded in
// this binary. Verdicts:
//
//	not-installed  no file at the install path
//	current        installed bytes match the embedded artifact exactly
//	stale          installed version differs from the embedded version
//	               (includes unstamped pre-versioning installs)
//	modified       same version stamp but different content (hand-edited)
type artifactStatus struct {
	Path             string
	Installed        bool
	InstalledVersion string // "" when unstamped
	EmbeddedVersion  string
	Verdict          string
}

func inspectArtifact(path string, embedded []byte) artifactStatus {
	status := artifactStatus{
		Path:            path,
		EmbeddedVersion: pluginversion.Extract(embedded),
	}

	installed, err := os.ReadFile(path)
	if err != nil {
		status.Verdict = "not-installed"
		return status
	}
	status.Installed = true
	status.InstalledVersion = pluginversion.Extract(installed)

	switch {
	case bytes.Equal(installed, embedded):
		status.Verdict = "current"
	case status.InstalledVersion != status.EmbeddedVersion:
		status.Verdict = "stale"
	default:
		status.Verdict = "modified"
	}
	return status
}

// describeVersion renders a version stamp for display, marking unstamped
// (pre-versioning) artifacts explicitly.
func describeVersion(v string) string {
	if v == "" {
		return "unversioned (pre-versioning)"
	}
	return v
}

// emitArtifactStatus reports an artifact's drift status through the unified
// logger; shared by `hooks opencode status` and `hooks pi status`.
// installHint names the command that (re)installs the artifact.
func emitArtifactStatus(ulog *grovelogging.UnifiedLogger, label, installHint string, status artifactStatus) {
	base := ulog.Info("Integration status").
		Field("path", status.Path).
		Field("installed", fmt.Sprintf("%t", status.Installed)).
		Field("installed_version", status.InstalledVersion).
		Field("embedded_version", status.EmbeddedVersion).
		Field("verdict", status.Verdict)

	switch status.Verdict {
	case "not-installed":
		base.Pretty(fmt.Sprintf("! %s is not installed (embedded version %s)\n  Install with: %s", label, describeVersion(status.EmbeddedVersion), installHint)).Emit()
	case "current":
		base.Pretty(fmt.Sprintf("* %s is up to date (version %s)\n  %s", label, describeVersion(status.InstalledVersion), status.Path)).Emit()
	case "stale":
		base.Pretty(fmt.Sprintf("! %s is STALE: installed %s, embedded %s\n  %s\n  Update with: %s", label, describeVersion(status.InstalledVersion), describeVersion(status.EmbeddedVersion), status.Path, installHint)).Emit()
	case "modified":
		base.Pretty(fmt.Sprintf("! %s has local modifications (version %s)\n  %s\n  Reinstall with: %s", label, describeVersion(status.InstalledVersion), status.Path, installHint)).Emit()
	}
}
