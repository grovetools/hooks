// Package pluginversion extracts the GROVE_PLUGIN_VERSION stamp from
// embedded provider-integration artifacts (the opencode plugin and pi
// extension TypeScript files). The version lives in the artifact itself —
// `export const GROVE_PLUGIN_VERSION = "x.y.z"` — so the installed file and
// the embedded copy can be compared for drift by `hooks <provider> status`.
package pluginversion

import "regexp"

var versionRe = regexp.MustCompile(`GROVE_PLUGIN_VERSION\s*=\s*"([^"]+)"`)

// Extract returns the GROVE_PLUGIN_VERSION stamp in src, or "" when the
// artifact is unstamped (pre-versioning installs).
func Extract(src []byte) string {
	m := versionRe.FindSubmatch(src)
	if m == nil {
		return ""
	}
	return string(m[1])
}
