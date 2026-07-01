package hooks

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func strptr(s string) *string { return &s }

func TestBuildPreCommandEntry(t *testing.T) {
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)

	t.Run("bash attempt is recorded as pending", func(t *testing.T) {
		entry, ok := buildPreCommandEntry("Bash", map[string]any{
			"command": "go build ./... && go test ./...",
		}, "link-123", "/repo", now)
		if !ok {
			t.Fatal("expected ok for Bash input")
		}
		if entry.Phase != cmdPhasePre {
			t.Errorf("phase = %q, want %q", entry.Phase, cmdPhasePre)
		}
		if entry.Outcome != cmdOutcomePending {
			t.Errorf("outcome = %q, want %q", entry.Outcome, cmdOutcomePending)
		}
		if entry.LinkID != "link-123" {
			t.Errorf("link_id = %q, want link-123", entry.LinkID)
		}
		if entry.ToolUseID != "" {
			t.Errorf("pre row must not carry a payload tool_use_id, got %q", entry.ToolUseID)
		}
		if entry.Cwd != "/repo" {
			t.Errorf("cwd = %q, want /repo", entry.Cwd)
		}
		if entry.Command != "go build ./... && go test ./..." {
			t.Errorf("command = %q", entry.Command)
		}
		want := []string{"go build ./...", "go test ./..."}
		if !reflect.DeepEqual(entry.Subcommands, want) {
			t.Errorf("subcommands = %v, want %v", entry.Subcommands, want)
		}
		if entry.Timestamp != "2026-06-23T10:00:00Z" {
			t.Errorf("timestamp = %q", entry.Timestamp)
		}
		if entry.DurationMs != 0 {
			t.Errorf("duration should be unset on pre row, got %d", entry.DurationMs)
		}
	})

	t.Run("non-bash tool is skipped", func(t *testing.T) {
		if _, ok := buildPreCommandEntry("Edit", map[string]any{"file_path": "/a.go"}, "x", "/repo", now); ok {
			t.Error("expected non-Bash tool to be skipped")
		}
	})

	t.Run("empty command is skipped", func(t *testing.T) {
		if _, ok := buildPreCommandEntry("Bash", map[string]any{"command": ""}, "x", "/repo", now); ok {
			t.Error("expected empty command to be skipped")
		}
		if _, ok := buildPreCommandEntry("Bash", map[string]any{}, "x", "/repo", now); ok {
			t.Error("expected missing command to be skipped")
		}
	})
}

