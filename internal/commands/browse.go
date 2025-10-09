package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-core/tui/components"
	"github.com/mattsolo1/grove-core/tui/components/help"
	gtable "github.com/mattsolo1/grove-core/tui/components/table"
	"github.com/mattsolo1/grove-core/tui/keymap"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/mattsolo1/grove-hooks/internal/storage/interfaces"
	"github.com/spf13/cobra"
)

func NewBrowseCmd() *cobra.Command {
	var hideCompleted bool

	cmd := &cobra.Command{
		Use:     "browse",
		Aliases: []string{"b"},
		Short:   "Browse sessions interactively",
		Long:    `Launch an interactive terminal UI to browse all sessions (Claude sessions and grove-flow jobs). Navigate with arrow keys, search by typing, and select sessions to view details.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create storage for session cleanup
			storage, err := disk.NewSQLiteStore()
			if err != nil {
				return fmt.Errorf("failed to create storage: %w", err)
			}
			defer storage.(*disk.SQLiteStore).Close()

			// Clean up dead sessions first
			_, _ = CleanupDeadSessions(storage)

			// Discover live Claude sessions from filesystem
			liveClaudeSessions, err := DiscoverLiveClaudeSessions()
			if err != nil {
				// Log error but continue
				if os.Getenv("GROVE_DEBUG") != "" {
					fmt.Fprintf(os.Stderr, "Warning: failed to discover live Claude sessions: %v\n", err)
				}
				liveClaudeSessions = []*models.Session{}
			}

			// Discover live grove-flow jobs from plan directories
			liveFlowJobs, err := DiscoverLiveFlowJobs()
			if err != nil {
				// Log error but continue
				if os.Getenv("GROVE_DEBUG") != "" {
					fmt.Fprintf(os.Stderr, "Warning: failed to discover live flow jobs: %v\n", err)
				}
				liveFlowJobs = []*models.Session{}
			}

			// Get archived sessions from database
			dbSessions, err := storage.GetAllSessions()
			if err != nil {
				return fmt.Errorf("failed to get sessions: %w", err)
			}

			// Merge all sources, prioritizing live sessions
			seenIDs := make(map[string]bool)
			sessions := make([]*models.Session, 0, len(liveClaudeSessions)+len(liveFlowJobs)+len(dbSessions))

			// Add live Claude sessions first
			for _, session := range liveClaudeSessions {
				sessions = append(sessions, session)
				seenIDs[session.ID] = true
			}

			// Add live flow jobs
			for _, session := range liveFlowJobs {
				sessions = append(sessions, session)
				seenIDs[session.ID] = true
			}

			// Add DB sessions that aren't already in live sessions
			for _, session := range dbSessions {
				if !seenIDs[session.ID] {
					sessions = append(sessions, session)
				}
			}

			// Filter by hideCompleted if requested
			if hideCompleted {
				var filtered []*models.Session
				for _, s := range sessions {
					if s.Status != "completed" && s.Status != "failed" && s.Status != "error" {
						filtered = append(filtered, s)
					}
				}
				sessions = filtered
			}

			// Sort sessions: running first, then idle, then others by started_at desc
			sort.Slice(sessions, func(i, j int) bool {
				iPriority := 3
				if sessions[i].Status == "running" {
					iPriority = 1
				} else if sessions[i].Status == "idle" {
					iPriority = 2
				}

				jPriority := 3
				if sessions[j].Status == "running" {
					jPriority = 1
				} else if sessions[j].Status == "idle" {
					jPriority = 2
				}

				if iPriority != jPriority {
					return iPriority < jPriority
				}

				return sessions[i].StartedAt.After(sessions[j].StartedAt)
			})

			if len(sessions) == 0 {
				fmt.Println("No sessions found")
				return nil
			}

			// Create the interactive model
			m := newBrowseModel(sessions, storage)

			// Run the interactive program
			p := tea.NewProgram(m, tea.WithAltScreen())
			finalModel, err := p.Run()
			if err != nil {
				return fmt.Errorf("error running program: %w", err)
			}

			// Check if a session was selected
			if bm, ok := finalModel.(browseModel); ok && bm.selectedSession != nil {
				// Output the selected session details
				s := bm.selectedSession
				fmt.Printf("\nSelected Session: %s\n", s.ID)
				fmt.Printf("Status: %s\n", s.Status)
				fmt.Printf("Type: %s\n", s.Type)
				if s.Repo != "" {
					fmt.Printf("Repo: %s/%s\n", s.Repo, s.Branch)
				}
				fmt.Printf("Working Directory: %s\n", s.WorkingDirectory)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&hideCompleted, "active", false, "Show only active sessions (hide completed/failed)")

	return cmd
}

// browseKeyMap is the custom keymap for the session browser
type browseKeyMap struct {
	keymap.Base
	CycleFilter key.Binding
	Archive     key.Binding
	CopyID      key.Binding
	OpenDir     key.Binding
	ExportJSON  key.Binding
	SelectAll   key.Binding
	ScrollDown  key.Binding
	ScrollUp    key.Binding
	Kill        key.Binding
}

func (k browseKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{}
}

func (k browseKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.ScrollUp, k.ScrollDown},
		{k.Confirm, k.Back, k.Select, k.SelectAll},
		{k.CycleFilter, k.CopyID, k.OpenDir, k.ExportJSON},
		{k.Kill, k.Help, k.Quit},
	}
}

func newBrowseKeyMap() browseKeyMap {
	base := keymap.NewBase()
	return browseKeyMap{
		Base: base,
		CycleFilter: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "cycle filter"),
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

// browseModel is the model for the interactive session browser
type browseModel struct {
	sessions        []*models.Session
	filtered        []*models.Session
	selectedSession *models.Session
	cursor          int
	scrollOffset    int // For viewport scrolling
	filterInput     textinput.Model
	width           int
	height          int
	statusFilter    string // "", "running", "idle", "completed", "interrupted"
	showDetails     bool
	selectedIDs     map[string]bool // Track multiple selections by ID
	storage         interfaces.SessionStorer
	lastRefresh     time.Time
	keys            browseKeyMap
	help            help.Model
	statusMessage   string // For showing kill/error messages
}

func newBrowseModel(sessions []*models.Session, storage interfaces.SessionStorer) browseModel {
	// Create text input for filtering
	ti := textinput.New()
	ti.Placeholder = "Type to filter by session ID, repo, branch, or working directory..."
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 60

	// Style the text input with grove-core theme
	t := theme.DefaultTheme
	ti.PromptStyle = t.Muted
	ti.Cursor.Style = t.Cursor
	ti.TextStyle = t.Input

	// Create keymap and help model
	keys := newBrowseKeyMap()

	return browseModel{
		sessions:     sessions,
		filtered:     sessions,
		filterInput:  ti,
		cursor:       0,
		scrollOffset: 0,
		statusFilter: "",
		showDetails:  false,
		selectedIDs:  make(map[string]bool),
		storage:      storage,
		lastRefresh:  time.Now(),
		keys:         keys,
		help:         help.New(keys),
	}
}

// tickMsg is sent on a regular interval for refreshing data
type tickMsg time.Time

func (m browseModel) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return tickMsg(t)
		}),
	)
}

func (m browseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tickMsg:
		// For sessions, we don't need auto-refresh since sessions are discovered once
		// Just continue ticking for potential future use
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return tickMsg(t)
		})

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.SetSize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyMsg:
		// Handle help toggle first
		if key.Matches(msg, m.keys.Help) {
			m.help.Toggle()
			return m, nil
		}

		// Handle quit even when help is shown
		if key.Matches(msg, m.keys.Quit) {
			if m.help.ShowAll {
				// If help is showing, close it instead of quitting
				m.help.Toggle()
				return m, nil
			}
			if m.showDetails {
				m.showDetails = false
				return m, nil
			}
			return m, tea.Quit
		}

		// Handle back/escape
		if key.Matches(msg, m.keys.Back) {
			if m.help.ShowAll {
				m.help.Toggle()
				return m, nil
			}
			if m.showDetails {
				m.showDetails = false
				return m, nil
			}
			return m, tea.Quit
		}

		// If help is shown, ignore other keys
		if m.help.ShowAll {
			return m, nil
		}

		// Handle other key input
		if false {

		} else if key.Matches(msg, m.keys.Up) {
			if !m.showDetails && m.cursor > 0 {
				m.cursor--
				// Scroll up if cursor goes above viewport
				if m.cursor < m.scrollOffset {
					m.scrollOffset = m.cursor
				}
			}

		} else if key.Matches(msg, m.keys.Down) {
			if !m.showDetails && m.cursor < len(m.filtered)-1 {
				m.cursor++
				// Scroll down if cursor goes below viewport
				viewportHeight := m.getViewportHeight()
				if m.cursor >= m.scrollOffset+viewportHeight {
					m.scrollOffset = m.cursor - viewportHeight + 1
				}
			}

		} else if msg.String() == "ctrl+d" || msg.String() == "pagedown" {
			// Scroll down half page
			if !m.showDetails {
				viewportHeight := m.getViewportHeight()
				m.scrollOffset += viewportHeight / 2
				maxScroll := len(m.filtered) - viewportHeight
				if maxScroll < 0 {
					maxScroll = 0
				}
				if m.scrollOffset > maxScroll {
					m.scrollOffset = maxScroll
				}
				// Move cursor with scroll
				if m.cursor < m.scrollOffset {
					m.cursor = m.scrollOffset
				}
			}

		} else if msg.String() == "ctrl+u" || msg.String() == "pageup" {
			// Scroll up half page
			if !m.showDetails {
				viewportHeight := m.getViewportHeight()
				m.scrollOffset -= viewportHeight / 2
				if m.scrollOffset < 0 {
					m.scrollOffset = 0
				}
				// Move cursor with scroll
				if m.cursor >= m.scrollOffset+viewportHeight {
					m.cursor = m.scrollOffset + viewportHeight - 1
				}
				if m.cursor >= len(m.filtered) {
					m.cursor = len(m.filtered) - 1
				}
			}

		} else if key.Matches(msg, m.keys.Confirm) {
			if m.cursor < len(m.filtered) {
				if m.showDetails {
					// If showing details, enter exits
					return m, tea.Quit
				} else {
					// Toggle details view
					m.selectedSession = m.filtered[m.cursor]
					m.showDetails = true
				}
			}

		} else if key.Matches(msg, m.keys.CycleFilter) {
			// Cycle through status filters
			switch m.statusFilter {
			case "":
				m.statusFilter = "running"
			case "running":
				m.statusFilter = "idle"
			case "idle":
				m.statusFilter = "interrupted"
			case "interrupted":
				m.statusFilter = "completed"
			case "completed":
				m.statusFilter = ""
			}
			m.updateFiltered()
			m.cursor = 0
			m.scrollOffset = 0

		} else if key.Matches(msg, m.keys.CopyID) {
			// Copy session ID to clipboard
			if m.cursor < len(m.filtered) {
				session := m.filtered[m.cursor]
				copyToClipboard(session.ID)
			}

		} else if key.Matches(msg, m.keys.OpenDir) {
			// Open session working directory in file manager
			if m.cursor < len(m.filtered) {
				session := m.filtered[m.cursor]
				if session.WorkingDirectory != "" {
					openInFileManager(session.WorkingDirectory)
				}
			}

		} else if key.Matches(msg, m.keys.ExportJSON) {
			// Export selected session as JSON
			if m.cursor < len(m.filtered) {
				session := m.filtered[m.cursor]
				data, _ := json.MarshalIndent(session, "", "  ")
				filename := fmt.Sprintf("session_%s.json", session.ID[:8])
				os.WriteFile(filename, data, 0644)
			}

		} else if key.Matches(msg, m.keys.Select) {
			// Toggle selection on current session when not in details view
			if m.cursor < len(m.filtered) && !m.showDetails {
				session := m.filtered[m.cursor]
				if m.selectedIDs[session.ID] {
					delete(m.selectedIDs, session.ID)
				} else {
					m.selectedIDs[session.ID] = true
				}
				// Don't open details view when using space for selection
				return m, nil
			}

		} else if key.Matches(msg, m.keys.SelectAll) {
			// Select/Deselect all filtered items
			if len(m.filtered) > 0 && !m.showDetails {
				// Check if all filtered items are already selected
				allSelected := true
				for _, s := range m.filtered {
					if !m.selectedIDs[s.ID] {
						allSelected = false
						break
					}
				}

				if allSelected {
					// If all are selected, deselect all filtered items
					for _, s := range m.filtered {
						delete(m.selectedIDs, s.ID)
					}
				} else {
					// Otherwise, select all filtered items
					for _, s := range m.filtered {
						m.selectedIDs[s.ID] = true
					}
				}
			}

		} else if key.Matches(msg, m.keys.Kill) || msg.String() == "ctrl+k" {
			// Kill the selected session
			if m.cursor < len(m.filtered) && !m.showDetails {
				session := m.filtered[m.cursor]

				// Only kill Claude sessions (not flow jobs)
				if session.Type == "" || session.Type == "claude_session" {
					// Find session directory
					groveSessionsDir := expandPath("~/.grove/hooks/sessions")
					sessionDir := filepath.Join(groveSessionsDir, session.ID)
					pidFile := filepath.Join(sessionDir, "pid.lock")

					// Read PID
					pidContent, err := os.ReadFile(pidFile)
					if err != nil {
						m.statusMessage = fmt.Sprintf("Error: failed to read PID file: %v", err)
						return m, nil
					}

					var pid int
					if _, err := fmt.Sscanf(string(pidContent), "%d", &pid); err != nil {
						m.statusMessage = fmt.Sprintf("Error: invalid PID: %v", err)
						return m, nil
					}

					// Kill the process
					if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
						m.statusMessage = fmt.Sprintf("Error: failed to kill PID %d: %v", pid, err)
						return m, nil
					}

					// Clean up session directory
					os.RemoveAll(sessionDir)

					// Remove from filtered list
					m.filtered = append(m.filtered[:m.cursor], m.filtered[m.cursor+1:]...)
					if m.cursor >= len(m.filtered) && m.cursor > 0 {
						m.cursor--
					}

					m.statusMessage = fmt.Sprintf("Killed session %s (PID %d)", session.ID[:8], pid)
				} else {
					m.statusMessage = "Error: Can only kill Claude sessions, not flow jobs"
				}
			}

		} else if key.Matches(msg, m.keys.Archive) {
			// Archive functionality removed for workspace dashboard
			// Projects themselves aren't archived, only sessions are

		} else {
			if !m.showDetails {
				// Update filter input
				prevValue := m.filterInput.Value()
				m.filterInput, cmd = m.filterInput.Update(msg)

				// If the filter changed, update filtered list
				if m.filterInput.Value() != prevValue {
					m.updateFiltered()
					m.cursor = 0
				}
				return m, cmd
			}
		}
	}

	return m, nil
}

func (m browseModel) getViewportHeight() int {
	// Calculate available height for the list
	// Account for: header (3 lines), help (1 line), selection count (1 line), padding
	const headerLines = 3
	const footerLines = 2
	availableHeight := m.height - headerLines - footerLines
	if availableHeight < 1 {
		return 1
	}
	return availableHeight
}

func (m browseModel) generateScrollbar(viewHeight int) []string {
	if len(m.filtered) == 0 || viewHeight == 0 {
		return []string{}
	}

	scrollbar := make([]string, viewHeight)

	// Calculate scrollbar thumb size and position
	totalItems := len(m.filtered)
	if totalItems <= viewHeight {
		// No need for scrollbar if all items fit
		for i := 0; i < viewHeight; i++ {
			scrollbar[i] = " "
		}
		return scrollbar
	}

	thumbSize := max(1, (viewHeight*viewHeight)/totalItems)

	// Calculate thumb position
	maxScroll := totalItems - viewHeight
	scrollProgress := 0.0
	if maxScroll > 0 {
		scrollProgress = float64(m.scrollOffset) / float64(maxScroll)
	}
	if scrollProgress < 0 {
		scrollProgress = 0
	}
	if scrollProgress > 1 {
		scrollProgress = 1
	}
	thumbStart := int(scrollProgress * float64(viewHeight-thumbSize))

	// Generate scrollbar characters
	for i := 0; i < viewHeight; i++ {
		if i >= thumbStart && i < thumbStart+thumbSize {
			scrollbar[i] = "█" // Thumb
		} else {
			scrollbar[i] = "░" // Track
		}
	}

	return scrollbar
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m *browseModel) updateFiltered() {
	filter := strings.ToLower(m.filterInput.Value())
	m.filtered = []*models.Session{}

	for _, s := range m.sessions {
		// Apply status filter first
		if m.statusFilter != "" {
			if s.Status != m.statusFilter {
				continue
			}
		}

		// Apply text filter if present
		if filter != "" {
			// Build search text from session info
			sessionType := s.Type
			if sessionType == "" {
				sessionType = "claude"
			}
			searchText := strings.ToLower(fmt.Sprintf("%s %s %s %s %s %s %s",
				s.ID, s.Repo, s.Branch, s.WorkingDirectory, s.User, sessionType, s.PlanName))
			if !strings.Contains(searchText, filter) {
				continue
			}
		}

		m.filtered = append(m.filtered, s)
	}
}

func (m browseModel) View() string {
	// Show help if toggled
	if m.help.ShowAll {
		return m.help.View()
	}

	if m.showDetails && m.selectedSession != nil {
		return m.viewDetails()
	}

	t := theme.DefaultTheme
	var b strings.Builder

	// Compact header with filter input on same line
	filterText := "all"
	if m.statusFilter != "" {
		filterText = m.statusFilter
	}

	headerLine := t.Header.Render("Grove Sessions Browser") + "  " +
		t.Muted.Render(m.filterInput.View()) + "  " +
		t.Muted.Render("filter:") + t.Info.Render(filterText) + "  " +
		t.Success.Render("●")

	b.WriteString(headerLine)
	b.WriteString("\n")

	// Build table data with viewport scrolling (matching sessions list format)
	headers := []string{"", "SESSION ID", "TYPE", "STATUS", "CONTEXT", "USER", "STARTED"}
	var rows [][]string

	// Calculate visible range
	viewportHeight := m.getViewportHeight()
	startIdx := m.scrollOffset
	endIdx := m.scrollOffset + viewportHeight
	if endIdx > len(m.filtered) {
		endIdx = len(m.filtered)
	}

	// Only render visible rows
	for i := startIdx; i < endIdx; i++ {
		s := m.filtered[i]

		// Format session type
		sessionType := s.Type
		if sessionType == "" {
			sessionType = "claude"
		} else if sessionType == "oneshot_job" {
			sessionType = "job"
		}

		// Format context (repo/branch or plan name)
		context := ""
		if sessionType == "job" {
			if s.Repo != "" && s.Branch != "" {
				context = fmt.Sprintf("%s/%s", s.Repo, s.Branch)
			} else if s.PlanName != "" {
				context = s.PlanName
			} else {
				context = "oneshot"
			}
		} else {
			if s.Repo != "" && s.Branch != "" {
				context = fmt.Sprintf("%s/%s", s.Repo, s.Branch)
			} else if s.Repo != "" {
				context = s.Repo
			} else {
				context = "n/a"
			}
		}

		// Format status with color
		statusStyle := getStatusStyle(s.Status)
		statusStr := statusStyle.Render(s.Status)

		// Selection and cursor indicator
		var indicator string
		isSelected := m.selectedIDs[s.ID]
		isCursor := i == m.cursor

		if isSelected && isCursor {
			indicator = "[*]▶"
		} else if isSelected {
			indicator = "[*] "
		} else if isCursor {
			indicator = "  ▶"
		} else {
			indicator = "   "
		}

		rows = append(rows, []string{
			indicator,
			truncateStr(s.ID, 12),
			sessionType,
			statusStr,
			truncateStr(context, 30),
			s.User,
			s.StartedAt.Format("2006-01-02 15:04"),
		})
	}

	// Render table using SelectableTable
	if len(m.filtered) > 0 {
		// Adjust cursor for visible range
		visibleCursor := m.cursor - m.scrollOffset
		tableStr := gtable.SelectableTable(headers, rows, visibleCursor)

		// Add scrollbar to the right of the table
		scrollbar := m.generateScrollbar(viewportHeight)
		tableLines := strings.Split(tableStr, "\n")

		var combinedLines []string
		for i, line := range tableLines {
			scrollbarChar := " "
			if i < len(scrollbar) {
				scrollbarChar = scrollbar[i]
			}
			combinedLines = append(combinedLines, line+scrollbarChar)
		}

		b.WriteString(strings.Join(combinedLines, "\n"))
	} else {
		b.WriteString("\n" + t.Muted.Render("No matching sessions"))
	}

	// Selection count, scroll position, status message, and help
	b.WriteString("\n")
	if len(m.selectedIDs) > 0 {
		b.WriteString(t.Highlight.Render(fmt.Sprintf("[%d selected]", len(m.selectedIDs))) + " ")
	}
	// Show scroll position if there are more items than viewport
	if len(m.filtered) > viewportHeight {
		scrollInfo := fmt.Sprintf("(%d-%d of %d)", startIdx+1, endIdx, len(m.filtered))
		b.WriteString(t.Muted.Render(scrollInfo) + " ")
	}
	// Show status message if present
	if m.statusMessage != "" {
		// Style based on whether it's an error
		if strings.HasPrefix(m.statusMessage, "Error:") {
			b.WriteString(t.Error.Render(m.statusMessage) + " ")
		} else {
			b.WriteString(t.Success.Render(m.statusMessage) + " ")
		}
	}
	b.WriteString(m.help.View())

	return b.String()
}

func getStatusStyle(status string) lipgloss.Style {
	t := theme.DefaultTheme
	switch status {
	case "running":
		return t.Success
	case "idle":
		return t.Warning
	case "completed":
		return t.Info
	case "failed", "error":
		return t.Error
	default:
		return t.Muted
	}
}

func (m browseModel) viewDetails() string {
	if m.selectedSession == nil {
		return "No session selected"
	}

	s := m.selectedSession
	t := theme.DefaultTheme
	var content strings.Builder

	// Basic session info
	content.WriteString(components.RenderKeyValue("Session ID", s.ID))
	content.WriteString("\n")

	// Session type
	sessionType := s.Type
	if sessionType == "" {
		sessionType = "claude"
	} else if sessionType == "oneshot_job" {
		sessionType = "job"
	}
	content.WriteString(components.RenderKeyValue("Type", sessionType))
	content.WriteString("\n")

	// Status with color
	statusStyle := getStatusStyle(s.Status)
	content.WriteString(components.RenderKeyValue("Status", statusStyle.Render(s.Status)))
	content.WriteString("\n")

	// Context info
	if s.Repo != "" {
		content.WriteString(components.RenderKeyValue("Repository", s.Repo))
		content.WriteString("\n")
		if s.Branch != "" {
			content.WriteString(components.RenderKeyValue("Branch", s.Branch))
			content.WriteString("\n")
		}
	}

	if s.PlanName != "" {
		content.WriteString(components.RenderKeyValue("Plan", s.PlanName))
		content.WriteString("\n")
	}

	if s.JobTitle != "" {
		content.WriteString(components.RenderKeyValue("Job Title", s.JobTitle))
		content.WriteString("\n")
	}

	content.WriteString(components.RenderKeyValue("Working Directory", s.WorkingDirectory))
	content.WriteString("\n")
	content.WriteString(components.RenderKeyValue("User", s.User))
	content.WriteString("\n")

	// Timing info
	content.WriteString("\n")
	content.WriteString(components.RenderKeyValue("Started", s.StartedAt.Format(time.RFC3339)))
	content.WriteString("\n")

	if s.EndedAt != nil {
		content.WriteString(components.RenderKeyValue("Ended", s.EndedAt.Format(time.RFC3339)))
		content.WriteString("\n")
		duration := s.EndedAt.Sub(s.StartedAt).Round(time.Second)
		content.WriteString(components.RenderKeyValue("Duration", duration.String()))
		content.WriteString("\n")
	} else {
		duration := time.Since(s.StartedAt).Round(time.Second)
		content.WriteString(components.RenderKeyValue("Duration", duration.String()))
		content.WriteString("\n")
	}

	// Process info
	if s.PID > 0 {
		content.WriteString("\n")
		content.WriteString(components.RenderKeyValue("PID", fmt.Sprintf("%d", s.PID)))
		content.WriteString("\n")
	}

	if s.TmuxKey != "" {
		content.WriteString(components.RenderKeyValue("Tmux Key", s.TmuxKey))
		content.WriteString("\n")
	}

	// Help
	content.WriteString("\n")
	content.WriteString(t.Muted.Render("enter/esc: back to list"))

	// Wrap in a box
	return components.RenderBox("Session Details", content.String(), m.width)
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func copyToClipboard(text string) {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("pbcopy")
	} else {
		// Try xclip first, then xsel
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else {
			// No clipboard utility found
			return
		}
	}

	if cmd != nil {
		cmd.Stdin = strings.NewReader(text)
		cmd.Run()
	}
}

func openInFileManager(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "linux":
		cmd = exec.Command("xdg-open", path)
	case "windows":
		cmd = exec.Command("explorer", path)
	}

	if cmd != nil {
		cmd.Start()
	}
}

