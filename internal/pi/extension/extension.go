// Package extension embeds the Grove integration extension for the pi coding
// agent. The TypeScript module is installed to
// ~/.pi/agent/extensions/grove-integration.ts by `hooks pi install`; pi
// auto-loads *.ts files from that directory and calls the default export with
// its ExtensionAPI.
package extension

import (
	_ "embed"

	"github.com/grovetools/hooks/internal/pluginversion"
)

//go:embed grove-integration.ts
var GroveIntegrationExtension []byte

// EmbeddedVersion returns the GROVE_PLUGIN_VERSION stamped in the embedded
// extension source. The TS file is the single source of truth for the
// version.
func EmbeddedVersion() string {
	return pluginversion.Extract(GroveIntegrationExtension)
}
