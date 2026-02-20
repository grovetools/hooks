package cmd

import (
	"github.com/grovetools/core/tui/keymap"
	"github.com/grovetools/hooks/internal/tui/browse"
)

// BrowseKeymapInfo returns the keymap metadata for the hooks session browser TUI.
// Used by the grove keys registry generator to aggregate all TUI keybindings.
func BrowseKeymapInfo() keymap.TUIInfo {
	return browse.KeymapInfo()
}
