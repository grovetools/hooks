package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-core/tui/components"
	"github.com/mattsolo1/grove-core/tui/components/help"
	gtable "github.com/mattsolo1/grove-core/tui/components/table"
	"github.com/mattsolo1/grove-core/tui/keymap"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/mattsolo1/grove-hooks/internal/storage/interfaces"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func NewBrowseCmd() *cobra.Command {
	var hideCompleted bool

	cmd := &cobra.Command{
		Use:     "browse",
		Aliases: []string{"b"},
		Short:   "Browse workspace projects and their sessions",
		Long:    `Launch an interactive terminal UI to browse all workspace projects and their Claude sessions. Navigate with arrow keys, search by typing, and select projects to view details.`,
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
				liveClaudeSessions = make([]*models.Session, 0)
			}

			// Discover live grove-flow jobs from plan directories
			liveFlowJobs, err := DiscoverLiveFlowJobs()
			if err != nil {
				// Log error but continue
				if os.Getenv("GROVE_DEBUG") != "" {
					fmt.Fprintf(os.Stderr, "Warning: failed to discover live flow jobs: %v\n", err)
				}
				liveFlowJobs = make([]*models.Session, 0)
			}

			// Initialize the workspace discovery service
			logger := logrus.New()
			logger.SetLevel(logrus.WarnLevel) // Keep it quiet for TUI
			discoverySvc := workspace.NewDiscoveryService(logger)

			// Discover all projects, ecosystems, and worktrees
			discoveryResult, err := discoverySvc.DiscoverAll()
			if err != nil {
				return fmt.Errorf("failed to discover projects: %w", err)
			}

			// Transform the result into a flat list for the TUI
			projects := workspace.TransformToProjectInfo(discoveryResult)

			if len(projects) == 0 {
				fmt.Println("No projects found in configured groves.")
				return nil
			}

			// Enrich the projects with session data from the database
			// This is now secondary - we'll override with live data
			enrichOpts := &workspace.EnrichmentOptions{
				FetchClaudeSessions: true,
				FetchGitStatus:      false, // Don't fetch git status upfront for performance
			}
			if err := workspace.EnrichProjects(context.Background(), projects, enrichOpts); err != nil {
				// Non-fatal - just log and continue without enrichment
				logger.Warnf("Failed to enrich projects: %v", err)
			}

			// Override with live session data from filesystem
			// Match live sessions to projects by working directory
			for _, project := range projects {
				// Check Claude sessions
				for _, session := range liveClaudeSessions {
					if strings.Contains(session.WorkingDirectory, project.Path) {
						// Convert models.Session to workspace.ClaudeSessionInfo
						duration := "running"
						if session.EndedAt != nil {
							duration = session.EndedAt.Sub(session.StartedAt).String()
						}
						project.ClaudeSession = &workspace.ClaudeSessionInfo{
							ID:       session.ID,
							PID:      session.PID,
							Status:   session.Status,
							Duration: duration,
						}
						break
					}
				}
				// Check flow jobs
				for _, job := range liveFlowJobs {
					if strings.Contains(job.WorkingDirectory, project.Path) {
						// Add flow job as a session (browse shows the most recent active work)
						if project.ClaudeSession == nil || project.ClaudeSession.Status != "running" {
							duration := "running"
							if job.EndedAt != nil {
								duration = job.EndedAt.Sub(job.StartedAt).String()
							}
							project.ClaudeSession = &workspace.ClaudeSessionInfo{
								ID:       job.ID,
								PID:      job.PID,
								Status:   job.Status,
								Duration: duration,
							}
						}
						break
					}
				}
			}

			// Filter out projects without sessions if hideCompleted is set
			if hideCompleted {
				var filtered []*workspace.ProjectInfo
				for _, p := range projects {
					if p.ClaudeSession != nil && p.ClaudeSession.Status != "completed" && p.ClaudeSession.Status != "failed" {
						filtered = append(filtered, p)
					}
				}
				projects = filtered
			}

			// Sort projects: those with running sessions first, then idle, then others
			sort.Slice(projects, func(i, j int) bool {
				// Define session priority
				iPriority := 4 // No session
				if projects[i].ClaudeSession != nil {
					switch projects[i].ClaudeSession.Status {
					case "running":
						iPriority = 1
					case "idle":
						iPriority = 2
					default:
						iPriority = 3
					}
				}

				jPriority := 4
				if projects[j].ClaudeSession != nil {
					switch projects[j].ClaudeSession.Status {
					case "running":
						jPriority = 1
					case "idle":
						jPriority = 2
					default:
						jPriority = 3
					}
				}

				// Sort by priority
				return iPriority < jPriority
			})

			// Create the interactive model
			m := newBrowseModel(projects, storage)

			// Run the interactive program
			p := tea.NewProgram(m, tea.WithAltScreen())
			finalModel, err := p.Run()
			if err != nil {
				return fmt.Errorf("error running program: %w", err)
			}

			// Check if a project was selected
			if bm, ok := finalModel.(browseModel); ok && bm.selectedProject != nil {
				// Output the selected project details
				proj := bm.selectedProject
				fmt.Printf("\nSelected Project: %s\n", proj.Name)
				fmt.Printf("Path: %s\n", proj.Path)
				if proj.ParentEcosystemPath != "" {
					fmt.Printf("Ecosystem: %s\n", filepath.Base(proj.ParentEcosystemPath))
				}
				if proj.ClaudeSession != nil {
					fmt.Printf("Session Status: %s\n", proj.ClaudeSession.Status)
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

// browseModel is the model for the interactive workspace browser
type browseModel struct {
	projects        []*workspace.ProjectInfo
	filtered        []*workspace.ProjectInfo
	selectedProject *workspace.ProjectInfo
	cursor          int
	filterInput     textinput.Model
	width           int
	height          int
	statusFilter    string // "", "running", "idle", "no_session"
	showDetails     bool
	selectedPaths   map[string]bool // Track multiple selections by path
	storage         interfaces.SessionStorer
	lastRefresh     time.Time
	keys            browseKeyMap
	help            help.Model
}

func newBrowseModel(projects []*workspace.ProjectInfo, storage interfaces.SessionStorer) browseModel {
	// Create text input for filtering
	ti := textinput.New()
	ti.Placeholder = "Type to filter by project name, ecosystem, or path..."
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
		projects:     projects,
		filtered:     projects,
		filterInput:  ti,
		cursor:       0,
		statusFilter: "",
		showDetails:  false,
		selectedPaths: make(map[string]bool),
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
		// Refresh project data every 5 seconds (less frequent than before to reduce load)
		if !m.showDetails && time.Since(m.lastRefresh) >= 5*time.Second {
			// Re-enrich projects with updated session data
			enrichOpts := &workspace.EnrichmentOptions{
				FetchClaudeSessions: true,
				FetchGitStatus:      false,
			}
			// Remember the currently selected project path
			var selectedPath string
			if m.cursor >= 0 && m.cursor < len(m.filtered) {
				selectedPath = m.filtered[m.cursor].Path
			}

			// Enrich in place
			_ = workspace.EnrichProjects(context.Background(), m.projects, enrichOpts)

			// Re-sort projects
			sort.Slice(m.projects, func(i, j int) bool {
				iPriority := 4
				if m.projects[i].ClaudeSession != nil {
					switch m.projects[i].ClaudeSession.Status {
					case "running":
						iPriority = 1
					case "idle":
						iPriority = 2
					default:
						iPriority = 3
					}
				}

				jPriority := 4
				if m.projects[j].ClaudeSession != nil {
					switch m.projects[j].ClaudeSession.Status {
					case "running":
						jPriority = 1
					case "idle":
						jPriority = 2
					default:
						jPriority = 3
					}
				}

				return iPriority < jPriority
			})

			// Update filtered list
			m.updateFiltered()

			// Try to maintain cursor position on the same project
			if selectedPath != "" {
				for i, p := range m.filtered {
					if p.Path == selectedPath {
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
					m.selectedProject = m.filtered[m.cursor]
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
				m.statusFilter = "no_session"
			case "no_session":
				m.statusFilter = ""
			}
			m.updateFiltered()
			m.cursor = 0

		} else if key.Matches(msg, m.keys.CopyID) {
			// Copy project path to clipboard
			if m.cursor < len(m.filtered) {
				project := m.filtered[m.cursor]
				copyToClipboard(project.Path)
			}

		} else if key.Matches(msg, m.keys.OpenDir) {
			// Open project directory in file manager
			if m.cursor < len(m.filtered) {
				project := m.filtered[m.cursor]
				if project.Path != "" {
					openInFileManager(project.Path)
				}
			}

		} else if key.Matches(msg, m.keys.ExportJSON) {
			// Export selected project as JSON
			if m.cursor < len(m.filtered) {
				project := m.filtered[m.cursor]
				data, _ := json.MarshalIndent(project, "", "  ")
				filename := fmt.Sprintf("project_%s.json", project.Name)
				os.WriteFile(filename, data, 0644)
			}

		} else if key.Matches(msg, m.keys.Select) {
			// Toggle selection on current project when not in details view
			if m.cursor < len(m.filtered) && !m.showDetails {
				project := m.filtered[m.cursor]
				if m.selectedPaths[project.Path] {
					delete(m.selectedPaths, project.Path)
				} else {
					m.selectedPaths[project.Path] = true
				}
				// Don't open details view when using space for selection
				return m, nil
			}

		} else if key.Matches(msg, m.keys.SelectAll) {
			// Select/Deselect all filtered items
			if len(m.filtered) > 0 && !m.showDetails {
				// Check if all filtered items are already selected
				allSelected := true
				for _, p := range m.filtered {
					if !m.selectedPaths[p.Path] {
						allSelected = false
						break
					}
				}

				if allSelected {
					// If all are selected, deselect all filtered items
					for _, p := range m.filtered {
						delete(m.selectedPaths, p.Path)
					}
				} else {
					// Otherwise, select all filtered items
					for _, p := range m.filtered {
						m.selectedPaths[p.Path] = true
					}
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

func (m *browseModel) updateFiltered() {
	filter := strings.ToLower(m.filterInput.Value())
	m.filtered = []*workspace.ProjectInfo{}

	for _, p := range m.projects {
		// Apply status filter first
		if m.statusFilter != "" {
			sessionStatus := ""
			if p.ClaudeSession != nil {
				sessionStatus = p.ClaudeSession.Status
			}

			if m.statusFilter == "no_session" {
				if p.ClaudeSession != nil {
					continue
				}
			} else if sessionStatus != m.statusFilter {
				continue
			}
		}

		// Apply text filter if present
		if filter != "" {
			// Build search text from project info
			ecosystemName := ""
			if p.ParentEcosystemPath != "" {
				ecosystemName = filepath.Base(p.ParentEcosystemPath)
			}
			searchText := strings.ToLower(fmt.Sprintf("%s %s %s %s",
				p.Name, p.Path, ecosystemName, p.WorktreeName))
			if !strings.Contains(searchText, filter) {
				continue
			}
		}

		m.filtered = append(m.filtered, p)
	}
}

func (m browseModel) View() string {
	// Show help if toggled
	if m.help.ShowAll {
		return m.help.View()
	}

	if m.showDetails && m.selectedProject != nil {
		return m.viewDetails()
	}

	t := theme.DefaultTheme
	var b strings.Builder

	// Compact header with filter input on same line
	filterText := "all"
	if m.statusFilter != "" {
		filterText = m.statusFilter
	}

	headerLine := t.Header.Render("Grove Workspace Dashboard") + "  " +
		t.Muted.Render(m.filterInput.View()) + "  " +
		t.Muted.Render("filter:") + t.Info.Render(filterText) + "  " +
		t.Success.Render("●")

	b.WriteString(headerLine)
	b.WriteString("\n")

	// Build table data
	headers := []string{"", "TYPE", "PROJECT", "ECOSYSTEM", "SESSION"}
	var rows [][]string

	for i, proj := range m.filtered {
		// Determine project type
		projType := "project"
		if proj.IsEcosystem && !proj.IsWorktree {
			projType = "ecosystem"
		} else if proj.IsWorktree {
			projType = "worktree"
		}

		// Format project name (with hierarchy for worktrees)
		projectName := proj.Name
		if proj.IsWorktree && proj.ParentPath != "" {
			// Show as "parent-name / worktree-name"
			parentName := filepath.Base(proj.ParentPath)
			projectName = fmt.Sprintf("%s / %s", parentName, proj.Name)
		}

		// Format ecosystem column
		ecosystem := "-"
		if proj.ParentEcosystemPath != "" && !proj.IsEcosystem {
			ecosystem = filepath.Base(proj.ParentEcosystemPath)
		}

		// Format session status
		sessionStatus := "-"
		if proj.ClaudeSession != nil {
			statusStyle := getStatusStyle(proj.ClaudeSession.Status)
			sessionStatus = statusStyle.Render(fmt.Sprintf("%s (%s)", proj.ClaudeSession.Status, proj.ClaudeSession.Duration))
		}

		// Selection and cursor indicator
		var indicator string
		isSelected := m.selectedPaths[proj.Path]
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
			projType,
			truncateStr(projectName, 50),
			truncateStr(ecosystem, 30),
			sessionStatus,
		})
	}

	// Render table using SelectableTable
	if len(m.filtered) > 0 {
		tableStr := gtable.SelectableTable(headers, rows, m.cursor)
		b.WriteString(tableStr)
	} else {
		b.WriteString("\n" + t.Muted.Render("No matching projects"))
	}

	// Selection count and help on same line
	b.WriteString("\n")
	if len(m.selectedPaths) > 0 {
		b.WriteString(t.Highlight.Render(fmt.Sprintf("[%d selected]", len(m.selectedPaths))) + " ")
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
	if m.selectedProject == nil {
		return "No project selected"
	}

	p := m.selectedProject
	t := theme.DefaultTheme
	var content strings.Builder

	// Basic project info
	content.WriteString(components.RenderKeyValue("Project Name", p.Name))
	content.WriteString("\n")
	content.WriteString(components.RenderKeyValue("Path", p.Path))
	content.WriteString("\n")

	// Project type
	projType := "Project"
	if p.IsEcosystem && !p.IsWorktree {
		projType = "Ecosystem"
	} else if p.IsWorktree {
		projType = "Worktree"
	}
	content.WriteString(components.RenderKeyValue("Type", projType))
	content.WriteString("\n")

	// Hierarchy info
	if p.IsWorktree && p.ParentPath != "" {
		content.WriteString(components.RenderKeyValue("Parent Project", filepath.Base(p.ParentPath)))
		content.WriteString("\n")
		content.WriteString(components.RenderKeyValue("Parent Path", p.ParentPath))
		content.WriteString("\n")
	}

	if p.ParentEcosystemPath != "" && !p.IsEcosystem {
		ecosystemName := filepath.Base(p.ParentEcosystemPath)
		content.WriteString(components.RenderKeyValue("Parent Ecosystem", ecosystemName))
		content.WriteString("\n")
		content.WriteString(components.RenderKeyValue("Ecosystem Path", p.ParentEcosystemPath))
		content.WriteString("\n")
	}

	if p.WorktreeName != "" {
		content.WriteString(components.RenderKeyValue("Worktree Name", p.WorktreeName))
		content.WriteString("\n")
	}

	// Claude session info
	if p.ClaudeSession != nil {
		content.WriteString("\n")
		var sessionContent strings.Builder

		sessionContent.WriteString(components.RenderKeyValue("Session ID", p.ClaudeSession.ID))
		sessionContent.WriteString("\n")

		statusStyle := getStatusStyle(p.ClaudeSession.Status)
		sessionContent.WriteString(components.RenderKeyValue("Status", statusStyle.Render(p.ClaudeSession.Status)))
		sessionContent.WriteString("\n")

		sessionContent.WriteString(components.RenderKeyValue("PID", fmt.Sprintf("%d", p.ClaudeSession.PID)))
		sessionContent.WriteString("\n")

		sessionContent.WriteString(components.RenderKeyValue("Duration", p.ClaudeSession.Duration))
		sessionContent.WriteString("\n")

		content.WriteString(components.RenderSection("Active Claude Session", sessionContent.String()))
		content.WriteString("\n")
	} else {
		content.WriteString("\n")
		content.WriteString(components.RenderKeyValue("Claude Session", t.Muted.Render("No active session")))
		content.WriteString("\n")
	}

	// Help
	content.WriteString("\n")
	content.WriteString(t.Muted.Render("enter/esc: back to list"))

	// Wrap in a box
	return components.RenderBox("Project Details", content.String(), m.width)
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

