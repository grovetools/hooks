package commands

import (
	"encoding/json"
	"strings"
	"testing"
)

const groveNotifyLine = `notify = ["grove", "hooks", "codex", "notify"]`

func TestUpsertCodexNotify_EmptyConfig(t *testing.T) {
	updated, changed, prev := upsertCodexNotify("")
	if !changed {
		t.Fatal("expected change on empty config")
	}
	if prev != "" {
		t.Errorf("previous = %q, want empty", prev)
	}
	if strings.TrimSpace(updated) != groveNotifyLine {
		t.Errorf("updated = %q", updated)
	}
}

func TestUpsertCodexNotify_InsertsBeforeTables(t *testing.T) {
	content := "# my codex config\nmodel = \"gpt-5\"\n\n[mcp_servers.foo]\ncommand = \"foo\"\n"
	updated, changed, _ := upsertCodexNotify(content)
	if !changed {
		t.Fatal("expected change")
	}
	// notify must be top-level: inserted before any [table] header.
	notifyIdx := strings.Index(updated, "notify = ")
	tableIdx := strings.Index(updated, "[mcp_servers.foo]")
	if notifyIdx == -1 || tableIdx == -1 || notifyIdx > tableIdx {
		t.Errorf("notify not inserted at top level:\n%s", updated)
	}
	// Existing content must survive untouched.
	for _, want := range []string{"# my codex config", `model = "gpt-5"`, "[mcp_servers.foo]", `command = "foo"`} {
		if !strings.Contains(updated, want) {
			t.Errorf("lost existing content %q:\n%s", want, updated)
		}
	}
}

func TestUpsertCodexNotify_ReplacesExisting(t *testing.T) {
	content := "model = \"gpt-5\"\nnotify = [\"notify-send\", \"Codex\"]\n"
	updated, changed, prev := upsertCodexNotify(content)
	if !changed {
		t.Fatal("expected change")
	}
	if prev != `notify = ["notify-send", "Codex"]` {
		t.Errorf("previous = %q", prev)
	}
	if !strings.Contains(updated, groveNotifyLine) {
		t.Errorf("grove notify line missing:\n%s", updated)
	}
	if strings.Contains(updated, "notify-send") {
		t.Errorf("old notify value not replaced:\n%s", updated)
	}
}

func TestUpsertCodexNotify_Idempotent(t *testing.T) {
	first, _, _ := upsertCodexNotify("model = \"gpt-5\"\n")
	second, changed, _ := upsertCodexNotify(first)
	if changed {
		t.Errorf("second upsert should be a no-op, got:\n%s", second)
	}
}

func TestUpsertCodexNotify_IgnoresNotifyInsideTable(t *testing.T) {
	// A notify key inside a table is NOT the top-level notify setting.
	content := "[some_table]\nnotify = [\"other\"]\n"
	updated, changed, prev := upsertCodexNotify(content)
	if !changed {
		t.Fatal("expected change (top-level notify missing)")
	}
	if prev != "" {
		t.Errorf("previous = %q, want empty (table-scoped notify is not top-level)", prev)
	}
	if !strings.Contains(updated, "[some_table]\nnotify = [\"other\"]") {
		t.Errorf("table-scoped notify mangled:\n%s", updated)
	}
	if !strings.HasPrefix(updated, groveNotifyLine) {
		t.Errorf("top-level notify not inserted first:\n%s", updated)
	}
}

// codexTurnCompletePayload is the wire shape verified against
// codex-rs/hooks/src/legacy_notify.rs.
const codexTurnCompletePayload = `{"type":"agent-turn-complete","thread-id":"b5f6c1c2-1111-2222-3333-444455556666","turn-id":"12345","cwd":"/Users/example/project","client":"codex-tui","input-messages":["do the thing"],"last-assistant-message":"done"}`

func TestBuildCodexStopInput_FlowJob(t *testing.T) {
	raw, ok := buildCodexStopInput([]string{codexTurnCompletePayload}, "job-42")
	if !ok {
		t.Fatal("expected stop input")
	}
	var stop map[string]any
	if err := json.Unmarshal(raw, &stop); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if stop["session_id"] != "job-42" {
		t.Errorf("session_id = %v, want flow job id", stop["session_id"])
	}
	if stop["exit_reason"] != "" {
		t.Errorf("exit_reason = %v, want empty (end of turn = idle)", stop["exit_reason"])
	}
	if stop["hook_event_name"] != "stop" {
		t.Errorf("hook_event_name = %v", stop["hook_event_name"])
	}
	if stop["cwd"] != "/Users/example/project" {
		t.Errorf("cwd = %v", stop["cwd"])
	}
}

func TestBuildCodexStopInput_ManualSessionUsesThreadID(t *testing.T) {
	raw, ok := buildCodexStopInput([]string{codexTurnCompletePayload}, "")
	if !ok {
		t.Fatal("expected stop input")
	}
	var stop map[string]any
	if err := json.Unmarshal(raw, &stop); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if stop["session_id"] != "b5f6c1c2-1111-2222-3333-444455556666" {
		t.Errorf("session_id = %v, want codex thread id", stop["session_id"])
	}
}

func TestBuildCodexStopInput_PayloadIsLastArg(t *testing.T) {
	// codex appends the payload after any configured argv tokens.
	if _, ok := buildCodexStopInput([]string{"extra", codexTurnCompletePayload}, "job-1"); !ok {
		t.Error("payload as last of several args should parse")
	}
}

func TestBuildCodexStopInput_Rejects(t *testing.T) {
	cases := map[string][]string{
		"no args":         {},
		"not json":        {"nonsense"},
		"other event":     {`{"type":"something-else"}`},
		"no id available": {`{"type":"agent-turn-complete","cwd":"/x"}`},
	}
	for name, args := range cases {
		if _, ok := buildCodexStopInput(args, ""); ok {
			t.Errorf("%s: expected rejection", name)
		}
	}
}
