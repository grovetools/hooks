package view

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
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/tmux"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/core/tui/components/help"
	"github.com/grovetools/core/tui/embed"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/core/util/pathutil"
	"github.com/grovetools/flow/pkg/orchestration"
	"github.com/grovetools/hooks/internal/utils"
)

// FilterPreferences stores the user's filter preferences
type FilterPreferences struct {
	StatusFilters map[string]bool `json:"status_filters"`
	TypeFilters   map[string]bool `json:"type_filters"`
}

// GetAllSessionsFunc is the function type for getting all sessions
type GetAllSessionsFunc func(client daemon.Client, hideCompleted bool) ([]*models.Session, error)

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
	Name         string
	JobCount     int
	Status       string
	StatusParts  map[string]int
	LastUpdated  time.Time
	IsActualPlan bool
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
	jumpKey         rune   // Keyboard shortcut for quick navigation (1-9)
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
	daemonClient    daemon.Client
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
	jumpMap         map[rune]int // Maps keyboard shortcuts (1-9) to displayNode indices

	// For "new" indicator
	accessHistory map[string]time.Time

	// Exit actions
	CommandOnExit *exec.Cmd
	MessageOnExit string

	// Function dependencies (to avoid import cycles)
	getAllSessions        GetAllSessionsFunc
	dispatchNotifications DispatchNotificationsFunc
	saveFilterPreferences SaveFilterPreferencesFunc

	// Embed contract: when hosted inside the terminal multiplexer, the
	// host issues embed.SetWorkspaceMsg / FocusMsg / BlurMsg as the
	// active workspace changes. activeWorkspace scopes the session list
	// when localScope is true; standalone callers leave both unset and
	// the panel falls back to the legacy global view.
	activeWorkspace *workspace.WorkspaceNode
	localScope      bool
	hosted          bool

	// SSE stream lifecycle. Stored as a pointer so the bubbletea
	// value-receiver Update path doesn't copy the embedded sync
	// primitives (sync.WaitGroup / sync.Once / sync.Mutex). Allocated
	// once in NewModel and shared across all subsequent value copies.
	// Close() forwards to stream.close() to cancel the context and wait
	// for the in-flight read goroutine to exit. Mirrors the flow status
	// teardown fix.
	stream *streamLifecycle
}

