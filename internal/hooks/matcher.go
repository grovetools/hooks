package hooks

import (
	"path/filepath"
	"strings"
)

// evaluatePermissionRule reports whether a Claude Code permission-rule string
// (e.g. "Bash(git commit *)" or "Edit(*.go)") matches a given tool call.
//
// Supported tool names: Bash, Edit, Write, Read, MultiEdit. For Bash the glob
// is matched against each subcommand of tool_input.command split on '&&', ';',
// '|'. For file tools the glob is matched against tool_input.file_path.
//
// Empty rules, malformed rules, mismatched tool names, and unsupported tools
// all return false.
func evaluatePermissionRule(rule, toolName string, toolInput map[string]any) bool {
	ruleTool, glob, ok := parsePermissionRule(rule)
	if !ok {
		return false
	}
	if ruleTool != toolName {
		return false
	}

	switch toolName {
	case "Bash":
		cmd, _ := toolInput["command"].(string)
		if cmd == "" {
			return false
		}
		for _, sub := range splitShellCommand(cmd) {
			sub = strings.TrimSpace(sub)
			if sub == "" {
				continue
			}
			if matched, err := filepath.Match(glob, sub); err == nil && matched {
				return true
			}
		}
		return false
	case "Edit", "Write", "Read", "MultiEdit":
		path, _ := toolInput["file_path"].(string)
		if path == "" {
			return false
		}
		if matched, err := filepath.Match(glob, path); err == nil && matched {
			return true
		}
		// Fall back to matching against the basename when the glob has no
		// path separator — this lets "Edit(*.go)" match "/abs/path/foo.go"
		// the way Claude Code's permission rules intuitively work.
		if !strings.ContainsRune(glob, '/') {
			if matched, err := filepath.Match(glob, filepath.Base(path)); err == nil && matched {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// parsePermissionRule splits a rule like "Bash(git commit *)" into ("Bash",
// "git commit *", true). Returns ok=false for empty or malformed rules.
func parsePermissionRule(rule string) (toolName, glob string, ok bool) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return "", "", false
	}
	open := strings.IndexByte(rule, '(')
	if open <= 0 || !strings.HasSuffix(rule, ")") {
		return "", "", false
	}
	return rule[:open], rule[open+1 : len(rule)-1], true
}

// splitShellCommand splits a shell command string on the operators &&, ||, ;,
// and | (single-pipe), returning each subcommand. This is intentionally naive
// — it does not respect quoting or escapes — and follows Claude Code's
// "if any subcommand matches" rule for permission filtering.
func splitShellCommand(cmd string) []string {
	parts := []string{cmd}
	for _, sep := range []string{"&&", "||", ";", "|"} {
		var next []string
		for _, p := range parts {
			next = append(next, strings.Split(p, sep)...)
		}
		parts = next
	}
	return parts
}
