package browse

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/mattsolo1/grove-core/tui/keymap"
)

// KeyMap is the custom keymap for the session browser
type KeyMap struct {
	keymap.Base
	ToggleView   key.Binding
	Archive      key.Binding
	CopyID       key.Binding
	OpenDir      key.Binding
	ExportJSON   key.Binding
	SelectAll    key.Binding
	ScrollDown   key.Binding
	ScrollUp     key.Binding
	Kill         key.Binding
	ToggleFilter key.Binding
	SearchFilter key.Binding
}

func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{}
}

func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.ScrollUp, k.ScrollDown},
		{k.Confirm, k.Back, k.Select, k.SelectAll},
		{k.ToggleView, k.ToggleFilter, k.SearchFilter, k.CopyID, k.OpenDir, k.ExportJSON},
		{k.Kill, k.Help, k.Quit},
	}
}

func NewKeyMap() KeyMap {
	base := keymap.NewBase()
	return KeyMap{
		Base: base,
		ToggleView: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "toggle view"),
		),
		ToggleFilter: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("f", "toggle filter view"),
		),
		SearchFilter: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "search"),
		),
		Archive: key.NewBinding(
			key.WithKeys("ctrl+x"),
			key.WithHelp("ctrl+x", "archive selected"),
		),
		CopyID: key.NewBinding(
			key.WithKeys("ctrl+y"),
			key.WithHelp("ctrl+y", "copy id"),
		),
		OpenDir: key.NewBinding(
			key.WithKeys("ctrl+o"),
			key.WithHelp("ctrl+o", "open dir"),
		),
		ExportJSON: key.NewBinding(
			key.WithKeys("ctrl+j"),
			key.WithHelp("ctrl+j", "export json"),
		),
		SelectAll: key.NewBinding(
			key.WithKeys("ctrl+a"),
			key.WithHelp("ctrl+a", "select all"),
		),
		ScrollDown: key.NewBinding(
			key.WithKeys("ctrl+d", "pagedown"),
			key.WithHelp("ctrl+d/pgdn", "scroll down"),
		),
		ScrollUp: key.NewBinding(
			key.WithKeys("ctrl+u", "pageup"),
			key.WithHelp("ctrl+u/pgup", "scroll up"),
		),
		Kill: key.NewBinding(
			key.WithKeys("ctrl+k"),
			key.WithHelp("ctrl+k", "kill session"),
		),
	}
}
