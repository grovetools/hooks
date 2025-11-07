package browse

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattsolo1/grove-core/pkg/models"
	"github.com/mattsolo1/grove-core/pkg/tmux"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-core/tui/components/help"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/mattsolo1/grove-hooks/internal/storage/interfaces"
	"github.com/mattsolo1/grove-hooks/internal/utils"
)

// FilterPreferences stores the user's filter preferences
type FilterPreferences struct {
	StatusFilters map[string]bool `json:"status_filters"`
	TypeFilters   map[string]bool `json:"type_filters"`
}

// GetAllSessionsFunc is the function type for getting all sessions
type GetAllSessionsFunc func(storage interfaces.SessionStorer, hideCompleted bool) ([]*models.Session, error)

// DispatchNotificationsFunc is the function type for dispatching state change notifications
type DispatchNotificationsFunc func(oldSessions, newSessions []*models.Session)

// SaveFilterPreferencesFunc is the function type for saving filter preferences
type SaveFilterPreferencesFunc func(prefs FilterPreferences) error

type viewMode int

const (
	tableView viewMode = iota
	treeView
)

// PlanListItem holds aggregated data for a plan group
type PlanListItem struct {
	Name        string
	JobCount    int
	Status      string
	StatusParts map[string]int
	LastUpdated time.Time
}

// displayNode represents a single line in the TUI, which can be a workspace, a plan, or a session.
type displayNode struct {
	isSession bool
	isPlan    bool
	workspace *workspace.WorkspaceNode
	session   *models.Session
	plan      *PlanListItem

	// Pre-calculated for rendering
	prefix          string // Tree structure prefix (e.g., "│   ├─ ")
	depth           int
	workspaceStatus string // Aggregated status for workspace nodes (e.g., "running" if any session is active)
}

// Model is the model for the interactive session browser
type Model struct {
	sessions          []*models.Session
	previousSessions  []*models.Session // Track previous state for notification dispatch
	workspaces        []*workspace.WorkspaceNode
	workspaceProvider *workspace.Provider

	filteredSessions []*models.Session
	displayNodes     []*displayNode

	selectedSession *models.Session
	cursor          int
	scrollOffset    int // For viewport scrolling
	filterInput     textinput.Model
	width           int
	height          int
	showDetails     bool
	selectedIDs     map[string]bool // Track multiple selections by ID
	storage         interfaces.SessionStorer
	lastRefresh     time.Time
	keys            KeyMap
	help            help.Model
	statusMessage   string // For showing kill/error messages
	hideCompleted   bool   // Store the initial --active flag
	showFilterView  bool   // Toggle for filter options view
	filterCursor    int    // Cursor position in filter view
	statusFilters   map[string]bool
	typeFilters     map[string]bool
	searchActive    bool // Whether search input is active
	viewMode        viewMode
	gPressed        bool // Track first 'g' press for 'gg' chord

	// Exit actions
	CommandOnExit *exec.Cmd
	MessageOnExit string

	// Function dependencies (to avoid import cycles)
	getAllSessions        GetAllSessionsFunc
	dispatchNotifications DispatchNotificationsFunc
	saveFilterPreferences SaveFilterPreferencesFunc
}

