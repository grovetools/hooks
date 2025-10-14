package browse

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-core/tui/components/help"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/mattsolo1/grove-hooks/commands"
	"github.com/mattsolo1/grove-hooks/internal/storage/interfaces"
)

type viewMode int

const (
	tableView viewMode = iota
	treeView
)

// displayNode represents a single line in the TUI, which can be a workspace or a session.
type displayNode struct {
	isSession bool
	workspace *workspace.WorkspaceNode
	session   *models.Session

	// Pre-calculated for rendering
	lineText string
	depth    int
}

// Model is the model for the interactive session browser
type Model struct {
	sessions         []*models.Session
	previousSessions []*models.Session // Track previous state for notification dispatch
	workspaces       []*workspace.WorkspaceNode
	workspaceProvider *workspace.Provider

	filteredSessions []*models.Session
	displayNodes     []*displayNode

	selectedSession  *models.Session
	cursor           int
	scrollOffset     int // For viewport scrolling
	filterInput      textinput.Model
	width            int
	height           int
	showDetails      bool
	selectedIDs      map[string]bool // Track multiple selections by ID
	storage          interfaces.SessionStorer
	lastRefresh      time.Time
	keys             KeyMap
	help             help.Model
	statusMessage    string // For showing kill/error messages
	hideCompleted    bool   // Store the initial --active flag
	showFilterView   bool   // Toggle for filter options view
	filterCursor     int    // Cursor position in filter view
	statusFilters    map[string]bool
	typeFilters      map[string]bool
	searchActive     bool // Whether search input is active
	viewMode         viewMode
}

func NewModel(sessions []*models.Session, workspaces []*workspace.WorkspaceNode, storage interfaces.SessionStorer, hideCompleted bool, filterPrefs commands.BrowseFilterPreferences) Model {
	ti := textinput.New()
	ti.Placeholder = "Type to filter by session ID, repo, branch, or working directory..."
	ti.CharLimit = 256
	ti.Width = 60

	t := theme.DefaultTheme
	ti.PromptStyle = t.Muted
	ti.Cursor.Style = t.Cursor
	ti.TextStyle = t.Input

	keys := NewKeyMap()

	model := Model{
		sessions:         sessions,
		workspaces:       workspaces,
		filteredSessions: sessions,
		filterInput:      ti,
		selectedIDs:      make(map[string]bool),
		storage:          storage,
		lastRefresh:      time.Now(),
		keys:             keys,
		help:             help.New(keys),
		hideCompleted:    hideCompleted,
		statusFilters:    filterPrefs.StatusFilters,
		typeFilters:      filterPrefs.TypeFilters,
		viewMode:         tableView,
	}

	// Build a proper provider if we have workspaces
	if len(workspaces) > 0 {
		// Create provider using nodes directly
		// We'll build a minimal DiscoveryResult to satisfy the NewProvider function
		var projects []workspace.Project
		seen := make(map[string]bool)
		for _, node := range workspaces {
			// Only add root projects (not worktrees)
			if !seen[node.Path] && node.Kind != workspace.StandaloneProjectWorktree &&
			   node.Kind != workspace.EcosystemWorktree &&
			   node.Kind != workspace.EcosystemSubProjectWorktree &&
			   node.Kind != workspace.EcosystemWorktreeSubProjectWorktree {
				projects = append(projects, workspace.Project{Name: node.Name, Path: node.Path})
				seen[node.Path] = true
			}
		}
		result := &workspace.DiscoveryResult{Projects: projects}
		model.workspaceProvider = workspace.NewProvider(result)
	} else {
		// Create empty provider
		model.workspaceProvider = workspace.NewProvider(&workspace.DiscoveryResult{})
	}

	model.updateFilteredAndDisplayNodes()

	return model
}

