package browse

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-core/tui/components"
	gtable "github.com/mattsolo1/grove-core/tui/components/table"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/mattsolo1/grove-hooks/internal/utils"
)

func (m Model) View() string {
	if m.help.ShowAll {
		return m.help.View()
	}
	if m.showFilterView {
		return m.viewFilterOptions()
	}
	if m.showDetails && m.selectedSession != nil {
		return m.viewDetails()
	}

	switch m.viewMode {
	case treeView:
		return m.viewTree()
	default:
		return m.viewTable()
	}
}

func (m Model) viewTable() string {
	t := theme.DefaultTheme
	var b strings.Builder

	headerLine := ""
	if m.searchActive {
		headerLine += t.Muted.Render(m.filterInput.View()) + "  "
	}
	b.WriteString(headerLine)
	b.WriteString("\n")

	headers := []string{"WORKSPACE / JOB", "TYPE", "STATUS", "AGE"}
	var rows [][]string

	viewportHeight := m.getViewportHeight()
	startIdx := m.scrollOffset
	endIdx := m.scrollOffset + viewportHeight
	if endIdx > len(m.displayNodes) {
		endIdx = len(m.displayNodes)
	}

	if len(m.displayNodes) == 0 {
		b.WriteString("\n" + t.Muted.Render("No matching sessions or workspaces"))
	} else {
		for i := startIdx; i < endIdx; i++ {
			node := m.displayNodes[i]
			var row []string

			// WORKSPACE / JOB column
			// Use weight for hierarchy instead of explicit colors.
			// See: plans/tui-updates/14-terminal-ui-styling-philosophy.md
			var firstCol string
			if node.isSession {
				sessionTitle := ""
				if node.session.JobFilePath != "" {
					sessionTitle = path.Base(node.session.JobFilePath)
				} else if node.session.JobTitle != "" {
					sessionTitle = node.session.JobTitle
				} else {
					sessionTitle = node.session.ID
				}
				jobTypeIcon := getJobTypeIcon(node.session.Type)
				firstCol = node.prefix + jobTypeIcon + " " + utils.TruncateStr(sessionTitle, 40)
			} else if node.isPlan {
				statusIcon := getStatusIcon(node.plan.Status, "plan")
				label := "Plan:"
				labelStyle := t.Accent
				if !node.plan.IsActualPlan {
					label = "Group:"
					labelStyle = t.Muted
				}
				firstCol = node.prefix + statusIcon + " " + labelStyle.Render(label) + " " + t.Bold.Render(node.plan.Name) + " " + t.Muted.Render(fmt.Sprintf("(%d jobs)", node.plan.JobCount))
			} else { // Workspace
				var nameStyle lipgloss.Style
				var gitSymbol string

				// Determine symbol based on workspace type
				// Check for ecosystem worktree first (both IsEcosystem and IsWorktree are true)
				if node.workspace.IsEcosystem() && node.workspace.IsWorktree() {
					nameStyle = t.WorkspaceWorktree
					gitSymbol = theme.IconEcosystemWorktree
				} else if node.workspace.IsWorktree() {
					nameStyle = t.WorkspaceWorktree
					gitSymbol = theme.IconWorktree
				} else if node.workspace.IsEcosystem() {
					nameStyle = t.WorkspaceEcosystem
					gitSymbol = theme.IconEcosystem
				} else {
					nameStyle = t.WorkspaceStandard
					gitSymbol = theme.IconRepo
				}

				if node.jumpKey != 0 {
					trimmedPrefix := strings.TrimRight(node.prefix, " ")
					jumpLabel := t.Muted.Render(fmt.Sprintf("(%c)", node.jumpKey))
					firstCol = trimmedPrefix + jumpLabel + " " + t.Muted.Render(gitSymbol) + " " + nameStyle.Render(node.workspace.Name)
				} else {
					firstCol = node.prefix + t.Muted.Render(gitSymbol) + " " + nameStyle.Render(node.workspace.Name)
				}
			}
			row = append(row, firstCol)

			// TYPE column
			var typeCol string
			if node.isSession {
				s := node.session
				statusStyle := getStatusStyle(s.Status)
				typeCol = statusStyle.Render(fmt.Sprintf("[%s]", s.Type))
			}
			// Plans don't have a TYPE column entry anymore (job count moved to WORKSPACE col)
			row = append(row, typeCol)

			// STATUS column - only show for sessions, not for plans or workspaces
			var statusCol string
			if node.isSession {
				s := node.session
				statusIcon := getStatusIcon(s.Status, s.Type)
				statusStyle := getStatusStyle(s.Status)
				statusCol = statusIcon + " " + statusStyle.Render(s.Status)

				// Only show provider for interactive session types
				if s.Type == "interactive_agent" || s.Type == "" || s.Type == "claude_session" {
					provider := "claude_code"
					if s.Provider == "codex" {
						provider = "codex"
					} else if s.Provider != "" && s.Provider != "claude" {
						provider = s.Provider
					}
					statusCol += " " + t.Muted.Render(fmt.Sprintf("(%s)", provider))
				}
			}
			row = append(row, statusCol)

			// LAST ACTIVITY column
			var lastActivityCol string
			if node.isSession {
				s := node.session
				if s.Status == "running" || s.Status == "idle" || s.Status == "pending_user" {
					if !s.LastActivity.IsZero() {
						lastActivityCol = utils.FormatDuration(time.Since(s.LastActivity))
					} else if !s.StartedAt.IsZero() {
						lastActivityCol = utils.FormatDuration(time.Since(s.StartedAt))
					}
				}
			}
			row = append(row, lastActivityCol)

			rows = append(rows, row)
		}
	}

	if len(rows) > 0 {
		visibleCursor := m.cursor - m.scrollOffset
		tableStr := gtable.SelectableTable(headers, rows, visibleCursor)
		b.WriteString(tableStr)
	}

	b.WriteString("\n")
	if len(m.selectedIDs) > 0 {
		b.WriteString(t.Highlight.Render(fmt.Sprintf("[%d selected]", len(m.selectedIDs))) + " ")
	}
	if len(m.displayNodes) > viewportHeight {
		scrollInfo := fmt.Sprintf("(%d-%d of %d)", startIdx+1, endIdx, len(m.displayNodes))
		b.WriteString(t.Muted.Render(scrollInfo) + " ")
	}
	if m.statusMessage != "" {
		if strings.HasPrefix(m.statusMessage, "Error:") {
			b.WriteString(t.Error.Render(m.statusMessage) + " ")
		} else {
			b.WriteString(t.Success.Render(m.statusMessage) + " ")
		}
	}
	b.WriteString(m.help.View())
	return b.String()
}