func NewModel(
	sessions []*models.Session,
	workspaces []*workspace.WorkspaceNode,
	storage interfaces.SessionStorer,
	hideCompleted bool,
	filterPrefs FilterPreferences,
	getAllSessions GetAllSessionsFunc,
	dispatchNotifications DispatchNotificationsFunc,
	saveFilterPreferences SaveFilterPreferencesFunc,
) Model {
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
		sessions:              sessions,
		workspaces:            workspaces,
		filteredSessions:      sessions,
		filterInput:           ti,
		selectedIDs:           make(map[string]bool),
		storage:               storage,
		lastRefresh:           time.Now(),
		keys:                  keys,
		help:                  help.New(keys),
		hideCompleted:         hideCompleted,
		statusFilters:         filterPrefs.StatusFilters,
		typeFilters:           filterPrefs.TypeFilters,
		viewMode:              tableView,
		getAllSessions:        getAllSessions,
		dispatchNotifications: dispatchNotifications,
		saveFilterPreferences: saveFilterPreferences,
	}

	// Build a proper provider if we have workspaces
	if len(workspaces) > 0 {
		// Create provider using nodes directly
		// We'll build a minimal DiscoveryResult to satisfy the NewProvider function
		var projects []workspace.Project
		seen := make(map[string]bool)
		for _, node := range workspaces {
			// Only add root projects (not worktrees)
			// Check if this is a root node (not a worktree)
			if !seen[node.Path] {
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

type gChordTimeoutMsg struct{}

type editFileAndQuitMsg struct{ filePath string }

const gChordTimeout = 400 * time.Millisecond

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
	case gChordTimeoutMsg:
		m.gPressed = false
		return m, nil

	case editFileAndQuitMsg:
		// Print protocol string and quit - Neovim plugin will handle the file opening
		fmt.Printf("EDIT_FILE:%s\n", msg.filePath)
		return m, tea.Quit

	case tickMsg:
		// Preserve cursor position
		var selectedID string
		if m.cursor >= 0 && m.cursor < len(m.displayNodes) {
			if m.displayNodes[m.cursor].isSession {
				selectedID = m.displayNodes[m.cursor].session.ID
			}
		}

		newSessions, err := m.getAllSessions(m.storage, m.hideCompleted)
		if err != nil {
			m.statusMessage = fmt.Sprintf("Error refreshing: %v", err)
		} else {
			if len(m.previousSessions) > 0 {
				go m.dispatchNotifications(m.previousSessions, newSessions)
			}
			m.previousSessions = m.sessions
			m.sessions = newSessions
			m.updateFilteredAndDisplayNodes()
		}

		if selectedID != "" {
			newCursor := -1
			for i, n := range m.displayNodes {
				if n.isSession && n.session.ID == selectedID {
					newCursor = i
					break
				}
			}
			if newCursor != -1 {
				m.cursor = newCursor
			} else {
				// ID disappeared, reset cursor if out of bounds
				listLen := len(m.displayNodes)
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
		// Handle 'gg' chord for go to top
		if m.gPressed {
			m.gPressed = false
			if key.Matches(msg, m.keys.GoToTop) {
				// Second 'g' pressed - go to top
				m.cursor = 0
				m.scrollOffset = 0
				return m, nil
			}
			// Any other key resets the chord state
		} else if key.Matches(msg, m.keys.GoToTop) && !m.showDetails {
			// First 'g' pressed - start chord timer
			m.gPressed = true
			return m, tea.Tick(gChordTimeout, func(t time.Time) tea.Msg {
				return gChordTimeoutMsg{}
			})
		}

		// Handle 'G' for go to bottom
		if key.Matches(msg, m.keys.GoToBottom) && !m.showDetails {
			listLen := len(m.displayNodes)
			if listLen > 0 {
				m.cursor = listLen - 1
				viewportHeight := m.getViewportHeight()
				m.scrollOffset = listLen - viewportHeight
				if m.scrollOffset < 0 {
					m.scrollOffset = 0
				}
			}
			return m, nil
		}

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

		listLen := len(m.displayNodes)

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
				halfPage := viewportHeight / 2
				// Move cursor down by half a page
				m.cursor += halfPage
				if m.cursor >= listLen {
					m.cursor = listLen - 1
				}
				// Adjust scroll offset if cursor is outside viewport
				if m.cursor >= m.scrollOffset+viewportHeight {
					m.scrollOffset = m.cursor - viewportHeight + 1
				}
			}
		} else if key.Matches(msg, m.keys.ScrollUp) {
			if !m.showDetails {
				viewportHeight := m.getViewportHeight()
				halfPage := viewportHeight / 2
				// Move cursor up by half a page
				m.cursor -= halfPage
				if m.cursor < 0 {
					m.cursor = 0
				}
				// Adjust scroll offset if cursor is outside viewport
				if m.cursor < m.scrollOffset {
					m.scrollOffset = m.cursor
				}
			}
		} else if key.Matches(msg, m.keys.Confirm) || key.Matches(msg, m.keys.Open) || key.Matches(msg, m.keys.Edit) {
			if m.showDetails {
				m.showDetails = false
				return m, nil
			}

			node := m.getCurrentDisplayNode()
			if node == nil {
				return m, nil // Nothing selected
			}

			// Handle context-aware actions for workspaces and plans
			if node.isPlan {
				// Action for plan: switch to workspace session and open flow plan TUI in new window
				sessionName := node.workspace.Identifier()
				planName := node.plan.Name
				windowName := fmt.Sprintf("plan-%s", planName)

				if os.Getenv("TMUX") != "" {
					tmuxClient, _ := tmux.NewClient()
					cmd, err := tmuxClient.NewWindowAndClosePopup(context.Background(), sessionName, windowName, fmt.Sprintf("flow plan status -t %s", planName))
					if err != nil {
						m.statusMessage = fmt.Sprintf("Failed: %v", err)
						return m, nil
					}
					m.CommandOnExit = cmd
					return m, tea.Quit
				}
				m.MessageOnExit = fmt.Sprintf("Run: tmux attach -t %s; tmux new-window -n '%s' 'flow plan status -t %s'", sessionName, windowName, planName)
				return m, tea.Quit
			} else if !node.isSession { // It's a workspace
				// Action for workspace: open tmux session
				sessionName := node.workspace.Identifier()
				return m.switchToTmuxSession(sessionName)
			}

			// If it's a session (in either view), fall back to key-specific actions
			if node.isSession {
				session := node.session
				if key.Matches(msg, m.keys.Confirm) { // Enter -> view details
					m.selectedSession = session
					m.showDetails = true
				} else if key.Matches(msg, m.keys.Open) { // o -> open running session
					sessionType := session.Type
					if sessionType == "" || sessionType == "claude_session" {
						sessionType = "claude_code"
					}

					if sessionType == "claude_code" && session.TmuxKey != "" && (session.Status == "running" || session.Status == "idle") {
						if os.Getenv("TMUX") != "" {
							tmuxClient, _ := tmux.NewClient()
							if err := tmuxClient.SwitchClient(context.Background(), session.TmuxKey); err != nil {
								m.statusMessage = fmt.Sprintf("Failed to switch to session: %v", err)
								return m, nil
							}
							m.CommandOnExit = tmuxClient.ClosePopupCmd()
							return m, tea.Quit
						}
						m.MessageOnExit = fmt.Sprintf("Attach to session with:\ntmux attach -t %s", session.TmuxKey)
						return m, tea.Quit
					}

					if session.Type == "interactive_agent" && (session.Status == "running" || session.Status == "idle") {
						workDir := session.WorkingDirectory
						projInfo, err := workspace.GetProjectByPath(workDir)
						if err != nil {
							m.statusMessage = fmt.Sprintf("Error: could not find workspace for %s", workDir)
							return m, nil
						}

						sessionName := projInfo.Identifier()
						windowName := "job-" + session.JobTitle
						// Sanitize window name for tmux
						windowName = strings.Map(func(r rune) rune {
							if r == ':' || r == '.' || r == ' ' {
								return '-'
							}
							return r
						}, windowName)

						if os.Getenv("TMUX") != "" {
							tmuxClient, _ := tmux.NewClient()
							cmd, err := tmuxClient.SelectWindowAndClosePopup(context.Background(), sessionName, windowName)
							if err != nil {
								m.statusMessage = fmt.Sprintf("Failed: %v", err)
								return m, nil
							}
							m.CommandOnExit = cmd
							return m, tea.Quit
						}
						m.MessageOnExit = fmt.Sprintf("Attach to session and window with:\ntmux attach -t %s\ntmux select-window -t :'%s'", sessionName, windowName)
						return m, tea.Quit
					}
				} else if key.Matches(msg, m.keys.Edit) { // e -> edit job file
					if session.JobFilePath != "" {
						if os.Getenv("GROVE_NVIM_PLUGIN") == "true" {
							return m, func() tea.Msg {
								return editFileAndQuitMsg{filePath: session.JobFilePath}
							}
						}

						editor := os.Getenv("EDITOR")
						if editor == "" {
							editor = "vim"
						}
						return m, tea.ExecProcess(exec.Command(editor, session.JobFilePath), func(err error) tea.Msg {
							if err != nil {
								return fmt.Errorf("editor failed: %w", err)
							}
							return tickMsg(time.Now())
						})
					}
				}
			}
			return m, nil
		} else if key.Matches(msg, m.keys.CopyID) {
			if session := m.getCurrentSession(); session != nil {
				utils.CopyToClipboard(session.ID)
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
				utils.OpenInFileManager(pathToOpen)
			}
		} else if key.Matches(msg, m.keys.ExportJSON) {
			if session := m.getCurrentSession(); session != nil {
				utils.ExportSessionToJSON(session)
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
					groveSessionsDir := utils.ExpandPath("~/.grove/hooks/sessions")
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

func createPlanListItem(planName string, sessions []*models.Session) PlanListItem {
	item := PlanListItem{
		Name:        planName,
		JobCount:    len(sessions),
		StatusParts: make(map[string]int),
	}

	var latestUpdate time.Time
	for _, s := range sessions {
		item.StatusParts[s.Status]++
		if s.LastActivity.After(latestUpdate) {
			latestUpdate = s.LastActivity
		}
	}
	item.LastUpdated = latestUpdate

	// Determine a single aggregate status string
	if item.StatusParts["running"] > 0 {
		item.Status = "running"
	} else if item.StatusParts["pending_user"] > 0 {
		item.Status = "pending_user"
	} else if item.StatusParts["idle"] > 0 {
		item.Status = "idle"
	} else if item.StatusParts["failed"] > 0 {
		item.Status = "failed"
	} else if len(sessions) > 0 && item.StatusParts["completed"] == len(sessions) {
		item.Status = "completed"
	} else {
		item.Status = "pending"
	}

	return item
}

func (m *Model) buildDisplayTree() {
	var nodes []*displayNode
	filterText := strings.ToLower(m.filterInput.Value())

	// Group sessions by workspace, then by plan
	workspaceSessionMap := make(map[string]map[string][]*models.Session)
	var unmatchedSessions []*models.Session

	for _, session := range m.filteredSessions {
		var bestMatch *workspace.WorkspaceNode
		bestMatchDepth := -1

		// Normalize paths for case-insensitive comparison (macOS filesystem is case-insensitive)
		sessionWorkDir := strings.ToLower(session.WorkingDirectory)

		for _, ws := range m.workspaces {
			wsPath := strings.ToLower(ws.Path)
			if strings.HasPrefix(sessionWorkDir+"/", wsPath+"/") || sessionWorkDir == wsPath {
				if ws.Depth > bestMatchDepth {
					bestMatch = ws
					bestMatchDepth = ws.Depth
				}
			}
		}

		// For flow plan sessions that didn't match by working directory,
		// try to match based on repo metadata
		if bestMatch == nil && session.JobFilePath != "" {
			parts := strings.Split(session.JobFilePath, "/")
			for i, part := range parts {
				if part == "repos" && i+1 < len(parts) {
					repoName := parts[i+1]
					for _, ws := range m.workspaces {
						if ws.Name == repoName {
							if session.Repo != "" {
								if strings.Contains(ws.Path, session.Repo) {
									if ws.Depth > bestMatchDepth {
										bestMatch = ws
										bestMatchDepth = ws.Depth
									}
								}
							} else {
								if ws.Depth > bestMatchDepth {
									bestMatch = ws
									bestMatchDepth = ws.Depth
								}
							}
						}
					}
					break
				}
			}
		}

		if bestMatch != nil {
			if _, ok := workspaceSessionMap[bestMatch.Path]; !ok {
				workspaceSessionMap[bestMatch.Path] = make(map[string][]*models.Session)
			}
			planName := session.PlanName
			workspaceSessionMap[bestMatch.Path][planName] = append(workspaceSessionMap[bestMatch.Path][planName], session)
		} else {
			unmatchedSessions = append(unmatchedSessions, session)
		}
	}

	workspacesToShow := make(map[string]bool)
	if filterText != "" {
		for _, ws := range m.workspaces {
			if strings.Contains(strings.ToLower(ws.Name), filterText) || strings.Contains(strings.ToLower(ws.Path), filterText) {
				workspacesToShow[ws.Path] = true
				m.addParentWorkspaces(ws, workspacesToShow)
			}
		}
	}

	for wsPath := range workspaceSessionMap {
		workspacesToShow[wsPath] = true
		for _, ws := range m.workspaces {
			if ws.Path == wsPath {
				m.addParentWorkspaces(ws, workspacesToShow)
				break
			}
		}
	}

	for _, ws := range m.workspaces {
		if workspacesToShow[ws.Path] {
			// Check if this workspace should actually be displayed
			// Only show workspaces that either:
			// 1. Have sessions (directly or via plans)
			// 2. Are ancestors (parents) of other workspaces being shown
			hasSessions := workspaceSessionMap[ws.Path] != nil
			isAncestor := false
			for otherWsPath := range workspacesToShow {
				if ws.Path != otherWsPath && strings.HasPrefix(otherWsPath, ws.Path+string(filepath.Separator)) {
					isAncestor = true
					break
				}
			}

			// Skip empty leaf workspaces
			if !hasSessions && !isAncestor {
				continue
			}

			// Calculate workspace status based on sessions
			var wsStatus string
			if planGroups, ok := workspaceSessionMap[ws.Path]; ok {
				for _, sessions := range planGroups {
					for _, s := range sessions {
						if s.Status == "running" || s.Status == "idle" || s.Status == "pending_user" {
							wsStatus = "running"
							break
						}
					}
					if wsStatus == "running" {
						break
					}
				}
			}

			nodes = append(nodes, &displayNode{
				isSession:       false,
				workspace:       ws,
				prefix:          ws.TreePrefix,
				workspaceStatus: wsStatus,
			})

			if planGroups, ok := workspaceSessionMap[ws.Path]; ok {
				// Sort plan names for consistent order
				var planNames []string
				for name := range planGroups {
					planNames = append(planNames, name)
				}
				sort.Strings(planNames)

				sessionsWithoutPlan := planGroups[""]
				numPlanGroups := len(planNames)
				if sessionsWithoutPlan != nil {
					numPlanGroups--
				}

				planCount := 0
				for _, planName := range planNames {
					if planName == "" {
						continue
					}
					planCount++
					isLastPlan := (planCount == numPlanGroups) && (len(sessionsWithoutPlan) == 0)

					sessionsInPlan := planGroups[planName]
					planItem := createPlanListItem(planName, sessionsInPlan)

					// Calculate plan prefix
					var planPrefixBuilder strings.Builder
					indentPrefix := strings.ReplaceAll(ws.TreePrefix, "├─", "│ ")
					indentPrefix = strings.ReplaceAll(indentPrefix, "└─", "  ")
					planPrefixBuilder.WriteString(indentPrefix)
					if ws.Depth > 0 || ws.TreePrefix != "" {
						planPrefixBuilder.WriteString("  ")
					}
					if isLastPlan {
						planPrefixBuilder.WriteString("└─ ")
					} else {
						planPrefixBuilder.WriteString("├─ ")
					}
					planPrefix := planPrefixBuilder.String()

					nodes = append(nodes, &displayNode{
						isPlan:    true,
						plan:      &planItem,
						workspace: ws,
						prefix:    planPrefix,
					})

					// Add sessions under this plan
					for i, s := range sessionsInPlan {
						isLastSession := i == len(sessionsInPlan)-1

						var sessionPrefixBuilder strings.Builder
						sessionIndent := strings.ReplaceAll(planPrefix, "├─", "│ ")
						sessionIndent = strings.ReplaceAll(sessionIndent, "└─", "  ")
						sessionPrefixBuilder.WriteString(sessionIndent)
						sessionPrefixBuilder.WriteString("  ")

						if isLastSession {
							sessionPrefixBuilder.WriteString("└─ ")
						} else {
							sessionPrefixBuilder.WriteString("├─ ")
						}

						nodes = append(nodes, &displayNode{
							isSession: true,
							session:   s,
							workspace: ws,
							prefix:    sessionPrefixBuilder.String(),
						})
					}
				}

				// Add sessions without a plan directly under the workspace
				for i, s := range sessionsWithoutPlan {
					isLastItem := i == len(sessionsWithoutPlan)-1

					var sessionPrefixBuilder strings.Builder
					indentPrefix := strings.ReplaceAll(ws.TreePrefix, "├─", "│ ")
					indentPrefix = strings.ReplaceAll(indentPrefix, "└─", "  ")
					sessionPrefixBuilder.WriteString(indentPrefix)
					if ws.Depth > 0 || ws.TreePrefix != "" {
						sessionPrefixBuilder.WriteString("  ")
					}
					if isLastItem {
						sessionPrefixBuilder.WriteString("└─ ")
					} else {
						sessionPrefixBuilder.WriteString("├─ ")
					}

					nodes = append(nodes, &displayNode{
						isSession: true,
						session:   s,
						workspace: ws,
						prefix:    sessionPrefixBuilder.String(),
					})
				}
			}
		}
	}

	for _, s := range unmatchedSessions {
		nodes = append(nodes, &displayNode{
			isSession: true,
			session:   s,
			prefix:    "",
		})
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

// getCurrentDisplayNode returns the currently selected displayNode
func (m *Model) getCurrentDisplayNode() *displayNode {
	if m.cursor >= 0 && m.cursor < len(m.displayNodes) {
		return m.displayNodes[m.cursor]
	}
	return nil
}

// switchToTmuxSession handles switching to a tmux session
func (m Model) switchToTmuxSession(sessionName string) (tea.Model, tea.Cmd) {
	tmuxClient, err := tmux.NewClient()
	if err != nil {
		m.statusMessage = fmt.Sprintf("Error: tmux not available: %v", err)
		return m, nil
	}
	sessionExists, _ := tmuxClient.SessionExists(context.Background(), sessionName)

	if !sessionExists {
		// Session doesn't exist, so create it
		node := m.getCurrentDisplayNode()
		if node == nil || node.workspace == nil {
			m.statusMessage = "Error: no workspace selected to create session."
			return m, nil
		}

		opts := tmux.LaunchOptions{
			SessionName:      sessionName,
			WorkingDirectory: node.workspace.Path,
		}

		if err := tmuxClient.Launch(context.Background(), opts); err != nil {
			m.statusMessage = fmt.Sprintf("Failed to create session: %v", err)
			return m, nil
		}
		m.statusMessage = fmt.Sprintf("Created session '%s'", sessionName)
	}

	if os.Getenv("TMUX") != "" {
		// Switch to the session and close popup (works regardless of -E flag)
		if err := tmuxClient.SwitchClient(context.Background(), sessionName); err != nil {
			m.statusMessage = fmt.Sprintf("Failed to switch to session: %v", err)
			return m, nil
		}
		m.CommandOnExit = tmuxClient.ClosePopupCmd()
		return m, tea.Quit
	} else {
		m.MessageOnExit = fmt.Sprintf("Attach to session with:\ntmux attach -t %s", sessionName)
		return m, tea.Quit
	}
}

func (m *Model) getCurrentSession() *models.Session {
	if m.cursor >= 0 && m.cursor < len(m.displayNodes) {
		node := m.displayNodes[m.cursor]
		if node.isSession {
			return node.session
		}
	}
	return nil
}

func (m Model) updateFilterView(msg tea.Msg) (tea.Model, tea.Cmd) {
	statusOptions := []string{"running", "idle", "pending_user", "completed", "interrupted", "failed", "error", "hold", "todo", "abandoned"}
	typeOptions := []string{"claude_code", "chat", "interactive_agent", "oneshot", "headless_agent", "agent", "shell"}
	totalOptions := len(statusOptions) + len(typeOptions)

	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	if key.Matches(keyMsg, m.keys.Up) {
		if m.filterCursor > 0 {
			m.filterCursor--
		}
	} else if key.Matches(keyMsg, m.keys.Down) {
		if m.filterCursor < totalOptions-1 {
			m.filterCursor++
		}
	} else if key.Matches(keyMsg, m.keys.Select) || keyMsg.String() == " " {
		if m.filterCursor < len(statusOptions) {
			status := statusOptions[m.filterCursor]
			m.statusFilters[status] = !m.statusFilters[status]
		} else {
			typeIdx := m.filterCursor - len(statusOptions)
			typ := typeOptions[typeIdx]
			m.typeFilters[typ] = !m.typeFilters[typ]
		}
		prefs := FilterPreferences{
			StatusFilters: m.statusFilters,
			TypeFilters:   m.typeFilters,
		}
		m.saveFilterPreferences(prefs)
		m.updateFilteredAndDisplayNodes()
		m.cursor = 0
		m.scrollOffset = 0
	}
	return m, nil
}

// SelectedSession returns the currently selected session (if any)
func (m Model) SelectedSession() *models.Session {
	return m.selectedSession
}