type tickMsg time.Time

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		tea.Tick(1*time.Second, func(t time.Time) tea.Msg {
			return tickMsg(t)
		}),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tickMsg:
		// Preserve cursor position
		var selectedID string
		if m.viewMode == tableView && m.cursor >= 0 && m.cursor < len(m.filteredSessions) {
			selectedID = m.filteredSessions[m.cursor].ID
		} else if m.viewMode == treeView && m.cursor >= 0 && m.cursor < len(m.displayNodes) {
			if m.displayNodes[m.cursor].isSession {
				selectedID = m.displayNodes[m.cursor].session.ID
			}
		}

		newSessions, err := commands.GetAllSessions(m.storage, m.hideCompleted)
		if err != nil {
			m.statusMessage = fmt.Sprintf("Error refreshing: %v", err)
		} else {
			if len(m.previousSessions) > 0 {
				go commands.DispatchStateChangeNotifications(m.previousSessions, newSessions)
			}
			m.previousSessions = m.sessions
			m.sessions = newSessions
			m.updateFilteredAndDisplayNodes()
		}

		if selectedID != "" {
			newCursor := -1
			if m.viewMode == tableView {
				for i, s := range m.filteredSessions {
					if s.ID == selectedID {
						newCursor = i
						break
					}
				}
			} else if m.viewMode == treeView {
				for i, n := range m.displayNodes {
					if n.isSession && n.session.ID == selectedID {
						newCursor = i
						break
					}
				}
			}
			if newCursor != -1 {
				m.cursor = newCursor
			} else {
				// ID disappeared, reset cursor if out of bounds
				var listLen int
				if m.viewMode == tableView {
					listLen = len(m.filteredSessions)
				} else {
					listLen = len(m.displayNodes)
				}
				if m.cursor >= listLen {
					if listLen > 0 {
						m.cursor = listLen - 1
					} else {
						m.cursor = 0
					}
				}
			}
		}

		return m, tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
			return tickMsg(t)
		})

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.SetSize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyMsg:
		if key.Matches(msg, m.keys.Help) {
			m.help.Toggle()
			return m, nil
		}
		if key.Matches(msg, m.keys.Quit) {
			if m.help.ShowAll {
				m.help.Toggle()
				return m, nil
			}
			if m.showFilterView {
				m.showFilterView = false
				return m, nil
			}
			if m.showDetails {
				m.showDetails = false
				return m, nil
			}
			return m, tea.Quit
		}
		if key.Matches(msg, m.keys.Back) {
			if m.help.ShowAll {
				m.help.Toggle()
				return m, nil
			}
			if m.searchActive {
				m.searchActive = false
				m.filterInput.Blur()
				return m, nil
			}
			if m.showFilterView {
				m.showFilterView = false
				return m, nil
			}
			if m.showDetails {
				m.showDetails = false
				return m, nil
			}
			return m, tea.Quit
		}

		if m.help.ShowAll {
			return m, nil
		}

		if m.showFilterView {
			return m.updateFilterView(msg)
		}

		if key.Matches(msg, m.keys.SearchFilter) && !m.showDetails && !m.searchActive {
			m.searchActive = true
			m.filterInput.Focus()
			return m, textinput.Blink
		}
		if key.Matches(msg, m.keys.ToggleFilter) && !m.showDetails && !m.searchActive {
			m.showFilterView = !m.showFilterView
			return m, nil
		}

		if key.Matches(msg, m.keys.ToggleView) {
			if m.viewMode == tableView {
				m.viewMode = treeView
			} else {
				m.viewMode = tableView
			}
			m.cursor = 0
			m.scrollOffset = 0
			return m, nil
		}

		listLen := 0
		if m.viewMode == tableView {
			listLen = len(m.filteredSessions)
		} else {
			listLen = len(m.displayNodes)
		}

		if key.Matches(msg, m.keys.Up) {
			if !m.showDetails && m.cursor > 0 {
				m.cursor--
				if m.cursor < m.scrollOffset {
					m.scrollOffset = m.cursor
				}
			}
		} else if key.Matches(msg, m.keys.Down) {
			if !m.showDetails && m.cursor < listLen-1 {
				m.cursor++
				viewportHeight := m.getViewportHeight()
				if m.cursor >= m.scrollOffset+viewportHeight {
					m.scrollOffset = m.cursor - viewportHeight + 1
				}
			}
		} else if key.Matches(msg, m.keys.ScrollDown) {
			if !m.showDetails {
				viewportHeight := m.getViewportHeight()
				m.scrollOffset += viewportHeight / 2
				maxScroll := listLen - viewportHeight
				if maxScroll < 0 {
					maxScroll = 0
				}
				if m.scrollOffset > maxScroll {
					m.scrollOffset = maxScroll
				}
				if m.cursor < m.scrollOffset {
					m.cursor = m.scrollOffset
				}
			}
		} else if key.Matches(msg, m.keys.ScrollUp) {
			if !m.showDetails {
				viewportHeight := m.getViewportHeight()
				m.scrollOffset -= viewportHeight / 2
				if m.scrollOffset < 0 {
					m.scrollOffset = 0
				}
				if m.cursor >= m.scrollOffset+viewportHeight {
					m.cursor = m.scrollOffset + viewportHeight - 1
					if m.cursor >= listLen {
						m.cursor = listLen - 1
					}
				}
			}
		} else if key.Matches(msg, m.keys.Confirm) {
			var currentSession *models.Session
			if m.viewMode == tableView && m.cursor < len(m.filteredSessions) {
				currentSession = m.filteredSessions[m.cursor]
			} else if m.viewMode == treeView && m.cursor < len(m.displayNodes) {
				if m.displayNodes[m.cursor].isSession {
					currentSession = m.displayNodes[m.cursor].session
				}
			}
			if currentSession != nil {
				if m.showDetails {
					return m, tea.Quit
				} else {
					m.selectedSession = currentSession
					m.showDetails = true
				}
			}
		} else if key.Matches(msg, m.keys.CopyID) {
			if session := m.getCurrentSession(); session != nil {
				commands.CopyToClipboard(session.ID)
			}
		} else if key.Matches(msg, m.keys.OpenDir) {
			var pathToOpen string
			if m.viewMode == tableView {
				if session := m.getCurrentSession(); session != nil {
					pathToOpen = session.WorkingDirectory
				}
			} else if m.viewMode == treeView && m.cursor < len(m.displayNodes) {
				node := m.displayNodes[m.cursor]
				if node.isSession {
					pathToOpen = node.session.WorkingDirectory
				} else {
					pathToOpen = node.workspace.Path
				}
			}
			if pathToOpen != "" {
				commands.OpenInFileManager(pathToOpen)
			}
		} else if key.Matches(msg, m.keys.ExportJSON) {
			if session := m.getCurrentSession(); session != nil {
				commands.ExportSessionToJSON(session)
			}
		} else if key.Matches(msg, m.keys.Select) {
			if session := m.getCurrentSession(); session != nil && !m.showDetails {
				if m.selectedIDs[session.ID] {
					delete(m.selectedIDs, session.ID)
				} else {
					m.selectedIDs[session.ID] = true
				}
				return m, nil
			}
		} else if key.Matches(msg, m.keys.SelectAll) {
			// Select/Deselect all filtered items
			if len(m.filteredSessions) > 0 && !m.showDetails {
				allSelected := true
				for _, s := range m.filteredSessions {
					if !m.selectedIDs[s.ID] {
						allSelected = false
						break
					}
				}
				if allSelected {
					for _, s := range m.filteredSessions {
						delete(m.selectedIDs, s.ID)
					}
				} else {
					for _, s := range m.filteredSessions {
						m.selectedIDs[s.ID] = true
					}
				}
			}
		} else if key.Matches(msg, m.keys.Kill) {
			if session := m.getCurrentSession(); session != nil && !m.showDetails {
				if session.Type == "" || session.Type == "claude_session" {
					groveSessionsDir := commands.ExpandPath("~/.grove/hooks/sessions")
					sessionDir := filepath.Join(groveSessionsDir, session.ID)
					pidFile := filepath.Join(sessionDir, "pid.lock")
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
					if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
						m.statusMessage = fmt.Sprintf("Error: failed to kill PID %d: %v", pid, err)
						return m, nil
					}
					os.RemoveAll(sessionDir)
					m.statusMessage = fmt.Sprintf("Killed session %s (PID %d)", session.ID[:8], pid)
				} else {
					m.statusMessage = "Error: Can only kill Claude sessions, not flow jobs"
				}
			}
		} else if m.searchActive && !m.showDetails {
			prevValue := m.filterInput.Value()
			m.filterInput, cmd = m.filterInput.Update(msg)
			if m.filterInput.Value() != prevValue {
				m.updateFilteredAndDisplayNodes()
				m.cursor = 0
			}
			return m, cmd
		}
	}
	return m, nil
}