func NewModel(
	cfg *config.Config,
	sessions []*models.Session,
	workspaces []*workspace.WorkspaceNode,
	daemonClient daemon.Client,
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

	keys := NewKeyMap(cfg)

	model := Model{
		sessions:              sessions,
		workspaces:            workspaces,
		filteredSessions:      sessions,
		filterInput:           ti,
		selectedIDs:           make(map[string]bool),
		daemonClient:          daemonClient,
		lastRefresh:           time.Now(),
		keys:                  keys,
		help:                  help.New(keys),
		hideCompleted:         hideCompleted,
		statusFilters:         filterPrefs.StatusFilters,
		typeFilters:           filterPrefs.TypeFilters,
		viewMode:              tableView,
		jumpMap:               make(map[rune]int),
		accessHistory:         make(map[string]time.Time),
		getAllSessions:        getAllSessions,
		dispatchNotifications: dispatchNotifications,
		saveFilterPreferences: saveFilterPreferences,
		stream:                newStreamLifecycle(),
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

type accessHistoryMsg map[string]time.Time

const gChordTimeout = 400 * time.Millisecond

func loadAccessHistoryCmd() tea.Cmd {
	return func() tea.Msg {
		configDir := utils.ExpandPath("~/.grove")
		historyMap, err := workspace.LoadAccessHistoryAsMap(configDir)
		if err != nil {
			return accessHistoryMsg{} // Return empty map on error
		}
		return accessHistoryMsg(historyMap)
	}
}

// updateAccessHistory updates the gmux access-history.json file for a workspace
func updateAccessHistory(workspacePath string) error {
	configDir := utils.ExpandPath("~/.grove")
	return workspace.UpdateAccessHistory(configDir, workspacePath)
}

func (m Model) Init() tea.Cmd {
	// Phase 2.1: drop the 1-second tickMsg polling loop in favor of an
	// SSE subscription to the daemon. The Update loop handles the
	// resulting daemonStreamConnectedMsg / daemonStateUpdateMsg cycle
	// and re-applies the session list as events arrive.
	//
	// tickMsg handling stays in Update for legacy callers that emit it
	// after a manual mutation (kill, mark-complete) — those paths
	// trigger a one-shot refetch instead of restarting a periodic tick.
	return tea.Batch(
		textinput.Blink,
		loadAccessHistoryCmd(),
		subscribeToDaemonCmd(m.daemonClient),
	)
}

// Close releases the SSE stream lifecycle resources owned by this Model
// instance. Idempotent — host panels should call this from their own
// Close() override; standalone callers can ignore it (process exit tears
// the stream down).
func (m Model) Close() error {
	m.stream.close()
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	// Embed contract messages from the host multiplexer take precedence
	// so workspace repointing happens before any session-level message
	// processing in the same update tick.
	switch hm := msg.(type) {
	case embed.SetWorkspaceMsg:
		m.activeWorkspace = hm.Node
		m.localScope = true
		m.hosted = true
		m.updateFilteredAndDisplayNodes()
		return m, nil
	case embed.FocusMsg:
		// Trigger an opportunistic refetch on focus so the panel always
		// shows fresh data when the user pops back to it. The SSE
		// stream usually keeps state current, but a missed event during
		// a host workspace switch is harmless to recover from this way.
		return m, fetchSessionsCmd(m.getAllSessions, m.daemonClient, m.hideCompleted)
	case embed.BlurMsg:
		// No-op for now: we keep the SSE stream alive while blurred so
		// the user doesn't see stale data when refocusing. Hosts that
		// want to pause work entirely should call Close().
		return m, nil
	}

	switch msg := msg.(type) {
	case gChordTimeoutMsg:
		m.gPressed = false
		return m, nil

	case accessHistoryMsg:
		m.accessHistory = msg
		return m, nil

	case noteCompleteMsg:
		if msg.err != nil {
			m.statusMessage = fmt.Sprintf("Error: %v", msg.err)
			return m, nil
		}
		m.statusMessage = fmt.Sprintf("Marked '%s' as completed", msg.filename)
		// Trigger an asynchronous one-shot refetch to remove the note
		// from the list. The daemon will also push an SSE event for the
		// state change, but eager refetch keeps the UI snappy when the
		// daemon is briefly offline.
		return m, fetchSessionsCmd(m.getAllSessions, m.daemonClient, m.hideCompleted)

	case daemonStreamConnectedMsg:
		// SSE stream is open. Stash channel + cancel on the model's
		// stream lifecycle and start consuming updates. Each update
		// queues another read via readDaemonStreamCmd.
		m.stream.store(msg.ch, msg.cancel)
		return m, m.stream.readDaemonStreamCmd(msg.ch)

	case daemonStreamErrorMsg:
		// Subscription failed (daemon offline, etc.). Non-fatal — the
		// initial fetched session list stays on screen. The user can
		// still drive the panel via keyboard, just without live updates.
		return m, nil

	case daemonStateUpdateMsg:
		// Apply the update if it carries session data, then queue the
		// next read. Single-session lifecycle deltas (session intent /
		// confirm / end) don't carry the bulk Sessions slice, so we
		// kick off a background refetch to pull the latest list.
		var follow tea.Cmd
		if len(msg.update.Sessions) > 0 {
			m.applySessions(msg.update.Sessions)
		} else if msg.update.UpdateType == "session" {
			follow = fetchSessionsCmd(m.getAllSessions, m.daemonClient, m.hideCompleted)
		}
		next := m.stream.readDaemonStreamCmd(m.stream.ch)
		if follow == nil {
			return m, next
		}
		return m, tea.Batch(follow, next)

	case sessionsRefetchedMsg:
		if msg.err == nil && msg.sessions != nil {
			m.applySessions(msg.sessions)
		}
		return m, nil

	case editFileAndQuitMsg:
		// Print protocol string and quit - Neovim plugin will handle the file opening
		fmt.Printf("EDIT_FILE:%s\n", msg.filePath)
		return m, tea.Quit

	case tickMsg:
		// Phase 2.1: tickMsg is no longer self-rescheduling. It still
		// fires as a manual refresh trigger after some legacy actions
		// (e.g. EditFinished from the standalone CLI nvim plugin path)
		// but periodic polling is now driven by the SSE subscription
		// kicked off in Init().
		newSessions, err := m.getAllSessions(m.daemonClient, m.hideCompleted)
		if err != nil {
			m.statusMessage = fmt.Sprintf("Error refreshing: %v", err)
		} else {
			m.applySessions(newSessions)
		}
		return m, loadAccessHistoryCmd()

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.SetSize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyMsg:
		// Handle numeric jump keys (1-9) when search is not active
		if !m.searchActive && msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
			keyRune := msg.Runes[0]
			if keyRune >= '1' && keyRune <= '9' {
				if targetIndex, ok := m.jumpMap[keyRune]; ok {
					if targetIndex < len(m.displayNodes) {
						m.cursor = targetIndex
						// Ensure the new cursor position is visible
						viewportHeight := m.getViewportHeight()
						if m.cursor < m.scrollOffset {
							m.scrollOffset = m.cursor
						} else if m.cursor >= m.scrollOffset+viewportHeight {
							m.scrollOffset = m.cursor - viewportHeight + 1
						}
						return m, nil // Key press handled
					}
				}
			}
		}

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
			// When embedded inside a host TUI, swallow the quit
			// keystroke instead of returning tea.Quit — quitting
			// would tear down the host program. The host owns
			// global navigation (Tab) so the user can leave the
			// panel via the rail.
			if m.hosted {
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
			if m.hosted {
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

		if key.Matches(msg, m.keys.ScopeToggle) && !m.searchActive && !m.showFilterView {
			// Toggle the local/global scope. Local restricts the
			// session list to the active workspace; global shows
			// every session the daemon knows about. Mirrors the
			// memory panel's alt+s pattern.
			m.localScope = !m.localScope
			m.updateFilteredAndDisplayNodes()
			m.cursor = 0
			m.scrollOffset = 0
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

			// When hosted, swallow Confirm/Open/Edit on
			// non-session nodes (plans, workspaces): the
			// fallthrough paths spawn tmux windows + return
			// tea.Quit which would kill the host program.
			if m.hosted && !node.isSession {
				return m, nil
			}

			// Handle context-aware actions for workspaces and plans
			if node.isPlan {
				// Action for plan: switch to workspace session and open flow plan TUI in new window
				sessionName := node.workspace.Identifier("_")
				planName := node.plan.Name
				windowName := fmt.Sprintf("plan-%s", planName)

				// Update access history for this workspace
				if node.workspace != nil {
					if err := updateAccessHistory(node.workspace.Path); err != nil {
						m.statusMessage = fmt.Sprintf("Warning: failed to update access history: %v", err)
					}
				}

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
				sessionName := node.workspace.Identifier("_")
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

					// Phase 3.1: when hosted, hand routing off to the
					// terminal multiplexer via OpenAgentSessionMsg so
					// the host can spawn / focus the agent panel
					// without quitting the embedded TUI. We do this
					// for ALL session statuses when hosted — the
					// fallthrough tmux paths below all return
					// tea.Quit, which would tear down the host.
					if m.hosted {
						sid := session.ID
						return m, func() tea.Msg {
							return embed.OpenAgentSessionMsg{SessionID: sid}
						}
					}

					if sessionType == "claude_code" && session.TmuxKey != "" && (session.Status == "running" || session.Status == "idle") {
						// Update access history for this workspace
						if node.workspace != nil {
							if err := updateAccessHistory(node.workspace.Path); err != nil {
								m.statusMessage = fmt.Sprintf("Warning: failed to update access history: %v", err)
							}
						}

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

					if (session.Type == "interactive_agent" || session.Type == "isolated_agent") && (session.Status == "running" || session.Status == "idle") {
						workDir := session.WorkingDirectory
						projInfo, err := workspace.GetProjectByPath(workDir)
						if err != nil {
							m.statusMessage = fmt.Sprintf("Error: could not find workspace for %s", workDir)
							return m, nil
						}

						// Update access history for this workspace
						if err := updateAccessHistory(projInfo.Path); err != nil {
							m.statusMessage = fmt.Sprintf("Warning: failed to update access history: %v", err)
						}

						sessionName := projInfo.Identifier("_")
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
						// Phase 3.1: when hosted in the terminal
						// multiplexer, emit embed.EditRequestMsg so
						// the host suspends the bubbletea loop and
						// runs $EDITOR — the in-process exec path
						// would corrupt the host's altscreen.
						if m.hosted {
							path := session.JobFilePath
							return m, func() tea.Msg {
								return embed.EditRequestMsg{Path: path}
							}
						}
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
		} else if key.Matches(msg, m.keys.CopyPath) {
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
				if session.Type != "" && session.Type != "claude_session" {
					m.statusMessage = "Error: Can only kill Claude sessions, not flow jobs"
					return m, nil
				}
				// Phase 3.1: prefer the daemon-mediated kill path so
				// the daemon can clean up its in-memory store and
				// background workers atomically. The LocalClient
				// returns an error from KillSession; we treat any
				// daemon-side failure as a signal to fall back to
				// the in-process syscall path so the standalone CLI
				// keeps working when groved is offline.
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				err := m.daemonClient.KillSession(ctx, session.ID)
				cancel()
				if err == nil {
					m.statusMessage = fmt.Sprintf("Killed session %s", session.ID[:min(8, len(session.ID))])
					return m, nil
				}
				// Fall back to in-process syscall + filesystem cleanup
				// so the standalone CLI still works without the daemon.
				groveSessionsDir := filepath.Join(paths.StateDir(), "hooks", "sessions")
				sessionDir := filepath.Join(groveSessionsDir, session.ID)
				pidFile := filepath.Join(sessionDir, "pid.lock")
				pidContent, readErr := os.ReadFile(pidFile)
				if readErr != nil {
					m.statusMessage = fmt.Sprintf("Error: daemon kill failed (%v) and PID file unreadable: %v", err, readErr)
					return m, nil
				}
				var pid int
				if _, scanErr := fmt.Sscanf(string(pidContent), "%d", &pid); scanErr != nil {
					m.statusMessage = fmt.Sprintf("Error: invalid PID: %v", scanErr)
					return m, nil
				}
				if killErr := syscall.Kill(pid, syscall.SIGTERM); killErr != nil {
					m.statusMessage = fmt.Sprintf("Error: failed to kill PID %d: %v", pid, killErr)
					return m, nil
				}
				os.RemoveAll(sessionDir)
				m.statusMessage = fmt.Sprintf("Killed session %s (PID %d, fallback)", session.ID[:min(8, len(session.ID))], pid)
			}
		} else if key.Matches(msg, m.keys.MarkComplete) {
			if session := m.getCurrentSession(); session != nil && !m.showDetails {
				return m, m.markNoteComplete(session)
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

	// Phase 2.2: when hosted inside the terminal multiplexer with a
	// local scope active, restrict the visible sessions to those whose
	// working directory falls under the active workspace path. Path
	// normalization handles macOS/Windows case-insensitive matches.
	var scopePath string
	if m.localScope && m.activeWorkspace != nil {
		if normalized, err := pathutil.NormalizeForLookup(m.activeWorkspace.Path); err == nil {
			scopePath = normalized
		}
	}

	for _, s := range m.sessions {
		if !m.statusFilters[s.Status] {
			continue
		}
		if scopePath != "" {
			sessionPath, err := pathutil.NormalizeForLookup(s.WorkingDirectory)
			if err != nil || sessionPath == "" {
				continue
			}
			if sessionPath != scopePath && !strings.HasPrefix(sessionPath+"/", scopePath+"/") {
				continue
			}
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

	// Dedupe by JobFilePath: keep the most recently active session
	// per job. Without this, every restart / retry of the same job
	// shows up as its own row (the daemon legitimately tracks each
	// spawn) and the tree fills up with 16+ identical entries for
	// the same .md file. Sessions without a JobFilePath are kept
	// as-is (they aren't tied to a single job).
	m.filteredSessions = dedupeSessionsByJobPath(m.filteredSessions)

	m.buildDisplayTree()
}

// dedupeSessionsByJobPath collapses sessions that share a JobFilePath
// down to the single most-recently-active entry. Sessions with an
// empty JobFilePath are preserved as-is. Order is preserved for the
// surviving sessions.
func dedupeSessionsByJobPath(in []*models.Session) []*models.Session {
	if len(in) == 0 {
		return in
	}
	bestIdx := make(map[string]int, len(in))
	for i, s := range in {
		if s.JobFilePath == "" {
			continue
		}
		if cur, ok := bestIdx[s.JobFilePath]; ok {
			if in[i].LastActivity.After(in[cur].LastActivity) {
				bestIdx[s.JobFilePath] = i
			}
			continue
		}
		bestIdx[s.JobFilePath] = i
	}
	out := make([]*models.Session, 0, len(in))
	for i, s := range in {
		if s.JobFilePath == "" {
			out = append(out, s)
			continue
		}
		if bestIdx[s.JobFilePath] == i {
			out = append(out, s)
		}
	}
	return out
}

func createPlanListItem(planName string, sessions []*models.Session) PlanListItem {
	item := PlanListItem{
		Name:         planName,
		JobCount:     len(sessions),
		StatusParts:  make(map[string]int),
		IsActualPlan: false,
	}

	if len(sessions) > 0 {
		// A real plan will have its job files inside a "plans" directory.
		jobPath := sessions[0].JobFilePath
		if strings.Contains(jobPath, "/plans/") {
			item.IsActualPlan = true
		}
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

		// Normalize paths for case-insensitive comparison on macOS/Windows
		sessionWorkDir, err := pathutil.NormalizeForLookup(session.WorkingDirectory)
		if err != nil {
			continue // Skip if path normalization fails
		}

		for _, ws := range m.workspaces {
			wsPath, err := pathutil.NormalizeForLookup(ws.Path)
			if err != nil {
				continue // Skip if path normalization fails
			}
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

	// Assign jump keys to sub-projects (workspaces with depth > 0)
	m.jumpMap = make(map[rune]int)
	jumpCounter := '1'
	for i, node := range nodes {
		// Assign jump keys to sub-projects (depth > 0)
		if !node.isSession && !node.isPlan && node.workspace != nil && node.workspace.Depth > 0 {
			if jumpCounter <= '9' {
				nodes[i].jumpKey = jumpCounter
				m.jumpMap[jumpCounter] = i
				jumpCounter++
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

	// Get workspace path for access history update
	node := m.getCurrentDisplayNode()
	var workspacePath string
	if node != nil && node.workspace != nil {
		workspacePath = node.workspace.Path
	}

	if !sessionExists {
		// Session doesn't exist, so create it
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

	// Update access history for this workspace
	if workspacePath != "" {
		if err := updateAccessHistory(workspacePath); err != nil {
			// Don't fail the operation, just log the error
			m.statusMessage = fmt.Sprintf("Warning: failed to update access history: %v", err)
		}
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

// applySessions installs a fresh session slice on the model, preserving
// the current cursor position by ID where possible. Used by both the
// SSE update path (daemonStateUpdateMsg / sessionsRefetchedMsg) and the
// legacy tickMsg manual refresh path so they share cursor / dispatch
// semantics.
func (m *Model) applySessions(newSessions []*models.Session) {
	var selectedID string
	if m.cursor >= 0 && m.cursor < len(m.displayNodes) {
		if m.displayNodes[m.cursor].isSession {
			selectedID = m.displayNodes[m.cursor].session.ID
		}
	}

	if len(m.previousSessions) > 0 {
		go m.dispatchNotifications(m.previousSessions, newSessions)
	}
	m.previousSessions = m.sessions
	m.sessions = newSessions
	m.updateFilteredAndDisplayNodes()

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
	typeOptions := []string{"claude_code", "chat", "interactive_agent", "isolated_agent", "oneshot", "headless_agent", "agent", "shell"}
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
	} else if key.Matches(keyMsg, m.keys.Select) {
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

// markNoteComplete marks the given session/job as completed.
// For agent sessions (interactive_agent, isolated_agent), it delegates to
// `flow plan complete` which handles process cleanup, tmux teardown, and archiving.
// For chat/oneshot sessions, it directly updates frontmatter and notifies the daemon.
func (m *Model) markNoteComplete(session *models.Session) tea.Cmd {
	client := m.daemonClient
	return func() tea.Msg {
		if session.JobFilePath == "" {
			return noteCompleteMsg{err: fmt.Errorf("no job file path for session %s", session.ID)}
		}

		// Agent sessions need full cleanup via flow plan complete
		if session.Type == "interactive_agent" || session.Type == "isolated_agent" {
			cmd := exec.Command("flow", "plan", "complete", session.JobFilePath)
			if output, err := cmd.CombinedOutput(); err != nil {
				return noteCompleteMsg{err: fmt.Errorf("flow plan complete failed: %w\n%s", err, string(output))}
			}
			return noteCompleteMsg{
				sessionID: session.ID,
				filename:  filepath.Base(session.JobFilePath),
			}
		}

		// Chat/oneshot: update frontmatter directly
		content, err := os.ReadFile(session.JobFilePath)
		if err != nil {
			return noteCompleteMsg{err: fmt.Errorf("failed to read note: %w", err)}
		}

		updatedContent, err := orchestration.UpdateFrontmatter(content, map[string]interface{}{
			"status": "completed",
		})
		if err != nil {
			return noteCompleteMsg{err: fmt.Errorf("failed to update frontmatter: %w", err)}
		}

		if err := os.WriteFile(session.JobFilePath, updatedContent, 0644); err != nil {
			return noteCompleteMsg{err: fmt.Errorf("failed to write note: %w", err)}
		}

		// Notify daemon so session/job is updated in state
		if client != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = client.EndSession(ctx, session.ID, "completed")
		}

		return noteCompleteMsg{
			sessionID: session.ID,
			filename:  filepath.Base(session.JobFilePath),
		}
	}
}

type noteCompleteMsg struct {
	sessionID string
	filename  string
	err       error
}
