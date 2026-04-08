package view

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/keymap"
)

// KeyMap is the custom keymap for the session browser
type KeyMap struct {
	keymap.Base
	ToggleView      key.Binding
	Archive         key.Binding
	OpenDir         key.Binding
	ExportJSON      key.Binding
	ScrollDown      key.Binding
	ScrollUp        key.Binding
	Kill            key.Binding
	ToggleFilter    key.Binding
	SearchFilter    key.Binding
	GoToTop         key.Binding
	GoToBottom      key.Binding
	Edit            key.Binding
	Open            key.Binding
	JumpToWorkspace key.Binding
	MarkComplete    key.Binding
}

func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Quit}
}

func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.ScrollUp, k.ScrollDown, k.GoToTop, k.GoToBottom, k.JumpToWorkspace},
		{key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "action"),
		), k.Back, k.Select, k.SelectAll},
		{k.ToggleView, k.ToggleFilter, k.SearchFilter, k.Edit, k.Open, k.CopyPath, k.OpenDir, k.ExportJSON},
		{k.MarkComplete, k.Kill, k.Help, k.Quit},
	}
}

// Sections returns grouped sections of key bindings for the full help view.
// Only includes sections that the hooks browser actually implements.
func (k KeyMap) Sections() []keymap.Section {
	return []keymap.Section{
		{
			Name:     "Navigation",
			Bindings: []key.Binding{k.Up, k.Down, k.ScrollUp, k.ScrollDown, k.GoToTop, k.GoToBottom, k.JumpToWorkspace},
		},
		{
			Name:     "Selection",
			Bindings: []key.Binding{k.Select, k.SelectAll},
		},
		{
			Name:     "View",
			Bindings: []key.Binding{k.ToggleView, k.ToggleFilter, k.SearchFilter},
		},
		{
			Name:     "Actions",
			Bindings: []key.Binding{k.Edit, k.Open, k.CopyPath, k.OpenDir, k.ExportJSON, k.Archive},
		},
		{
			Name:     "Session",
			Bindings: []key.Binding{k.MarkComplete, k.Kill},
		},
		k.Base.SystemSection(),
	}
}

// NewKeyMap creates a new KeyMap with user configuration applied.
// Base bindings (navigation, actions, search, selection) come from keymap.Load().
// Only TUI-specific bindings are defined here.
func NewKeyMap(cfg *config.Config) KeyMap {
	km := KeyMap{
		Base: keymap.Load(cfg, "hooks.browser"),
		ToggleView: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "toggle view"),
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
			key.WithKeys("X"),
			key.WithHelp("X", "archive selected"),
		),
		OpenDir: key.NewBinding(
			key.WithKeys("ctrl+o"),
			key.WithHelp("ctrl+o", "open dir"),
		),
		ExportJSON: key.NewBinding(
			key.WithKeys("ctrl+j"),
			key.WithHelp("ctrl+j", "export json"),
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
		GoToTop: key.NewBinding(
			key.WithKeys("g"),
			key.WithHelp("gg", "go to top"),
		),
		GoToBottom: key.NewBinding(
			key.WithKeys("G"),
			key.WithHelp("G", "go to bottom"),
		),
		Edit: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "action"),
		),
		Open: key.NewBinding(
			key.WithKeys("o"),
			key.WithHelp("o", "action"),
		),
		JumpToWorkspace: key.NewBinding(
			key.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"),
			key.WithHelp("1-9", "jump"),
		),
		MarkComplete: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "mark complete"),
		),
	}

	// Apply TUI-specific overrides from config
	keymap.ApplyTUIOverrides(cfg, "hooks", "browser", &km)

	return km
}

// KeymapInfo returns the keymap metadata for the hooks session browser TUI.
// Used by the grove keys registry generator to aggregate all TUI keybindings.
func KeymapInfo() keymap.TUIInfo {
	km := NewKeyMap(nil)
	return keymap.MakeTUIInfo(
		"hooks-browser",
		"hooks",
		"Hook session browser and manager",
		km,
	)
}