func TestBuildPostCommandEntry(t *testing.T) {
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)

	t.Run("success becomes ran_ok", func(t *testing.T) {
		entry, ok := buildPostCommandEntry(PostToolUseInput{
			ToolName:       "Bash",
			ToolInput:      map[string]any{"command": "ls -la"},
			ToolUseID:      "toolu_9",
			ToolDurationMs: 42,
			Cwd:            "/repo",
		}, "link-9", now)
		if !ok {
			t.Fatal("expected ok")
		}
		if entry.Phase != cmdPhasePost {
			t.Errorf("phase = %q, want %q", entry.Phase, cmdPhasePost)
		}
		if entry.Outcome != cmdOutcomeRanOK {
			t.Errorf("outcome = %q, want %q", entry.Outcome, cmdOutcomeRanOK)
		}
		if entry.DurationMs != 42 {
			t.Errorf("duration_ms = %d, want 42", entry.DurationMs)
		}
		if entry.LinkID != "link-9" {
			t.Errorf("link_id = %q, want link-9", entry.LinkID)
		}
		if entry.ToolUseID != "toolu_9" {
			t.Errorf("tool_use_id = %q, want toolu_9 (informational)", entry.ToolUseID)
		}
	})

	t.Run("tool_error becomes ran_error", func(t *testing.T) {
		entry, ok := buildPostCommandEntry(PostToolUseInput{
			ToolName:  "Bash",
			ToolInput: map[string]any{"command": "false"},
			ToolError: strptr("exit status 1"),
		}, "link-err", now)
		if !ok {
			t.Fatal("expected ok")
		}
		if entry.Outcome != cmdOutcomeRanError {
			t.Errorf("outcome = %q, want %q", entry.Outcome, cmdOutcomeRanError)
		}
	})

	t.Run("sandbox write-denial in tool_response becomes sandbox_denied", func(t *testing.T) {
		// A denial can leave the command exit 0 (tool_error nil): the EPERM text
		// lives only in tool_response stdout/stderr. It must still be classified.
		entry, ok := buildPostCommandEntry(PostToolUseInput{
			ToolName:  "Bash",
			ToolInput: map[string]any{"command": "touch out/file"},
			ToolResponse: map[string]any{
				"stdout":      "touch: out/file: operation not permitted\n",
				"stderr":      "",
				"interrupted": false,
			},
		}, "link-sbx", now)
		if !ok {
			t.Fatal("expected ok")
		}
		if entry.Outcome != cmdOutcomeSandboxDenied {
			t.Errorf("outcome = %q, want %q", entry.Outcome, cmdOutcomeSandboxDenied)
		}
	})

	t.Run("sandbox denial wins over a non-zero tool_error", func(t *testing.T) {
		// When the denial also flips the command to a non-zero exit, sandbox_denied
		// must still win the classification over the generic ran_error.
		entry, ok := buildPostCommandEntry(PostToolUseInput{
			ToolName:     "Bash",
			ToolInput:    map[string]any{"command": "cp a.out bin/hooks"},
			ToolResponse: map[string]any{"stderr": "cp: bin/hooks: Operation not permitted"},
			ToolError:    strptr("exit status 1"),
		}, "link-sbx-err", now)
		if !ok {
			t.Fatal("expected ok")
		}
		if entry.Outcome != cmdOutcomeSandboxDenied {
			t.Errorf("outcome = %q, want %q (sandbox beats ran_error)", entry.Outcome, cmdOutcomeSandboxDenied)
		}
	})

	t.Run("ordinary error text does not trip the sandbox detector", func(t *testing.T) {
		entry, ok := buildPostCommandEntry(PostToolUseInput{
			ToolName:     "Bash",
			ToolInput:    map[string]any{"command": "go build ./..."},
			ToolResponse: map[string]any{"stderr": "some_pkg.go:10: undefined: Foo"},
			ToolError:    strptr("exit status 2"),
		}, "link-plain-err", now)
		if !ok {
			t.Fatal("expected ok")
		}
		if entry.Outcome != cmdOutcomeRanError {
			t.Errorf("outcome = %q, want %q", entry.Outcome, cmdOutcomeRanError)
		}
	})

	t.Run("non-bash and empty command skipped", func(t *testing.T) {
		if _, ok := buildPostCommandEntry(PostToolUseInput{ToolName: "Read", ToolInput: map[string]any{"file_path": "/a"}}, "", now); ok {
			t.Error("expected non-Bash skipped")
		}
		if _, ok := buildPostCommandEntry(PostToolUseInput{ToolName: "Bash", ToolInput: map[string]any{"command": ""}}, "", now); ok {
			t.Error("expected empty command skipped")
		}
	})

	t.Run("tool_input decoded from json any matches", func(t *testing.T) {
		// PostToolUseInput.ToolInput is `any`; mimic JSON decoding.
		var data PostToolUseInput
		raw := `{"tool_name":"Bash","tool_input":{"command":"echo hi"},"tool_use_id":"t1"}`
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			t.Fatal(err)
		}
		entry, ok := buildPostCommandEntry(data, "link-t1", now)
		if !ok {
			t.Fatal("expected ok after json decode")
		}
		if entry.Command != "echo hi" {
			t.Errorf("command = %q", entry.Command)
		}
	})
}

func TestCommandLinkIDBridge(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const sessionID = "sess-link"

	// Empty before anything is stored.
	if got := getCommandLinkID(sessionID); got != "" {
		t.Errorf("expected empty link id, got %q", got)
	}

	// newCommandLinkID is session-scoped and unique per call.
	a := newCommandLinkID(sessionID)
	b := newCommandLinkID(sessionID)
	if a == b {
		t.Errorf("expected distinct link ids, got %q twice", a)
	}

	// store → get returns the same id (the pre→post bridge).
	storeCommandLinkID(sessionID, a)
	if got := getCommandLinkID(sessionID); got != a {
		t.Errorf("get = %q, want %q", got, a)
	}

	// clear removes it.
	clearCommandLinkID(sessionID)
	if got := getCommandLinkID(sessionID); got != "" {
		t.Errorf("expected empty after clear, got %q", got)
	}
}