func (m Model) viewTree() string {
    t := theme.DefaultTheme
    var b strings.Builder

    headerLine := ""
    if m.searchActive {
        headerLine += t.Muted.Render(m.filterInput.View()) + "  "
    }
    b.WriteString(headerLine)
    b.WriteString("\n")

    viewportHeight := m.getViewportHeight()
    startIdx := m.scrollOffset
    endIdx := m.scrollOffset + viewportHeight
    if endIdx > len(m.displayNodes) {
        endIdx = len(m.displayNodes)
    }

    if len(m.displayNodes) == 0 {
        b.WriteString("\n" + t.Muted.Render("No matching sessions or workspaces"))
    } else {
		// Render visible rows
		for i := startIdx; i < endIdx; i++ {
			node := m.displayNodes[i]
			var line strings.Builder

			// 1. Render cursor
			if i == m.cursor {
				line.WriteString("▶ ")
			} else {
				line.WriteString("  ")
			}

			// 2. Render the unified prefix
			line.WriteString(node.prefix)

			// 3. Render the node's content
			if node.isSession {
				s := node.session

				// Get job type icon
				jobTypeIcon := getJobTypeIcon(s.Type)

				sessionID := ""
				if s.JobFilePath != "" {
					sessionID = path.Base(s.JobFilePath)
				} else if s.JobTitle != "" {
					sessionID = s.JobTitle
				} else {
					sessionID = s.ID
				}

				statusStyle := getStatusStyle(s.Status)

				// Determine provider display only for interactive session types
				providerDisplay := ""
				if s.Type == "interactive_agent" || s.Type == "" || s.Type == "claude_session" {
					provider := "claude_code"
					if s.Provider == "codex" {
						provider = "codex"
					} else if s.Provider != "" && s.Provider != "claude" {
						provider = s.Provider
					}
					providerDisplay = " " + t.Muted.Render(fmt.Sprintf("(%s)", provider))
				}

				// Format: jobTypeIcon [jobType] title status (provider)
				baseInfo := fmt.Sprintf("%s %s %s %s%s",
					jobTypeIcon,
					statusStyle.Render(fmt.Sprintf("[%s]", s.Type)),
					utils.TruncateStr(sessionID, 40),
					statusStyle.Render(s.Status),
					providerDisplay,
				)

				// Augment display for linked interactive_agent jobs
				if s.Type == "interactive_agent" && s.ClaudeSessionID != "" {
					// Show only the Claude session state (not the agent's running state)
					linkedStatusStyle := getStatusStyle(s.Status)
					baseInfo = fmt.Sprintf("%s %s %s → %s %s%s",
						jobTypeIcon,
						linkedStatusStyle.Render(fmt.Sprintf("[%s]", s.Type)),
						utils.TruncateStr(sessionID, 40),
						utils.TruncateStr(s.ClaudeSessionID, 8),
						linkedStatusStyle.Render(s.Status),
						providerDisplay,
					)
					line.WriteString(baseInfo)
				} else {
					line.WriteString(baseInfo)
				}
			} else if node.isPlan {
				plan := node.plan
				statusIcon := getStatusIcon(plan.Status, "plan")

				label := "Plan:"
				labelStyle := t.Accent
				if !plan.IsActualPlan {
					label = "Group:"
					labelStyle = t.Muted
				}

				line.WriteString(fmt.Sprintf("%s %s %s %s",
					statusIcon,
					labelStyle.Render(label),
					t.Bold.Render(plan.Name),
					t.Muted.Render(fmt.Sprintf("(%d jobs)", plan.JobCount)),
				))
			} else {
				ws := node.workspace
				// Style workspace name based on its type using theme styles
				var nameStyle lipgloss.Style
				var gitSymbol string

				// Determine symbol based on workspace type
				// Check for ecosystem worktree first (both IsEcosystem and IsWorktree are true)
				if ws.IsEcosystem() && ws.IsWorktree() {
					nameStyle = t.WorkspaceWorktree
					gitSymbol = theme.IconEcosystemWorktree
				} else if ws.IsWorktree() {
					nameStyle = t.WorkspaceWorktree
					gitSymbol = theme.IconWorktree
				} else if ws.IsEcosystem() {
					nameStyle = t.WorkspaceEcosystem
					gitSymbol = theme.IconEcosystem
				} else {
					nameStyle = t.WorkspaceStandard
					gitSymbol = theme.IconRepo
				}

				if node.jumpKey != 0 {
					trimmedPrefix := strings.TrimRight(node.prefix, " ")
					jumpLabel := t.Muted.Render(fmt.Sprintf("(%c)", node.jumpKey))
					line.WriteString(trimmedPrefix)
					line.WriteString(jumpLabel)
					line.WriteString(" " + t.Muted.Render(gitSymbol) + " ")
					line.WriteString(nameStyle.Render(ws.Name))
				} else {
					line.WriteString(node.prefix)
					line.WriteString(t.Muted.Render(gitSymbol) + " ")
					line.WriteString(nameStyle.Render(ws.Name))
				}
			}

			b.WriteString(line.String() + "\n")
		}
	}


    b.WriteString("\n")
    if len(m.selectedIDs) > 0 {
        b.WriteString(t.Highlight.Render(fmt.Sprintf("[%d selected]", len(m.selectedIDs))) + " ")
    }
    if len(m.displayNodes) > viewportHeight {
        scrollInfo := fmt.Sprintf("(%d-%d of %d)", startIdx+1, endIdx, len(m.displayNodes))
        b.WriteString(t.Muted.Render(scrollInfo) + " ")
    }
    if m.statusMessage != "" {
        if strings.HasPrefix(m.statusMessage, "Error:") {
            b.WriteString(t.Error.Render(m.statusMessage) + " ")
        } else {
            b.WriteString(t.Success.Render(m.statusMessage) + " ")
        }
    }
    b.WriteString(m.help.View())
    return b.String()
}


