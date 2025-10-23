package browse

import (
	"fmt"
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

	headers := []string{"WORKSPACE / JOB", "TYPE", "STATUS", "LAST ACTIVITY"}
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
				sessionTitle := node.session.JobTitle
				if sessionTitle == "" {
					sessionTitle = node.session.ID
				}
				firstCol = node.prefix + utils.TruncateStr(sessionTitle, 40)
			} else if node.isPlan {
				firstCol = node.prefix + t.Bold.Render("Plan: "+node.plan.Name)
			} else { // Workspace
				var nameStyle lipgloss.Style
				var workspaceName string
				if node.workspace.IsWorktree() {
					nameStyle = t.WorkspaceWorktree
					workspaceName = node.workspace.Name + " " + t.Muted.Render("(⑂)")
				} else if node.workspace.IsEcosystem() {
					nameStyle = t.WorkspaceEcosystem
					workspaceName = node.workspace.Name
				} else {
					nameStyle = t.WorkspaceStandard
					workspaceName = node.workspace.Name
				}
				firstCol = node.prefix + nameStyle.Render(workspaceName)
			}
			row = append(row, firstCol)

			// TYPE column
			var typeCol string
			if node.isSession {
				sessionType := node.session.Type
				if sessionType == "" || sessionType == "claude_session" {
					if node.session.Provider == "codex" {
						sessionType = "codex"
					} else {
						sessionType = "claude_code"
					}
				}
				typeCol = sessionType

			} else if node.isPlan {
				typeCol = t.Muted.Render(fmt.Sprintf("(%d jobs)", node.plan.JobCount))
			}
			row = append(row, typeCol)

			// STATUS column - only show for sessions, not for plans or workspaces
			var statusCol string
			if node.isSession {
				s := node.session
				// Determine provider for display (claude_code, codex, etc.)
				provider := "claude_code"
				if s.Provider == "codex" {
					provider = "codex"
				} else if s.Provider != "" && s.Provider != "claude" {
					provider = s.Provider
				}
				statusIcon := getStatusIcon(s.Status, s.Type)
				statusStyle := getStatusStyle(s.Status)
				statusCol = statusIcon + " " + statusStyle.Render(s.Status) + " " + t.Muted.Render(fmt.Sprintf("(%s)", provider))
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
				statusIcon := getStatusIcon(s.Status, s.Type)
				sessionType := s.Type
				if sessionType == "" || sessionType == "claude_session" {
					if s.Provider == "codex" {
						sessionType = "codex"
					} else {
						sessionType = "claude_code"
					}
				}

				sessionID := s.ID
				if s.JobTitle != "" {
					sessionID = s.JobTitle
				}

				statusStyle := getStatusStyle(s.Status)

				baseInfo := fmt.Sprintf("%s %s %s %s",
					statusIcon,
					utils.TruncateStr(sessionID, 40),
					statusStyle.Render(s.Status),
					t.Muted.Render(fmt.Sprintf("(%s)", sessionType)),
				)

				// Augment display for linked interactive_agent jobs
				if s.Type == "interactive_agent" && s.ClaudeSessionID != "" {
					// For display purposes, show 'running' for the agent, but use the live status for the linked session
					agentStatusStyle := getStatusStyle("running")
					agentStatusIcon := getStatusIcon("running", s.Type)
					baseInfo = fmt.Sprintf("%s %s %s %s",
						agentStatusIcon,
						utils.TruncateStr(sessionID, 40),
						agentStatusStyle.Render("running"),
						t.Muted.Render("(interactive_agent)"),
					)

					provider := "claude_code"
					if s.Provider != "" {
						provider = s.Provider
					}
					if provider == "claude" {
						provider = "claude_code"
					}
					linkedStatusIcon := getStatusIcon(s.Status, provider)
					linkedStatusStyle := getStatusStyle(s.Status)
					augmentedInfo := fmt.Sprintf(" → %s %s %s %s",
						linkedStatusIcon, utils.TruncateStr(s.ClaudeSessionID, 8),
						linkedStatusStyle.Render(s.Status), t.Muted.Render(fmt.Sprintf("(%s)", provider)),
					)
					line.WriteString(baseInfo + t.Muted.Render(augmentedInfo))
				} else {
					line.WriteString(baseInfo)
				}
			} else if node.isPlan {
				plan := node.plan
				statusIcon := getStatusIcon(plan.Status, "plan")
				statusStyle := getStatusStyle(plan.Status)

				line.WriteString(fmt.Sprintf("%s Plan: %s (%d jobs, %s)",
					statusIcon,
					t.Bold.Render(plan.Name),
					plan.JobCount,
					statusStyle.Render(plan.Status),
				))
			} else {
				ws := node.workspace
				// Style workspace name based on its type using theme styles
				var nameStyle lipgloss.Style
				if ws.IsWorktree() {
					nameStyle = t.WorkspaceWorktree
				} else if ws.IsEcosystem() {
					nameStyle = t.WorkspaceEcosystem
				} else {
					nameStyle = t.WorkspaceStandard
				}

				// Prepend status icon if workspace has active sessions
				if node.workspaceStatus == "running" {
					statusIcon := getStatusIcon(node.workspaceStatus, "workspace")
					line.WriteString(statusIcon + " ")
				}

				// Render workspace name with worktree indicator in muted parens
				if ws.IsWorktree() {
					line.WriteString(nameStyle.Render(ws.Name) + " " + t.Muted.Render("(⑂)"))
				} else {
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
			icon = "▶"
		} else {
			icon = ""
		}
	} else if sessionType == "plan" {
		switch status {
		case "completed":
			icon = "✔"
		case "running":
			icon = "▶"
		case "pending_user", "idle":
			icon = "⏸"
		case "failed", "error":
			icon = "✗"
		default:
			icon = "…"
		}
	} else {
		switch status {
		case "completed":
			icon = "●"
		case "running":
			icon = "◐"
		case "idle":
			icon = "⏸"
		case "pending_user":
			if sessionType == "chat" {
				icon = "⏸"
			} else {
				icon = "○"
			}
		case "failed", "error":
			icon = "✗"
		case "interrupted":
			icon = "⊗"
		case "hold":
			icon = "⏸"
		case "todo":
			icon = "○"
		case "abandoned":
			icon = "⊗"
		default:
			icon = "○"
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