func TestCommandSubcommands(t *testing.T) {
	got := commandSubcommands("a && b ; c | d || e")
	want := []string{"a", "b", "c", "d", "e"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if subs := commandSubcommands("single"); !reflect.DeepEqual(subs, []string{"single"}) {
		t.Errorf("single command = %v", subs)
	}
}

// setupJobArtifacts wires GROVE_HOME to a temp dir and writes the session
// metadata.json so resolveFileAccessTarget resolves the plan/job artifacts dir.
// It returns the expected commands.jsonl path.
func setupJobArtifacts(t *testing.T, sessionID, jobName string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)

	planDir := filepath.Join(home, "plan")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jobFilePath := filepath.Join(planDir, "job.md")

	sessDir := filepath.Join(home, "state", "grove", "hooks", "sessions", sessionID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{"session_id": jobName, "job_file_path": jobFilePath}
	b, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(sessDir, "metadata.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	return filepath.Join(planDir, ".artifacts", jobName, "commands.jsonl")
}

func readEntries(t *testing.T, path string) []commandEntry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open commands.jsonl: %v", err)
	}
	defer f.Close()
	var entries []commandEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e commandEntry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", line, err)
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestAppendCommandEntries_WritesPreAndPostRows(t *testing.T) {
	const sessionID = "sess-abc"
	const jobName = "job-xyz"
	jsonlPath := setupJobArtifacts(t, sessionID, jobName)

	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	const linkID = "lnk-1"
	pre, _ := buildPreCommandEntry("Bash", map[string]any{"command": "go test ./..."}, linkID, "/repo", now)
	post, _ := buildPostCommandEntry(PostToolUseInput{
		ToolName: "Bash", ToolInput: map[string]any{"command": "go test ./..."},
		ToolUseID: "t1", ToolDurationMs: 100,
	}, linkID, now)

	appendCommandEntries(sessionID, []commandEntry{pre})
	appendCommandEntries(sessionID, []commandEntry{post})

	entries := readEntries(t, jsonlPath)
	if len(entries) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(entries))
	}
	if entries[0].Phase != cmdPhasePre || entries[0].Outcome != cmdOutcomePending {
		t.Errorf("row 0 = %+v", entries[0])
	}
	if entries[1].Phase != cmdPhasePost || entries[1].Outcome != cmdOutcomeRanOK {
		t.Errorf("row 1 = %+v", entries[1])
	}
	// pre and post must share the recorder-generated link_id (NOT the payload
	// tool_use_id, which is absent at PreToolUse).
	if entries[0].LinkID == "" || entries[0].LinkID != entries[1].LinkID {
		t.Errorf("link_id mismatch: %q vs %q", entries[0].LinkID, entries[1].LinkID)
	}
}

func TestAppendCommandEntries_NoTargetIsNoOp(t *testing.T) {
	t.Setenv("GROVE_HOME", t.TempDir())
	t.Setenv("PWD", "/nonexistent")
	// No metadata.json and no resolvable plan dir → silent no-op, no panic.
	appendCommandEntries("unknown-session", []commandEntry{{Command: "x", Outcome: cmdOutcomePending}})
}

func TestAppendCommandEntries_ConcurrentAppendsAreLineAtomic(t *testing.T) {
	const sessionID = "sess-conc"
	const jobName = "job-conc"
	jsonlPath := setupJobArtifacts(t, sessionID, jobName)

	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	const writers = 16
	const perWriter = 25

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				entry, _ := buildPostCommandEntry(PostToolUseInput{
					ToolName:  "Bash",
					ToolInput: map[string]any{"command": "echo concurrent && true"},
					ToolUseID: "w" + string(rune('a'+w)),
				}, "lnk-"+string(rune('a'+w)), now)
				appendCommandEntries(sessionID, []commandEntry{entry})
			}
		}(w)
	}
	wg.Wait()

	// Every line must be valid JSON (no interleaving/torn writes) and the count
	// must match exactly.
	entries := readEntries(t, jsonlPath)
	if len(entries) != writers*perWriter {
		t.Fatalf("expected %d rows, got %d", writers*perWriter, len(entries))
	}
	for _, e := range entries {
		if e.Command != "echo concurrent && true" {
			t.Fatalf("corrupted row: %+v", e)
		}
	}
}