func (m *Model) updateFilteredAndDisplayNodes() {
	filter := strings.ToLower(m.filterInput.Value())
	m.filteredSessions = []*models.Session{}

	for _, s := range m.sessions {
		if !m.statusFilters[s.Status] {
			continue
		}
		sessionType := s.Type
		if sessionType == "" || sessionType == "claude_session" {
			sessionType = "claude_code"
		}
		if !m.typeFilters[sessionType] {
			continue
		}
		if filter != "" {
			searchText := strings.ToLower(fmt.Sprintf("%s %s %s %s %s %s %s",
				s.ID, s.Repo, s.Branch, s.WorkingDirectory, s.User, sessionType, s.PlanName))
			if !strings.Contains(searchText, filter) {
				continue
			}
		}
		m.filteredSessions = append(m.filteredSessions, s)
	}

	m.buildDisplayTree()
}

func (m *Model) buildDisplayTree() {
	var nodes []*displayNode
	filterText := strings.ToLower(m.filterInput.Value())

	workspaceSessionMap := make(map[string][]*models.Session)
	for _, session := range m.filteredSessions {
		// Find parent workspace for session
		parentWorkspace := m.workspaceProvider.FindByPath(session.WorkingDirectory)
		if parentWorkspace != nil {
			workspaceSessionMap[parentWorkspace.Path] = append(workspaceSessionMap[parentWorkspace.Path], session)
		}
	}

	// Determine which workspaces to show
	workspacesToShow := make(map[string]bool)
	if filterText != "" {
		for _, ws := range m.workspaces {
			if strings.Contains(strings.ToLower(ws.Name), filterText) || strings.Contains(strings.ToLower(ws.Path), filterText) {
				workspacesToShow[ws.Path] = true
				// Add all parents
				m.addParentWorkspaces(ws, workspacesToShow)
			}
		}
	}

	// Show workspaces that have sessions
	for wsPath := range workspaceSessionMap {
		workspacesToShow[wsPath] = true
		// Add all parents
		for _, ws := range m.workspaces {
			if ws.Path == wsPath {
				m.addParentWorkspaces(ws, workspacesToShow)
				break
			}
		}
	}

	// Build the final list
	for _, ws := range m.workspaces {
		if workspacesToShow[ws.Path] || filterText == "" {
			nodes = append(nodes, &displayNode{isSession: false, workspace: ws})
			if sessions, ok := workspaceSessionMap[ws.Path]; ok {
				for _, s := range sessions {
					nodes = append(nodes, &displayNode{isSession: true, session: s, workspace: ws})
				}
			}
		}
	}
	m.displayNodes = nodes
}

