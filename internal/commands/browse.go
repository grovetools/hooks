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

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/spf13/cobra"
)

func NewBrowseCmd() *cobra.Command {
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

			if len(sessions) == 0 {
				fmt.Println("No sessions found.")
				return nil
			}

			// Sort sessions by started_at desc (most recent first)
			sort.Slice(sessions, func(i, j int) bool {
				return sessions[i].StartedAt.After(sessions[j].StartedAt)
			})

			// Create the interactive model
			m := newBrowseModel(sessions)

			// Run the interactive program
			p := tea.NewProgram(m, tea.WithAltScreen())
			finalModel, err := p.Run()
			if err != nil {
				return fmt.Errorf("error running program: %w", err)
			}

			// Check if a session was selected
			if bm, ok := finalModel.(browseModel); ok && bm.selectedSession != nil {
				// Output the selected session details
				fmt.Printf("\nSelected Session: %s\n", bm.selectedSession.ID)
				fmt.Printf("Status: %s\n", bm.selectedSession.Status)
				fmt.Printf("Repository: %s\n", bm.selectedSession.Repo)
				fmt.Printf("Branch: %s\n", bm.selectedSession.Branch)
				fmt.Printf("Started: %s\n", bm.selectedSession.StartedAt.Format(time.RFC3339))
				if bm.selectedSession.EndedAt != nil {
					fmt.Printf("Duration: %s\n", bm.selectedSession.EndedAt.Sub(bm.selectedSession.StartedAt).Round(time.Second))
				}
			}

			return nil
		},
	}

	return cmd
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
}

func newBrowseModel(sessions []*models.Session) browseModel {
	// Create text input for filtering
	ti := textinput.New()
	ti.Placeholder = "Type to filter by repo, branch, user, or session ID..."
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 60

	return browseModel{
		sessions:     sessions,
		filtered:     sessions,
		filterInput:  ti,
		cursor:       0,
		statusFilter: "",
		showDetails:  false,
	}
}

func (m browseModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m browseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// Handle key input
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			if m.showDetails {
				m.showDetails = false
				return m, nil
			}
			return m, tea.Quit

		case tea.KeyUp, tea.KeyCtrlP:
			if !m.showDetails && m.cursor > 0 {
				m.cursor--
			}

		case tea.KeyDown, tea.KeyCtrlN:
			if !m.showDetails && m.cursor < len(m.filtered)-1 {
				m.cursor++
			}

		case tea.KeyEnter, tea.KeySpace:
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

		case tea.KeyTab:
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

		case tea.KeyCtrlY:
			// Copy session ID to clipboard
			if m.cursor < len(m.filtered) {
				session := m.filtered[m.cursor]
				copyToClipboard(session.ID)
			}

		case tea.KeyCtrlO:
			// Open working directory in file manager
			if m.cursor < len(m.filtered) {
				session := m.filtered[m.cursor]
				if session.WorkingDirectory != "" {
					openInFileManager(session.WorkingDirectory)
				}
			}

		case tea.KeyCtrlJ:
			// Export selected session as JSON
			if m.cursor < len(m.filtered) {
				session := m.filtered[m.cursor]
				data, _ := json.MarshalIndent(session, "", "  ")
				filename := fmt.Sprintf("session_%s.json", session.ID)
				os.WriteFile(filename, data, 0644)
			}

		default:
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
			searchText := strings.ToLower(fmt.Sprintf("%s %s %s %s %s",
				s.ID, s.Repo, s.Branch, s.User, s.WorkingDirectory))
			if !strings.Contains(searchText, filter) {
				continue
			}
		}

		m.filtered = append(m.filtered, s)
	}
}