func (m Model) viewDetails() string {
	s := m.selectedSession
	t := theme.DefaultTheme
	var content strings.Builder

	content.WriteString(components.RenderKeyValue("Session ID", s.ID))
	content.WriteString("\n")

	sessionType := s.Type
	if sessionType == "" || sessionType == "claude_session" {
		sessionType = "claude_code"
	} else if sessionType == "oneshot_job" {
		sessionType = "job"
	}
	content.WriteString(components.RenderKeyValue("Type", sessionType))
	content.WriteString("\n")

	statusStyle := getStatusStyle(s.Status)
	content.WriteString(components.RenderKeyValue("Status", statusStyle.Render(s.Status)))
	content.WriteString("\n")

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
	content.WriteString("\n\n")
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

	if s.PID > 0 {
		content.WriteString("\n")
		content.WriteString(components.RenderKeyValue("PID", fmt.Sprintf("%d", s.PID)))
		content.WriteString("\n")
	}
	if s.TmuxKey != "" {
		content.WriteString(components.RenderKeyValue("Tmux Key", s.TmuxKey))
		content.WriteString("\n")
	}

	content.WriteString("\n" + t.Muted.Render("enter/esc: back to list"))
	return components.RenderBox("Session Details", content.String(), m.width)
}

func (m Model) viewFilterOptions() string {
	t := theme.DefaultTheme
	var content strings.Builder

	content.WriteString(t.Header.Render("Filter Options") + "\n\n")

	statusOptions := []string{"running", "idle", "pending_user", "completed", "interrupted", "failed", "error", "hold", "todo", "abandoned"}
	typeOptions := []string{"claude_code", "chat", "interactive_agent", "oneshot", "headless_agent", "agent", "shell"}

	var rows [][]string
	rows = append(rows, []string{t.Muted.Render("STATUS FILTERS"), ""})
	for _, status := range statusOptions {
		checkbox := "[ ]"
		if m.statusFilters[status] {
			checkbox = "[✓]"
		}
		statusText := status
		if m.statusFilters[status] {
			// Use bold to indicate active filter, not explicit color
			statusText = t.Bold.Render(status)
		}
		rows = append(rows, []string{"  " + checkbox, statusText})
	}

	rows = append(rows, []string{"", ""}) // Spacing
	rows = append(rows, []string{t.Muted.Render("TYPE FILTERS"), ""})
	for _, typ := range typeOptions {
		checkbox := "[ ]"
		if m.typeFilters[typ] {
			checkbox = "[✓]"
		}
		typeText := typ
		if m.typeFilters[typ] {
			// Use bold to indicate active filter, not explicit color
			typeText = t.Bold.Render(typ)
		}
		rows = append(rows, []string{"  " + checkbox, typeText})
	}

	actualCursor := m.filterCursor + 1
	if m.filterCursor >= len(statusOptions) {
		actualCursor = m.filterCursor + 3
	}
	tableStr := gtable.SelectableTable([]string{"", ""}, rows, actualCursor)
	content.WriteString(tableStr)

	content.WriteString("\n\n" + t.Muted.Render("j/k/arrows: navigate • space: toggle • f/esc: close"))
	return components.RenderBox("Filters", content.String(), m.width)
}

