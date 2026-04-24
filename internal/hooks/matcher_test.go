package hooks

import "testing"

func TestEvaluatePermissionRule_Bash(t *testing.T) {
	tests := []struct {
		name     string
		rule     string
		toolName string
		input    map[string]any
		want     bool
	}{
		{"git commit matches glob", "Bash(git commit *)", "Bash", map[string]any{"command": "git commit -m foo"}, true},
		{"git commit matches inside &&", "Bash(git commit *)", "Bash", map[string]any{"command": "git add x && git commit -m foo"}, true},
		{"git push not matched", "Bash(git commit *)", "Bash", map[string]any{"command": "git push"}, false},
		{"tool name mismatch", "Bash(git commit *)", "Edit", map[string]any{"command": "git commit -m foo"}, false},
		{"missing command", "Bash(git commit *)", "Bash", map[string]any{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := evaluatePermissionRule(tc.rule, tc.toolName, tc.input); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestEvaluatePermissionRule_FileTools(t *testing.T) {
	tests := []struct {
		name     string
		rule     string
		toolName string
		input    map[string]any
		want     bool
	}{
		{"go file matches", "Edit(*.go)", "Edit", map[string]any{"file_path": "/abs/path/foo.go"}, true},
		{"md file rejected", "Edit(*.go)", "Edit", map[string]any{"file_path": "foo.md"}, false},
		{"write tool", "Write(*.txt)", "Write", map[string]any{"file_path": "notes.txt"}, true},
		{"missing file_path", "Edit(*.go)", "Edit", map[string]any{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := evaluatePermissionRule(tc.rule, tc.toolName, tc.input); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestEvaluatePermissionRule_Malformed(t *testing.T) {
	cases := []string{"", "Bash", "Bash(", "(foo)", "   "}
	for _, rule := range cases {
		if evaluatePermissionRule(rule, "Bash", map[string]any{"command": "ls"}) {
			t.Fatalf("rule %q should not match", rule)
		}
	}
}
