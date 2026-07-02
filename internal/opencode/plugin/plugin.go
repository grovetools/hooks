package plugin

import (
	_ "embed"

	"github.com/grovetools/hooks/internal/pluginversion"
)

//go:embed grove-integration.ts
var GroveIntegrationPlugin []byte

// EmbeddedVersion returns the GROVE_PLUGIN_VERSION stamped in the embedded
// plugin source. The TS file is the single source of truth for the version;
// TestEmbeddedPluginVersion guards that the stamp stays parseable.
func EmbeddedVersion() string {
	return pluginversion.Extract(GroveIntegrationPlugin)
}