func (m browseModel) View() string {
	if m.showDetails && m.selectedSession != nil {
		return m.viewDetails()
	}

	var b strings.Builder

	// Header with filter input
	b.WriteString(m.filterInput.View())
	b.WriteString("\n")

	// Status filter indicator
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
	if m.statusFilter != "" {
		filterText := fmt.Sprintf("Filter: %s", m.statusFilter)
		b.WriteString(statusStyle.Render(filterText))
	} else {
		b.WriteString(statusStyle.Render("Filter: all"))
	}
	b.WriteString("\n\n")

	// Calculate visible items
	visibleHeight := m.height - 7 // Reserve space for header and help
	if visibleHeight < 5 {
		visibleHeight = 5
	}

	// Determine visible range with scrolling
	start := 0
	end := len(m.filtered)

	if end > visibleHeight {
		// Center the cursor in the visible area when possible
		if m.cursor < visibleHeight/2 {
			start = 0
		} else if m.cursor >= len(m.filtered)-visibleHeight/2 {
			start = len(m.filtered) - visibleHeight
		} else {
			start = m.cursor - visibleHeight/2
		}

		end = start + visibleHeight
		if end > len(m.filtered) {
			end = len(m.filtered)
		}
		if start < 0 {
			start = 0
		}
	}

	// Table header
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ecdc4"))
	b.WriteString(headerStyle.Render("STATUS     REPO               BRANCH     USER      STARTED              DURATION"))
	b.WriteString("\n")

	// Render visible sessions
	for i := start; i < end && i < len(m.filtered); i++ {
		session := m.filtered[i]

		// Calculate duration
		duration := "running"
		if session.EndedAt != nil {
			duration = session.EndedAt.Sub(session.StartedAt).Round(time.Second).String()
		} else if session.Status == "idle" {
			duration = time.Since(session.StartedAt).Round(time.Second).String() + " (idle)"
		} else if session.Status == "running" {
			duration = time.Since(session.StartedAt).Round(time.Second).String()
		}

		// Status color
		statusColor := "#808080"
		switch session.Status {
		case "running":
			statusColor = "#00ff00"
		case "idle":
			statusColor = "#ffaa00"
		case "completed":
			statusColor = "#4ecdc4"
		case "failed", "error":
			statusColor = "#ff4444"
		}

		// Format the row
		row := fmt.Sprintf("%-10s %-18s %-10s %-9s %-20s %s",
			truncateStr(session.Status, 10),
			truncateStr(session.Repo, 18),
			truncateStr(session.Branch, 10),
			truncateStr(session.User, 9),
			session.StartedAt.Format("2006-01-02 15:04:05"),
			truncateStr(duration, 20),
		)

		// Apply styling
		if i == m.cursor {
			// Highlight selected row
			indicator := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#00ff00")).
				Bold(true).
				Render("▶ ")

			rowStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(statusColor)).
				Bold(true)

			b.WriteString(indicator + rowStyle.Render(row))
		} else {
			// Normal row
			rowStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(statusColor))

			b.WriteString("  " + rowStyle.Render(row))
		}
		b.WriteString("\n")
	}

	// Show scroll indicators if needed
	if start > 0 || end < len(m.filtered) {
		scrollInfo := fmt.Sprintf("\n(%d-%d of %d)", start+1, end, len(m.filtered))
		scrollStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
		b.WriteString(scrollStyle.Render(scrollInfo))
	}

	// Show "no results" if filtered list is empty
	if len(m.filtered) == 0 {
		noResultsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
		b.WriteString("\n" + noResultsStyle.Render("No matching sessions"))
	}

	// Help text
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
	b.WriteString("\n" + helpStyle.Render("↑/↓: navigate • enter: details • tab: filter status • ctrl+y: copy ID • ctrl+o: open dir • esc: quit"))

	return b.String()
}

