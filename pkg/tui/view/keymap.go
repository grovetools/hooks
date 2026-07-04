package view

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/keymap"
)

// KeyMap is the custom keymap for the session browser.
//
// It embeds keymap.Base and prefers Base bindings for standard behavior
// (Up/Down/PageUp/PageDown scrolling, Top/Bottom via the gg sequence, Search,
// Edit, Confirm, Back, CopyPath, Select/SelectAll, Help/Quit). Only the
// bindings that are genuinely hooks-specific are declared as fields here.
// Base bindings the browser does not implement are disabled in NewKeyMap so
// help stays truthful and keymap.AuditCoverage reports no hidden bindings.
type KeyMap struct {
	keymap.Base
	ToggleView      key.Binding
	OpenDir         key.Binding
	ExportJSON      key.Binding
	Kill            key.Binding
	ToggleFilter    key.Binding
	Open            key.Binding
	JumpToWorkspace key.Binding
	MarkComplete    key.Binding
	ScopeToggle     key.Binding
}

func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Quit}
}

func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom, k.JumpToWorkspace},
		{k.Confirm, k.Back, k.Select, k.SelectAll},
		{k.ToggleView, k.ToggleFilter, k.Search, k.ScopeToggle, k.Edit, k.Open, k.CopyPath, k.OpenDir, k.ExportJSON},
		{k.MarkComplete, k.Kill, k.Help, k.Quit},
	}
}

// Sections returns grouped sections of key bindings for the full help view.
// Only includes sections (and bindings) that the hooks browser actually
// implements. Base bindings not represented here are disabled in NewKeyMap.
func (k KeyMap) Sections() []keymap.Section {
	return []keymap.Section{
		keymap.NavigationSection(
			k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom, k.JumpToWorkspace,
		),
		keymap.SelectionSection(k.Select, k.SelectAll),
		keymap.SearchSection(k.Search),
		keymap.NewSection(keymap.SectionView, k.ToggleView, k.ToggleFilter, k.ScopeToggle),
		keymap.ActionsSection(k.Confirm, k.Back, k.Edit, k.Open, k.CopyPath, k.OpenDir, k.ExportJSON),
		keymap.NewSection("Session", k.MarkComplete, k.Kill),
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
		OpenDir: key.NewBinding(
			key.WithKeys("ctrl+o"),
			key.WithHelp("ctrl+o", "open dir"),
		),
		ExportJSON: key.NewBinding(
			key.WithKeys("ctrl+j"),
			key.WithHelp("ctrl+j", "export json"),
		),
		Kill: key.NewBinding(
			key.WithKeys("ctrl+k"),
			key.WithHelp("ctrl+k", "kill session"),
		),
		Open: key.NewBinding(
			key.WithKeys("o"),
			key.WithHelp("o", "open agent session"),
		),
		JumpToWorkspace: key.NewBinding(
			key.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"),
			// Space in the label marks it as a synthetic range so AuditCoverage
			// treats it as an alternate list rather than a single-key label.
			key.WithHelp("1 - 9", "jump to session"),
		),
		MarkComplete: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "mark complete"),
		),
		ScopeToggle: key.NewBinding(
			key.WithKeys("alt+s"),
			key.WithHelp("alt+s", "local/global scope"),
		),
	}

	// The hooks browser reuses Base for scrolling/search/edit/confirm but does
	// not implement most Base bindings. Disable the unused ones so they neither
	// show up in help nor trip AuditCoverage. (gg/G top/bottom, PageUp/PageDown,
	// Search, Edit, Confirm, Back, CopyPath, Select/SelectAll, Help/Quit stay.)
	for _, b := range []*key.Binding{
		&km.Left, &km.Right, &km.Home, &km.End,
		&km.SearchNext, &km.SearchPrev, &km.ClearSearch, &km.Grep,
		&km.SelectNone,
		&km.SwitchView, &km.NextTab, &km.PrevTab, &km.FocusNext, &km.FocusPrev, &km.TogglePreview,
		&km.Tab1, &km.Tab2, &km.Tab3, &km.Tab4, &km.Tab5, &km.Tab6, &km.Tab7, &km.Tab8, &km.Tab9,
		&km.FoldOpen, &km.FoldClose, &km.FoldToggle, &km.FoldOpenAll, &km.FoldCloseAll,
		&km.Cancel, &km.Delete, &km.Yank, &km.Rename, &km.Refresh,
	} {
		b.SetEnabled(false)
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
