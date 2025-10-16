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

	headers := []string{"", "SESSION ID", "TYPE", "STATUS", "REPOSITORY", "WORKTREE", "STARTED"}
	var rows [][]string

	viewportHeight := m.getViewportHeight()
	startIdx := m.scrollOffset
	endIdx := m.scrollOffset + viewportHeight
	if endIdx > len(m.filteredSessions) {
		endIdx = len(m.filteredSessions)
	}

	for i := startIdx; i < endIdx; i++ {
		s := m.filteredSessions[i]
		sessionType := s.Type
		isClaudeSession := sessionType == "" || sessionType == "claude_session"
		if isClaudeSession {
			sessionType = "claude_code"
		}
		sessionID := s.ID
		if !isClaudeSession && s.JobTitle != "" {
			sessionID = s.JobTitle
		}

		var sessionIDStr, sessionTypeStr string
		if isClaudeSession {
			sessionIDStr = lipgloss.NewStyle().Foreground(t.Colors.Blue).Render(utils.TruncateStr(sessionID, 30))
			sessionTypeStr = lipgloss.NewStyle().Foreground(t.Colors.Blue).Render(sessionType)
		} else {
			sessionIDStr = lipgloss.NewStyle().Foreground(t.Colors.Violet).Render(utils.TruncateStr(sessionID, 30))
			sessionTypeStr = lipgloss.NewStyle().Foreground(t.Colors.Violet).Render(sessionType)
		}

		repository := s.Repo
		if repository == "" {
			if !isClaudeSession && s.PlanName != "" {
				repository = s.PlanName
			} else {
				repository = "n/a"
			}
		}
		worktree := s.Branch
		if worktree == "" {
			worktree = "n/a"
		}

		statusStyle := getStatusStyle(s.Status)
		statusIcon := getStatusIcon(s.Status, s.Type)
		var statusStr string
		if s.Status == "running" || s.Status == "idle" || s.Status == "pending_user" {
			var elapsedStr string
			if !s.LastActivity.IsZero() {
				elapsed := utils.FormatDuration(time.Since(s.LastActivity))
				elapsedStr = fmt.Sprintf("(%s)", elapsed)
			} else if !s.StartedAt.IsZero() {
				elapsed := utils.FormatDuration(time.Since(s.StartedAt))
				elapsedStr = fmt.Sprintf("(%s)", elapsed)
			} else {
				elapsedStr = "(unknown)"
			}
			statusStr = statusStyle.Render(statusIcon+" "+s.Status) + " " + t.Muted.Render(elapsedStr)
		} else {
			statusStr = statusStyle.Render(statusIcon + " " + s.Status)
		}

		var indicator string
		if m.selectedIDs[s.ID] && i == m.cursor {
			indicator = "[*]▶"
		} else if m.selectedIDs[s.ID] {
			indicator = "[*] "
		} else if i == m.cursor {
			indicator = "  ▶"
		} else {
			indicator = "   "
		}
		var startedStr string
		if s.StartedAt.IsZero() {
			startedStr = "n/a"
		} else {
			if time.Since(s.StartedAt) < 24*time.Hour {
				startedStr = utils.FormatDuration(time.Since(s.StartedAt)) + " ago"
			} else {
				startedStr = s.StartedAt.Format("Jan 2 15:04")
			}
		}
		rows = append(rows, []string{
			utils.PadStr(indicator, 4),
			utils.PadStr(sessionIDStr, 32),
			utils.PadStr(sessionTypeStr, 18),
			utils.PadStr(statusStr, 20),
			utils.PadStr(utils.TruncateStr(repository, 25), 25),
			utils.PadStr(utils.TruncateStr(worktree, 20), 20),
			utils.PadStr(startedStr, 12),
		})
	}

	if len(m.filteredSessions) > 0 {
		visibleCursor := m.cursor - m.scrollOffset
		tableStr := gtable.SelectableTable(headers, rows, visibleCursor)
		scrollbar := m.generateScrollbar(viewportHeight, len(m.filteredSessions))
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

	b.WriteString("\n")
	if len(m.selectedIDs) > 0 {
		b.WriteString(t.Highlight.Render(fmt.Sprintf("[%d selected]", len(m.selectedIDs))) + " ")
	}
	if len(m.filteredSessions) > viewportHeight {
		scrollInfo := fmt.Sprintf("(%d-%d of %d)", startIdx+1, endIdx, len(m.filteredSessions))
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
					sessionType = "claude_code"
				}

				sessionID := s.ID
				if s.JobTitle != "" {
					sessionID = s.JobTitle
				}

				statusStyle := getStatusStyle(s.Status)

				line.WriteString(fmt.Sprintf("%s %s (%s, %s)",
					statusIcon,
					utils.TruncateStr(sessionID, 40),
					sessionType,
					statusStyle.Render(s.Status),
				))
			} else if node.isPlan {
				plan := node.plan
				statusIcon := getStatusIcon(plan.Status, "plan")
				statusStyle := getStatusStyle(plan.Status)

				line.WriteString(fmt.Sprintf("%s Plan: %s (%d jobs, %s)",
					statusIcon,
					t.Highlight.Render(plan.Name),
					plan.JobCount,
					statusStyle.Render(plan.Status),
				))
			} else {
				ws := node.workspace
				// Style workspace name based on its type
				var nameStyle lipgloss.Style
				if ws.IsWorktree() {
					nameStyle = lipgloss.NewStyle().Foreground(t.Colors.Blue)
				} else if ws.IsEcosystem() {
					nameStyle = lipgloss.NewStyle().Foreground(t.Colors.Cyan).Bold(true)
				} else {
					nameStyle = lipgloss.NewStyle().Foreground(t.Colors.Cyan)
				}
				line.WriteString(nameStyle.Render(ws.Name))
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
			statusText = t.Success.Render(status)
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
			typeText = t.Info.Render(typ)
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
	case "abandoned": return t.Faint
	case "failed", "error": return t.Error
	default: return t.Muted
	}
}

func getStatusIcon(status string, sessionType string) string {
	if sessionType == "plan" {
		switch status {
		case "completed":
			return "✔"
		case "running":
			return "▶"
		case "pending_user", "idle":
			return "⏸"
		case "failed", "error":
			return "✗"
		default:
			return "…"
		}
	}

	switch status {
	case "completed":
		return "●"
	case "running":
		return "◐"
	case "idle":
		return "⏸"
	case "pending_user":
		if sessionType == "chat" {
			return "⏸"
		}
		return "○"
	case "failed", "error":
		return "✗"
	case "interrupted":
		return "⊗"
	case "hold":
		return "⏸"
	case "todo":
		return "○"
	case "abandoned":
		return "⊗"
	default:
		return "○"
	}
}

func max(a, b int) int {
	if a > b { return a }
	return b
}
