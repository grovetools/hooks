// Package extension embeds the Grove integration extension for the pi coding
// agent. The TypeScript module is installed to
// ~/.pi/agent/extensions/grove-integration.ts by `hooks pi install`; pi
// auto-loads *.ts files from that directory and calls the default export with
// its ExtensionAPI.
package extension

import _ "embed"

//go:embed grove-integration.ts
var GroveIntegrationExtension []byte