func (m browseModel) viewDetails() string {
	if m.selectedSession == nil {
		return "No session selected"
	}

	s := m.selectedSession
	var b strings.Builder

	// Title
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ecdc4"))
	b.WriteString(titleStyle.Render("Session Details"))
	b.WriteString("\n\n")

	// Helper function to add a field
	addField := func(label, value string, color ...string) {
		labelStyle := lipgloss.NewStyle().Bold(true).Width(20)
		valueStyle := lipgloss.NewStyle()
		if len(color) > 0 {
			valueStyle = valueStyle.Foreground(lipgloss.Color(color[0]))
		}
		b.WriteString(labelStyle.Render(label+":") + " " + valueStyle.Render(value) + "\n")
	}

	// Basic info
	addField("Session ID", s.ID)
	addField("Status", s.Status, getStatusColor(s.Status))
	addField("Repository", s.Repo)
	addField("Branch", s.Branch)
	addField("User", s.User)
	addField("PID", fmt.Sprintf("%d", s.PID))
	addField("Working Directory", s.WorkingDirectory)

	// Timing info
	addField("Started", s.StartedAt.Format("2006-01-02 15:04:05 MST"))
	if s.EndedAt != nil {
		addField("Ended", s.EndedAt.Format("2006-01-02 15:04:05 MST"))
		addField("Duration", s.EndedAt.Sub(s.StartedAt).Round(time.Second).String())
	} else {
		addField("Duration", time.Since(s.StartedAt).Round(time.Second).String()+" (ongoing)")
	}
	addField("Last Activity", s.LastActivity.Format("2006-01-02 15:04:05 MST"))

	// Tmux info
	if s.TmuxKey != "" {
		addField("Tmux Key", s.TmuxKey)
	}

	// Tool statistics
	if s.ToolStats != nil {
		b.WriteString("\n")
		b.WriteString(titleStyle.Render("Tool Statistics"))
		b.WriteString("\n")
		addField("Total Calls", fmt.Sprintf("%d", s.ToolStats.TotalCalls))
		addField("Bash Commands", fmt.Sprintf("%d", s.ToolStats.BashCommands))
		addField("File Modifications", fmt.Sprintf("%d", s.ToolStats.FileModifications))
		addField("File Reads", fmt.Sprintf("%d", s.ToolStats.FileReads))
		addField("Search Operations", fmt.Sprintf("%d", s.ToolStats.SearchOperations))
		if s.ToolStats.TotalCalls > 0 {
			addField("Avg Tool Duration", fmt.Sprintf("%.0fms", s.ToolStats.AverageToolDuration))
		}
	}

	// Session summary
	if s.SessionSummary != nil {
		b.WriteString("\n")
		b.WriteString(titleStyle.Render("Session Summary"))
		b.WriteString("\n")
		addField("Total Tools Used", fmt.Sprintf("%d", s.SessionSummary.TotalTools))
		addField("Files Modified", fmt.Sprintf("%d", s.SessionSummary.FilesModified))
		addField("Commands Executed", fmt.Sprintf("%d", s.SessionSummary.CommandsExecuted))
		addField("Errors Count", fmt.Sprintf("%d", s.SessionSummary.ErrorsCount))
		addField("Notifications", fmt.Sprintf("%d", s.SessionSummary.NotificationsSent))

		// AI Summary if available
		if s.SessionSummary.AISummary != nil && s.SessionSummary.AISummary.CurrentActivity != "" {
			b.WriteString("\n")
			b.WriteString(titleStyle.Render("AI Summary"))
			b.WriteString("\n")
			addField("Current Activity", s.SessionSummary.AISummary.CurrentActivity)
			if len(s.SessionSummary.AISummary.History) > 0 {
				b.WriteString("\nKey Accomplishments:\n")
				for i, milestone := range s.SessionSummary.AISummary.History {
					b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, milestone.Summary))
				}
			}
		}
	}

	// Help
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
	b.WriteString("\n" + helpStyle.Render("enter/esc: back to list"))

	return b.String()
}

func getStatusColor(status string) string {
	switch status {
	case "running":
		return "#00ff00"
	case "idle":
		return "#ffaa00"
	case "completed":
		return "#4ecdc4"
	case "failed", "error":
		return "#ff4444"
	default:
		return "#808080"
	}
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