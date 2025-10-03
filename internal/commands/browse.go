package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
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
		Short:   "Browse sessions interactively with search and filtering",
		Long:    `Launch an interactive terminal UI to browse, search, and filter Claude sessions. Navigate with arrow keys, search by typing, and select sessions to view details.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create storage
			storage, err := disk.NewSQLiteStore()
			if err != nil {
				return fmt.Errorf("failed to create storage: %w", err)
			}
			defer storage.(*disk.SQLiteStore).Close()

			// Clean up dead sessions first
			_, _ = CleanupDeadSessions(storage)

			// Get all sessions
			sessions, err := storage.GetAllSessions()
			if err != nil {
				return fmt.Errorf("failed to get sessions: %w", err)
			}

			// Filter out completed sessions if requested
			if hideCompleted {
				var filtered []*models.Session
				for _, s := range sessions {
					if s.Status != "completed" && s.Status != "failed" && s.Status != "error" {
						filtered = append(filtered, s)
					}
				}
				sessions = filtered
			}

			if len(sessions) == 0 {
				fmt.Println("No sessions found.")
				return nil
			}

			// Sort sessions: running first, then idle, then others by started_at desc
			sort.Slice(sessions, func(i, j int) bool {
				// Define status priority: running=1, idle=2, others=3
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

				// Sort by priority first
				if iPriority != jPriority {
					return iPriority < jPriority
				}

				// Within same status group, sort by most recent first
				return sessions[i].StartedAt.After(sessions[j].StartedAt)
			})

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
				sessionType := s.Type
				if sessionType == "" {
					sessionType = "claude_session"
				}
				fmt.Printf("Type: %s\n", sessionType)
				fmt.Printf("Status: %s\n", s.Status)

				if s.Type == "oneshot_job" {
					// Oneshot job specific fields
					if s.PlanName != "" {
						fmt.Printf("Plan: %s\n", s.PlanName)
					}
					if s.JobTitle != "" {
						fmt.Printf("Job Title: %s\n", s.JobTitle)
					}
				} else {
					// Claude session specific fields
					fmt.Printf("Repository: %s\n", s.Repo)
					fmt.Printf("Branch: %s\n", s.Branch)
				}

				fmt.Printf("Started: %s\n", s.StartedAt.Format(time.RFC3339))
				if s.EndedAt != nil {
					fmt.Printf("Duration: %s\n", s.EndedAt.Sub(s.StartedAt).Round(time.Second))
				}
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
}

func (k browseKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{}
}

func (k browseKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Confirm, k.Back},
		{k.Select, k.SelectAll, k.Archive},
		{k.CycleFilter, k.CopyID, k.OpenDir, k.ExportJSON},
		{k.Help, k.Quit},
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
	}
}

// browseModel is the model for the interactive session browser
type browseModel struct {
	sessions        []*models.Session
	filtered        []*models.Session
	selectedSession *models.Session
	cursor          int
	filterInput     textinput.Model
	width           int
	height          int
	statusFilter    string // "", "running", "idle", "completed", "failed"
	showDetails     bool
	selectedIDs     map[string]bool // Track multiple selections
	storage         interfaces.SessionStorer
	lastRefresh     time.Time
	keys            browseKeyMap
	help            help.Model
}

func newBrowseModel(sessions []*models.Session, storage interfaces.SessionStorer) browseModel {
	// Create text input for filtering
	ti := textinput.New()
	ti.Placeholder = "Type to filter by repo, branch, user, or session ID..."
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
		// Refresh sessions data every second
		if !m.showDetails {
			// Clean up dead sessions
			_, _ = CleanupDeadSessions(m.storage)

			// Get updated sessions
			sessions, err := m.storage.GetAllSessions()
			if err == nil {
				// Sort sessions: running first, then idle, then others by started_at desc
				sort.Slice(sessions, func(i, j int) bool {
					// Define status priority: running=1, idle=2, others=3
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

					// Sort by priority first
					if iPriority != jPriority {
						return iPriority < jPriority
					}

					// Within same status group, sort by most recent first
					return sessions[i].StartedAt.After(sessions[j].StartedAt)
				})

				// Remember the currently selected session ID
				var selectedID string
				if m.cursor >= 0 && m.cursor < len(m.filtered) {
					selectedID = m.filtered[m.cursor].ID
				}

				// Update sessions
				m.sessions = sessions
				m.updateFiltered()

				// Try to maintain cursor position on the same session
				if selectedID != "" {
					for i, s := range m.filtered {
						if s.ID == selectedID {
							m.cursor = i
							break
						}
					}
				}

				// Ensure cursor is within bounds
				if len(m.filtered) == 0 {
					m.cursor = 0
				} else if m.cursor >= len(m.filtered) {
					m.cursor = len(m.filtered) - 1
				}

				// Update last refresh time
				m.lastRefresh = time.Now()
			}
		}

		// Continue ticking
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
			}

		} else if key.Matches(msg, m.keys.Down) {
			if !m.showDetails && m.cursor < len(m.filtered)-1 {
				m.cursor++
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
				m.statusFilter = "completed"
			case "completed":
				m.statusFilter = "failed"
			case "failed":
				m.statusFilter = ""
			}
			m.updateFiltered()
			m.cursor = 0

		} else if key.Matches(msg, m.keys.CopyID) {
			// Copy session ID to clipboard
			if m.cursor < len(m.filtered) {
				session := m.filtered[m.cursor]
				copyToClipboard(session.ID)
			}

		} else if key.Matches(msg, m.keys.OpenDir) {
			// Open working directory in file manager
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
				filename := fmt.Sprintf("session_%s.json", session.ID)
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

		} else if key.Matches(msg, m.keys.Archive) {
			// Archive selected sessions
			if len(m.selectedIDs) > 0 && !m.showDetails {
				// Get list of selected IDs
				sessionIDs := make([]string, 0, len(m.selectedIDs))
				for id := range m.selectedIDs {
					sessionIDs = append(sessionIDs, id)
				}

				// Archive them
				if err := m.storage.ArchiveSessions(sessionIDs); err == nil {
					// Remove archived sessions from the lists
					newSessions := []*models.Session{}
					for _, s := range m.sessions {
						if !m.selectedIDs[s.ID] {
							newSessions = append(newSessions, s)
						}
					}
					m.sessions = newSessions

					// Clear selections
					m.selectedIDs = make(map[string]bool)

					// Update filtered list
					m.updateFiltered()

					// Adjust cursor if needed
					if len(m.filtered) == 0 {
						m.cursor = 0
					} else if m.cursor >= len(m.filtered) {
						m.cursor = len(m.filtered) - 1
					}
				}
			}

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

func (m *browseModel) updateFiltered() {
	filter := strings.ToLower(m.filterInput.Value())
	m.filtered = []*models.Session{}

	for _, s := range m.sessions {
		// Apply status filter first
		if m.statusFilter != "" && s.Status != m.statusFilter {
			continue
		}

		// Apply text filter if present
		if filter != "" {
			// Include job-specific fields in search
			searchText := strings.ToLower(fmt.Sprintf("%s %s %s %s %s %s %s %s",
				s.ID, s.Repo, s.Branch, s.User, s.WorkingDirectory,
				s.PlanName, s.JobTitle, s.JobFilePath))
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

	headerLine := t.Header.Render("Grove Sessions") + "  " +
		t.Muted.Render(m.filterInput.View()) + "  " +
		t.Muted.Render("filter:") + t.Info.Render(filterText) + "  " +
		t.Success.Render("●")

	b.WriteString(headerLine)
	b.WriteString("\n")

	// Build table data
	headers := []string{"", "TYPE", "STATUS", "CONTEXT", "TITLE", "STARTED", "IN STATE"}
	var rows [][]string

	for i, session := range m.filtered {
		// Calculate time in current state
		inState := ""
		if session.Status == "running" || session.Status == "idle" {
			inState = time.Since(session.LastActivity).Round(time.Second).String()
		} else if session.EndedAt != nil {
			inState = session.EndedAt.Sub(session.StartedAt).Round(time.Second).String()
		} else {
			inState = time.Since(session.StartedAt).Round(time.Second).String()
		}

		// Format context and title based on session type
		context := ""
		title := ""
		sessionType := "claude"
		if session.Type == "oneshot_job" {
			sessionType = "job"
			if session.Repo != "" {
				context = session.Repo
				if session.Branch != "" {
					context = fmt.Sprintf("%s/%s", session.Repo, session.Branch)
				}
			} else {
				context = "n/a"
			}

			if session.JobTitle != "" {
				title = session.JobTitle
			} else if session.PlanName != "" {
				title = session.PlanName
			} else {
				title = "untitled"
			}
		} else {
			if session.Repo != "" && session.Branch != "" {
				context = fmt.Sprintf("%s/%s", session.Repo, session.Branch)
			} else if session.Repo != "" {
				context = session.Repo
			} else {
				context = "n/a"
			}
			title = "-"
		}

		// Selection and cursor indicator
		var indicator string
		isSelected := m.selectedIDs[session.ID]
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

		// Style the status based on session state
		statusStyle := getStatusStyle(session.Status)
		styledStatus := statusStyle.Render(session.Status)

		rows = append(rows, []string{
			indicator,
			sessionType,
			styledStatus,
			truncateStr(context, 45),
			truncateStr(title, 45),
			session.StartedAt.Format("2006-01-02 15:04:05"),
			truncateStr(inState, 18),
		})
	}

	// Render table using SelectableTable
	if len(m.filtered) > 0 {
		tableStr := gtable.SelectableTable(headers, rows, m.cursor)
		b.WriteString(tableStr)
	} else {
		b.WriteString("\n" + t.Muted.Render("No matching sessions"))
	}

	// Selection count and help on same line
	b.WriteString("\n")
	if len(m.selectedIDs) > 0 {
		b.WriteString(t.Highlight.Render(fmt.Sprintf("[%d selected]", len(m.selectedIDs))) + " ")
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

	// Basic info
	content.WriteString(components.RenderKeyValue("Session ID", s.ID))
	content.WriteString("\n")

	sessionType := s.Type
	if sessionType == "" {
		sessionType = "claude_session"
	}
	content.WriteString(components.RenderKeyValue("Type", sessionType))
	content.WriteString("\n")

	statusStyle := getStatusStyle(s.Status)
	content.WriteString(components.RenderKeyValue("Status", statusStyle.Render(s.Status)))
	content.WriteString("\n")

	if s.Type == "oneshot_job" {
		if s.PlanName != "" {
			content.WriteString(components.RenderKeyValue("Plan", s.PlanName))
			content.WriteString("\n")
		}
		if s.PlanDirectory != "" {
			content.WriteString(components.RenderKeyValue("Plan Directory", s.PlanDirectory))
			content.WriteString("\n")
		}
		if s.JobTitle != "" {
			content.WriteString(components.RenderKeyValue("Job Title", s.JobTitle))
			content.WriteString("\n")
		}
		if s.JobFilePath != "" {
			content.WriteString(components.RenderKeyValue("Job File", s.JobFilePath))
			content.WriteString("\n")
		}
	} else {
		content.WriteString(components.RenderKeyValue("Repository", s.Repo))
		content.WriteString("\n")
		content.WriteString(components.RenderKeyValue("Branch", s.Branch))
		content.WriteString("\n")
	}

	content.WriteString(components.RenderKeyValue("User", s.User))
	content.WriteString("\n")
	content.WriteString(components.RenderKeyValue("PID", fmt.Sprintf("%d", s.PID)))
	content.WriteString("\n")
	content.WriteString(components.RenderKeyValue("Working Directory", s.WorkingDirectory))
	content.WriteString("\n")

	// Timing info
	content.WriteString(components.RenderKeyValue("Started", s.StartedAt.Format("2006-01-02 15:04:05 MST")))
	content.WriteString("\n")
	if s.EndedAt != nil {
		content.WriteString(components.RenderKeyValue("Ended", s.EndedAt.Format("2006-01-02 15:04:05 MST")))
		content.WriteString("\n")
		content.WriteString(components.RenderKeyValue("Duration", s.EndedAt.Sub(s.StartedAt).Round(time.Second).String()))
		content.WriteString("\n")
	} else {
		content.WriteString(components.RenderKeyValue("Duration", time.Since(s.StartedAt).Round(time.Second).String()+" (ongoing)"))
		content.WriteString("\n")
	}
	content.WriteString(components.RenderKeyValue("Last Activity", s.LastActivity.Format("2006-01-02 15:04:05 MST")))
	content.WriteString("\n")

	if s.TmuxKey != "" {
		content.WriteString(components.RenderKeyValue("Tmux Key", s.TmuxKey))
		content.WriteString("\n")
	}

	// Tool statistics
	if s.ToolStats != nil {
		var statsContent strings.Builder
		statsContent.WriteString(components.RenderKeyValue("Total Calls", fmt.Sprintf("%d", s.ToolStats.TotalCalls)))
		statsContent.WriteString("\n")
		statsContent.WriteString(components.RenderKeyValue("Bash Commands", fmt.Sprintf("%d", s.ToolStats.BashCommands)))
		statsContent.WriteString("\n")
		statsContent.WriteString(components.RenderKeyValue("File Modifications", fmt.Sprintf("%d", s.ToolStats.FileModifications)))
		statsContent.WriteString("\n")
		statsContent.WriteString(components.RenderKeyValue("File Reads", fmt.Sprintf("%d", s.ToolStats.FileReads)))
		statsContent.WriteString("\n")
		statsContent.WriteString(components.RenderKeyValue("Search Operations", fmt.Sprintf("%d", s.ToolStats.SearchOperations)))
		statsContent.WriteString("\n")
		if s.ToolStats.TotalCalls > 0 {
			statsContent.WriteString(components.RenderKeyValue("Avg Tool Duration", fmt.Sprintf("%.0fms", s.ToolStats.AverageToolDuration)))
			statsContent.WriteString("\n")
		}

		content.WriteString("\n")
		content.WriteString(components.RenderSection("Tool Statistics", statsContent.String()))
		content.WriteString("\n")
	}

	// Session summary
	if s.SessionSummary != nil {
		var summaryContent strings.Builder
		summaryContent.WriteString(components.RenderKeyValue("Total Tools Used", fmt.Sprintf("%d", s.SessionSummary.TotalTools)))
		summaryContent.WriteString("\n")
		summaryContent.WriteString(components.RenderKeyValue("Files Modified", fmt.Sprintf("%d", s.SessionSummary.FilesModified)))
		summaryContent.WriteString("\n")
		summaryContent.WriteString(components.RenderKeyValue("Commands Executed", fmt.Sprintf("%d", s.SessionSummary.CommandsExecuted)))
		summaryContent.WriteString("\n")
		summaryContent.WriteString(components.RenderKeyValue("Errors Count", fmt.Sprintf("%d", s.SessionSummary.ErrorsCount)))
		summaryContent.WriteString("\n")
		summaryContent.WriteString(components.RenderKeyValue("Notifications", fmt.Sprintf("%d", s.SessionSummary.NotificationsSent)))
		summaryContent.WriteString("\n")

		content.WriteString("\n")
		content.WriteString(components.RenderSection("Session Summary", summaryContent.String()))
		content.WriteString("\n")

		// AI Summary if available
		if s.SessionSummary.AISummary != nil && s.SessionSummary.AISummary.CurrentActivity != "" {
			var aiContent strings.Builder
			aiContent.WriteString(components.RenderKeyValue("Current Activity", s.SessionSummary.AISummary.CurrentActivity))
			aiContent.WriteString("\n")

			if len(s.SessionSummary.AISummary.History) > 0 {
				aiContent.WriteString("\n")
				aiContent.WriteString(t.Muted.Render("Key Accomplishments:"))
				aiContent.WriteString("\n")
				for i, milestone := range s.SessionSummary.AISummary.History {
					aiContent.WriteString(fmt.Sprintf("  %s. %s\n",
						t.Highlight.Render(fmt.Sprintf("%d", i+1)),
						milestone.Summary))
				}
			}

			content.WriteString("\n")
			content.WriteString(components.RenderSection("AI Summary", aiContent.String()))
			content.WriteString("\n")
		}
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

