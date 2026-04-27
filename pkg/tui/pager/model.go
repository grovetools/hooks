package pager

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/embed"
	"github.com/grovetools/core/tui/keymap"
	"github.com/grovetools/hooks/pkg/tui/view"
)

// Model is the hooks session-browser meta-panel.
type Model struct {
	pager pager.Model
}

// New constructs a Model wrapping a fresh hooks view.
func New(cfg view.Config) Model {
	inner := view.New(cfg)
	page := &sessionsPage{inner: inner}
	return Model{pager: pager.NewWith([]pager.Page{page}, pager.KeyMapFromBase(keymap.NewBase()), pager.Config{
		OuterPadding: [4]int{1, 2, 0, 2},
		FooterHeight: 1,
	})}
}

func (m Model) Init() tea.Cmd { return m.pager.Init() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.pager, cmd = m.pager.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	// Build footer from the active page's status/help line and
	// delegate to the pager which pins it at the bottom of the
	// pane. The pager's OuterPadding provides the horizontal
	// indent so no extra padding is needed here.
	for _, p := range m.pager.Pages() {
		if sp, ok := p.(*sessionsPage); ok {
			m.pager.SetFooter(sp.inner.FooterView())
			break
		}
	}
	return m.pager.View()
}

// Close tears down the inner model's SSE stream.
func (m Model) Close() error {
	for _, p := range m.pager.Pages() {
		if sp, ok := p.(*sessionsPage); ok {
			_ = sp.inner.Close()
		}
	}
	return nil
}

// Compile-time interface checks.
var (
	_ pager.Page              = (*sessionsPage)(nil)
	_ pager.PageWithTextInput = (*sessionsPage)(nil)
)

// sessionsPage adapts hooks view.Model to pager.Page.
type sessionsPage struct {
	inner  view.Model
	width  int
	height int
}

func (p *sessionsPage) Name() string  { return "Sessions" }
func (p *sessionsPage) Init() tea.Cmd { return p.inner.Init() }

func (p *sessionsPage) View() string {
	body := strings.TrimPrefix(p.inner.View(), "\n")
	if p.width > 0 {
		body = lipgloss.NewStyle().MaxWidth(p.width).Render(body)
	}
	return body
}

func (p *sessionsPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	updated, cmd := p.inner.Update(msg)
	if m, ok := updated.(view.Model); ok {
		p.inner = m
	}
	return p, cmd
}

func (p *sessionsPage) Focus() tea.Cmd {
	updated, cmd := p.inner.Update(embed.FocusMsg{})
	if m, ok := updated.(view.Model); ok {
		p.inner = m
	}
	return cmd
}

func (p *sessionsPage) Blur() {
	updated, _ := p.inner.Update(embed.BlurMsg{})
	if m, ok := updated.(view.Model); ok {
		p.inner = m
	}
}

func (p *sessionsPage) SetSize(w, h int) {
	p.width = w
	p.height = h
	updated, _ := p.inner.Update(tea.WindowSizeMsg{Width: w, Height: h})
	if m, ok := updated.(view.Model); ok {
		p.inner = m
	}
}

// IsTextEntryActive prevents the pager from eating keystrokes while searching.
func (p *sessionsPage) IsTextEntryActive() bool {
	return p.inner.IsTextInputFocused()
}