func (m Model) generateScrollbar(viewHeight, totalItems int) []string {
	if totalItems == 0 || viewHeight == 0 || totalItems <= viewHeight {
		return []string{}
	}
	scrollbar := make([]string, viewHeight)
	thumbSize := max(1, (viewHeight*viewHeight)/totalItems)
	maxScroll := totalItems - viewHeight
	scrollProgress := 0.0
	if maxScroll > 0 {
		scrollProgress = float64(m.scrollOffset) / float64(maxScroll)
	}
	if scrollProgress < 0 { scrollProgress = 0 }
	if scrollProgress > 1 { scrollProgress = 1 }
	thumbStart := int(scrollProgress * float64(viewHeight-thumbSize))
	for i := 0; i < viewHeight; i++ {
		if i >= thumbStart && i < thumbStart+thumbSize {
			scrollbar[i] = "█"
		} else {
			scrollbar[i] = "░"
		}
	}
	return scrollbar
}

func getStatusStyle(status string) lipgloss.Style {
	t := theme.DefaultTheme
	switch status {
	case "running": return t.Success
	case "idle", "pending_user": return t.Warning
	case "completed": return t.Info
	case "todo": return t.Muted
	case "hold": return t.Warning
	case "abandoned": return t.Muted
	case "failed", "error": return t.Error
	default: return t.Muted
	}
}

func getStatusIcon(status string, sessionType string) string {
	// First determine the icon character
	var icon string
	if sessionType == "workspace" {
		// Simple activity icon for workspaces
		if status == "running" {
			icon = theme.IconStatusRunning
		} else {
			icon = ""
		}
	} else if sessionType == "plan" {
		switch status {
		case "completed":
			icon = theme.IconSuccess
		case "running":
			icon = theme.IconStatusRunning
		case "pending_user", "idle":
			icon = theme.IconStatusIdle
		case "failed", "error":
			icon = theme.IconStatusFailed
		default:
			icon = "…"
		}
	} else {
		switch status {
		case "completed":
			icon = theme.IconStatusCompleted
		case "running":
			icon = theme.IconStatusRunning
		case "idle":
			icon = theme.IconStatusIdle
		case "pending_user":
			if sessionType == "chat" {
				icon = theme.IconStatusIdle
			} else {
				icon = theme.IconStatusPendingUser
			}
		case "failed", "error":
			icon = theme.IconStatusFailed
		case "interrupted":
			icon = theme.IconStatusInterrupted
		case "hold":
			icon = theme.IconStatusHold
		case "todo":
			icon = theme.IconStatusTodo
		case "abandoned":
			icon = theme.IconStatusAbandoned
		default:
			icon = theme.IconStatusPendingUser
		}
	}

	// Apply the status color to the icon
	statusStyle := getStatusStyle(status)
	return statusStyle.Render(icon)
}

func max(a, b int) int {
	if a > b { return a }
	return b
}

func getJobTypeIcon(jobType string) string {
	switch jobType {
	case "chat":
		return theme.IconChat
	case "interactive_agent":
		return theme.IconInteractiveAgent
	case "oneshot":
		return theme.IconOneshot
	case "headless_agent", "agent":
		return theme.IconHeadlessAgent
	case "shell":
		return theme.IconShell
	default:
		return theme.IconOneshot
	}
}