func (m *Model) addParentWorkspaces(ws *workspace.WorkspaceNode, workspacesToShow map[string]bool) {
	// Add parent project path if it exists
	if ws.ParentProjectPath != "" {
		workspacesToShow[ws.ParentProjectPath] = true
		for _, parentWs := range m.workspaces {
			if parentWs.Path == ws.ParentProjectPath {
				m.addParentWorkspaces(parentWs, workspacesToShow)
				break
			}
		}
	}
	// Add parent ecosystem path if it exists
	if ws.ParentEcosystemPath != "" {
		workspacesToShow[ws.ParentEcosystemPath] = true
		for _, parentWs := range m.workspaces {
			if parentWs.Path == ws.ParentEcosystemPath {
				m.addParentWorkspaces(parentWs, workspacesToShow)
				break
			}
		}
	}
}

func (m *Model) getViewportHeight() int {
	const headerLines = 3
	const footerLines = 2
	availableHeight := m.height - headerLines - footerLines
	if availableHeight < 1 {
		return 1
	}
	return availableHeight
}

func (m *Model) getCurrentSession() *models.Session {
	if m.viewMode == tableView {
		if m.cursor >= 0 && m.cursor < len(m.filteredSessions) {
			return m.filteredSessions[m.cursor]
		}
	} else if m.viewMode == treeView {
		if m.cursor >= 0 && m.cursor < len(m.displayNodes) {
			node := m.displayNodes[m.cursor]
			if node.isSession {
				return node.session
			}
		}
	}
	return nil
}

func (m Model) updateFilterView(msg tea.Msg) (tea.Model, tea.Cmd) {
	statusOptions := []string{"running", "idle", "pending_user", "completed", "interrupted", "failed", "error", "hold", "todo", "abandoned"}
	typeOptions := []string{"claude_code", "chat", "interactive_agent", "oneshot", "headless_agent", "agent", "shell"}
	totalOptions := len(statusOptions) + len(typeOptions)

	if key.Matches(msg, m.keys.Up) {
		if m.filterCursor > 0 {
			m.filterCursor--
		}
	} else if key.Matches(msg, m.keys.Down) {
		if m.filterCursor < totalOptions-1 {
			m.filterCursor++
		}
	} else if key.Matches(msg, m.keys.Select) || msg.(tea.KeyMsg).String() == " " {
		if m.filterCursor < len(statusOptions) {
			status := statusOptions[m.filterCursor]
			m.statusFilters[status] = !m.statusFilters[status]
		} else {
			typeIdx := m.filterCursor - len(statusOptions)
			typ := typeOptions[typeIdx]
			m.typeFilters[typ] = !m.typeFilters[typ]
		}
		prefs := commands.BrowseFilterPreferences{
			StatusFilters: m.statusFilters,
			TypeFilters:   m.typeFilters,
		}
		commands.SaveFilterPreferences(prefs)
		m.updateFilteredAndDisplayNodes()
		m.cursor = 0
		m.scrollOffset = 0
	}
	return m, nil
}
