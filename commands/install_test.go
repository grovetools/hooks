package commands

import (
	"testing"
)

// entryCommands collects the hook command strings from a merged entry list.
func entryCommands(t *testing.T, entries []interface{}) []string {
	t.Helper()
	var cmds []string
	for _, item := range entries {
		entryMap, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("entry is not a map: %T", item)
		}
		hooksList, ok := entryMap["hooks"].([]interface{})
		if !ok {
			// HookEntry-converted entries use []map[string]interface{}.
			typed, ok2 := entryMap["hooks"].([]map[string]interface{})
			if !ok2 {
				t.Fatalf("hooks is not a list: %T", entryMap["hooks"])
			}
			for _, h := range typed {
				if cmd, ok := h["command"].(string); ok {
					cmds = append(cmds, cmd)
				}
			}
			continue
		}
		for _, h := range hooksList {
			if hookMap, ok := h.(map[string]interface{}); ok {
				if cmd, ok := hookMap["command"].(string); ok {
					cmds = append(cmds, cmd)
				}
			}
		}
	}
	return cmds
}

func entryMatchers(t *testing.T, entries []interface{}) []string {
	t.Helper()
	var matchers []string
	for _, item := range entries {
		entryMap, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("entry is not a map: %T", item)
		}
		if m, ok := entryMap["matcher"].(string); ok {
			matchers = append(matchers, m)
		}
	}
	return matchers
}

func TestGroveHooksConfigRegistrations(t *testing.T) {
	cfg := groveHooksConfig()

	// New lifecycle events are registered with command-type hooks.
	for event, wantCmd := range map[string]string{
		"SessionStart":  "grove hooks session-start",
		"SubagentStart": "grove hooks subagent-start",
		"SubagentStop":  "grove hooks subagent-stop",
	} {
		entries, ok := cfg[event]
		if !ok {
			t.Fatalf("%s not registered", event)
		}
		if len(entries) != 1 || len(entries[0].Hooks) != 1 {
			t.Fatalf("%s: expected exactly one entry with one hook, got %+v", event, entries)
		}
		h := entries[0].Hooks[0]
		if h.Type != "command" {
			t.Errorf("%s hook type = %q, want command", event, h.Type)
		}
		if h.Command != wantCmd {
			t.Errorf("%s command = %q, want %q", event, h.Command, wantCmd)
		}
		if entries[0].Matcher != ".*" {
			t.Errorf("%s matcher = %q, want .*", event, entries[0].Matcher)
		}
	}

	// PostToolUse matcher must include ExitPlanMode (plan preservation) and
	// Agent (spawn-prompt/cost capture).
	post := cfg["PostToolUse"]
	if len(post) != 1 {
		t.Fatalf("PostToolUse: expected one entry, got %d", len(post))
	}
	if got, want := post[0].Matcher, "(Edit|Write|MultiEdit|Bash|Read|ExitPlanMode|Agent)"; got != want {
		t.Errorf("PostToolUse matcher = %q, want %q", got, want)
	}
}

func TestMergeHooksUpgradesGroveEntriesAndPreservesUserHooks(t *testing.T) {
	// Simulate a settings file from an older grove install (stale PostToolUse
	// matcher, no SubagentStart/SessionStart) plus a user-defined custom hook.
	settings := ClaudeSettings{
		"hooks": map[string]interface{}{
			"PostToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "(Edit|Write|MultiEdit|Bash|Read)",
					"hooks": []interface{}{
						map[string]interface{}{"type": "command", "command": "grove hooks posttooluse"},
					},
				},
				map[string]interface{}{
					"matcher": "Bash",
					"hooks": []interface{}{
						map[string]interface{}{"type": "command", "command": "/usr/local/bin/my-custom-audit"},
					},
				},
			},
			"SubagentStop": []interface{}{
				map[string]interface{}{
					"matcher": ".*",
					"hooks": []interface{}{
						map[string]interface{}{"type": "command", "command": "grove-hooks subagent-stop"},
					},
				},
			},
		},
	}

	mergeHooks(settings, groveHooksConfig())

	hooksMap := settings["hooks"].(map[string]interface{})

	// PostToolUse: user hook preserved, grove hook present exactly once with
	// the upgraded matcher.
	post := hooksMap["PostToolUse"].([]interface{})
	cmds := entryCommands(t, post)
	groveCount, userCount := 0, 0
	for _, c := range cmds {
		switch c {
		case "grove hooks posttooluse":
			groveCount++
		case "/usr/local/bin/my-custom-audit":
			userCount++
		}
	}
	if groveCount != 1 {
		t.Errorf("expected exactly one grove posttooluse hook after merge, got %d (cmds: %v)", groveCount, cmds)
	}
	if userCount != 1 {
		t.Errorf("user custom hook was not preserved (cmds: %v)", cmds)
	}
	matchers := entryMatchers(t, post)
	foundUpgraded := false
	for _, m := range matchers {
		if m == "(Edit|Write|MultiEdit|Bash|Read|ExitPlanMode|Agent)" {
			foundUpgraded = true
		}
		if m == "(Edit|Write|MultiEdit|Bash|Read)" {
			t.Errorf("stale grove matcher survived the merge: %v", matchers)
		}
	}
	if !foundUpgraded {
		t.Errorf("upgraded PostToolUse matcher missing: %v", matchers)
	}

	// Old-binary-name grove SubagentStop entry replaced, not duplicated.
	stop := hooksMap["SubagentStop"].([]interface{})
	stopCmds := entryCommands(t, stop)
	if len(stopCmds) != 1 || stopCmds[0] != "grove hooks subagent-stop" {
		t.Errorf("SubagentStop merge: got %v, want exactly [grove hooks subagent-stop]", stopCmds)
	}

	// Brand-new event types are added on upgrade.
	for _, event := range []string{"SessionStart", "SubagentStart"} {
		entries, ok := hooksMap[event].([]interface{})
		if !ok || len(entries) == 0 {
			t.Errorf("%s missing after merge into existing settings", event)
		}
	}
}

func TestMergeHooksIntoEmptySettings(t *testing.T) {
	settings := make(ClaudeSettings)
	mergeHooks(settings, groveHooksConfig())

	hooksMap, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		t.Fatal("hooks map not created")
	}
	for _, event := range []string{
		"PreToolUse", "PostToolUse", "SessionStart", "Notification",
		"Stop", "SubagentStart", "SubagentStop",
	} {
		entries, ok := hooksMap[event].([]interface{})
		if !ok || len(entries) == 0 {
			t.Errorf("%s missing from fresh install", event)
		}
	}
}
