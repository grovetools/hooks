package utils

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// TruncateStr truncates a string to maxLen, adding "..." if truncated
func TruncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// PadStr pads a string to a fixed width, accounting for ANSI color codes
func PadStr(s string, width int) string {
	// Use lipgloss to handle ANSI codes properly when measuring width
	visibleLen := lipgloss.Width(s)
	if visibleLen >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visibleLen)
}

// FormatDuration formats a duration in a human-readable format
func FormatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		// For hours, omit seconds
		return fmt.Sprintf("%dh%dm", h, m)
	} else if m >= 10 {
		// For 10+ minutes, omit seconds
		return fmt.Sprintf("%dm", m)
	} else if m > 0 {
		// For under 10 minutes, show seconds
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
