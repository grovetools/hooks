package hooks

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/grovetools/core/pkg/models"
)

func TestShouldForwardSubagentStart(t *testing.T) {
	tests := []struct {
		name string
		data SubagentStartInput
		want bool
	}{
		{
			// Real Agent/Task spawn: a + 16 hex (writes a transcript).
			name: "real spawn full id kept",
			data: SubagentStartInput{AgentID: "a62124203bfeb94f0", AgentType: "general-purpose"},
			want: true,
		},
		{
			// Phantom Explore type-registration: short a + 6 hex, no transcript.
			name: "phantom explore short id dropped",
			data: SubagentStartInput{AgentID: "a03e225", AgentType: "Explore"},
			want: false,
		},
		{
			name: "phantom plan short id dropped",
			data: SubagentStartInput{AgentID: "ac81b9b", AgentType: "Plan"},
			want: false,
		},
		{
			name: "empty agent id dropped",
			data: SubagentStartInput{AgentType: "Explore"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldForwardSubagentStart(tt.data); got != tt.want {
				t.Errorf("shouldForwardSubagentStart(%+v) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}

func TestShouldForwardSubagentStop(t *testing.T) {
	wfRunPath := "/home/u/.claude/projects/-x/sess/subagents/workflows/wf_008adc8e-9d1/agent-a861a01693271e1b7.jsonl"
	adhocPath := "/home/u/.claude/projects/-x/sess/subagents/agent-a912bb8b2d1810bac.jsonl"
	wfBackgroundTasks := []map[string]any{
		{"id": "wy8f2duwh", "name": "test-workflow", "type": "workflow", "status": "running"},
	}
	tests := []struct {
		name string
		data SubagentStopInput
		want bool
	}{
		{
			// The bug: main session's workflow-wait turn boundary — empty
			// agent_type + a background workflow task + a transcript that was
			// never written. Must be dropped (no phantom ad-hoc row).
			name: "phantom workflow-wait stop dropped",
			data: SubagentStopInput{AgentID: "a912bb8b2d1810bac", AgentType: "", AgentTranscriptPath: &adhocPath, BackgroundTasks: wfBackgroundTasks},
			want: false,
		},
		{
			// Real workflow subagent: transcript path embeds wf_<runId>, so it
			// attributes to its run. Kept even though a workflow runs.
			name: "real workflow subagent kept",
			data: SubagentStopInput{AgentID: "a861a01693271e1b7", AgentType: "workflow-subagent", AgentTranscriptPath: &wfRunPath, BackgroundTasks: wfBackgroundTasks},
			want: true,
		},
		{
			// Real ad-hoc Agent/Task spawn: non-empty agent_type, no background
			// workflow task. Kept.
			name: "real adhoc agent spawn kept",
			data: SubagentStopInput{AgentID: "a62124203bfeb94f0", AgentType: "Explore", AgentTranscriptPath: &adhocPath},
			want: true,
		},
		{
			// Plain stop with no enrichment fields at all: kept (no signal it
			// is a phantom).
			name: "minimal payload kept",
			data: SubagentStopInput{AgentID: "a62124203bfeb94f0"},
			want: true,
		},
		{
			// Empty agent_type but NO background workflow task: not the
			// workflow-wait shape, so kept.
			name: "empty type without workflow task kept",
			data: SubagentStopInput{AgentID: "a62124203bfeb94f0", AgentType: "", AgentTranscriptPath: &adhocPath},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldForwardSubagentStop(tt.data); got != tt.want {
				t.Errorf("shouldForwardSubagentStop(%+v) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}

func TestExtractWorkflowRunID(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			// Probe-confirmed full shape: session subdir under the project slug.
			name: "workflow transcript under session subdir",
			path: "/home/u/.claude/projects/-Users-x-repo/6c1e876f/subagents/workflows/wf_d2a7bbf5-710/agent-ab12cd3.jsonl",
			want: "wf_d2a7bbf5-710",
		},
		{
			// Minimal variant: no session subdir, workflow dir directly under
			// the slug dir.
			name: "workflow transcript directly under slug dir",
			path: "/home/u/.claude/projects/-Users-x-repo/subagents/workflows/wf_9f0e/agent-ad48c96.jsonl",
			want: "wf_9f0e",
		},
		{
			// Ad-hoc Agent-tool spawn: no workflows component.
			name: "ad-hoc agent transcript under subagents",
			path: "/home/u/.claude/projects/-Users-x-repo/6c1e876f/subagents/agent-ab12cd3.jsonl",
			want: "",
		},
		{
			// Probe-observed minimal variant: transcript directly under the
			// slug dir with no subagents/workflows components at all.
			name: "agent transcript directly under slug dir",
			path: "/home/u/.claude/projects/-Users-x-repo/agent-ad48c96.jsonl",
			want: "",
		},
		{
			name: "empty path",
			path: "",
			want: "",
		},
		{
			// wf_ dir must be nested under subagents/workflows to count.
			name: "wf prefix outside workflows dir",
			path: "/home/u/wf_decoy/agent-1.jsonl",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractWorkflowRunID(tt.path); got != tt.want {
				t.Errorf("extractWorkflowRunID(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestExtractWorkflowName(t *testing.T) {
	tests := []struct {
		name  string
		tasks []map[string]any
		want  string
	}{
		{
			name: "workflow task present",
			tasks: []map[string]any{
				{"id": "wfsk0ocla", "type": "workflow", "status": "running", "description": "d", "name": "p0-hook-probe"},
			},
			want: "p0-hook-probe",
		},
		{
			name: "non-workflow tasks skipped",
			tasks: []map[string]any{
				{"id": "x", "type": "cron", "name": "nightly"},
				{"id": "y", "type": "workflow", "name": "real-workflow"},
			},
			want: "real-workflow",
		},
		{
			name:  "empty list",
			tasks: nil,
			want:  "",
		},
		{
			name: "workflow task without name",
			tasks: []map[string]any{
				{"id": "wfsk0ocla", "type": "workflow"},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractWorkflowName(tt.tasks); got != tt.want {
				t.Errorf("extractWorkflowName(...) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCountLiveChildren(t *testing.T) {
	tests := []struct {
		name  string
		tasks []map[string]any
		crons []map[string]any
		want  int
	}{
		{
			name:  "nil lists",
			tasks: nil,
			crons: nil,
			want:  0,
		},
		{
			name:  "status absent counted",
			tasks: []map[string]any{{"id": "a", "type": "workflow"}},
			want:  1,
		},
		{
			name:  "running counted",
			tasks: []map[string]any{{"id": "a", "type": "workflow", "status": "running"}},
			want:  1,
		},
		{
			name: "terminal statuses excluded",
			tasks: []map[string]any{
				{"id": "a", "status": "completed"},
				{"id": "b", "status": "failed"},
				{"id": "c", "status": "errored"},
			},
			want: 0,
		},
		{
			name:  "non-string status counted",
			tasks: []map[string]any{{"id": "a", "status": 42}},
			want:  1,
		},
		{
			name: "mixed live and terminal",
			tasks: []map[string]any{
				{"id": "a", "status": "running"},
				{"id": "b", "status": "completed"},
				{"id": "c"},
			},
			want: 2,
		},
		{
			name:  "cron deduped by shared id",
			tasks: []map[string]any{{"id": "shared", "type": "workflow", "status": "running"}},
			crons: []map[string]any{{"id": "shared", "type": "cron", "status": "running"}},
			want:  1,
		},
		{
			name:  "cron with distinct live id counted",
			tasks: []map[string]any{{"id": "task", "type": "workflow", "status": "running"}},
			crons: []map[string]any{{"id": "cronjob", "type": "cron", "status": "running"}},
			want:  2,
		},
		{
			name:  "terminal cron excluded",
			crons: []map[string]any{{"id": "cronjob", "type": "cron", "status": "completed"}},
			want:  0,
		},
		{
			name:  "live cron with no id counted",
			crons: []map[string]any{{"type": "cron", "status": "running"}},
			want:  1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countLiveChildren(tt.tasks, tt.crons); got != tt.want {
				t.Errorf("countLiveChildren(%v, %v) = %d, want %d", tt.tasks, tt.crons, got, tt.want)
			}
		})
	}
}

func TestWorkflowChildrenSnapshotEvent(t *testing.T) {
	now := time.Date(2026, 7, 8, 11, 0, 0, 0, time.UTC)
	tasks := []map[string]any{
		{"id": "a", "type": "workflow", "status": "running"},
		{"id": "b", "type": "workflow", "status": "completed"},
	}
	crons := []map[string]any{
		{"id": "c", "type": "cron", "status": "running"},
	}

	t.Run("with job id", func(t *testing.T) {
		t.Setenv("GROVE_FLOW_JOB_ID", "job-42")
		ev := workflowChildrenSnapshotEvent(SubagentStopInput{
			SessionID:       "sess-1",
			BackgroundTasks: tasks,
			SessionCrons:    crons,
		}, now)

		want := models.WorkflowEvent{
			Kind:            models.WorkflowChildrenSnapshot,
			JobID:           "job-42",
			ClaudeSessionID: "sess-1",
			LiveChildren:    2, // one live task + one live cron; terminal task excluded
			Timestamp:       now,
			Source:          models.WorkflowSourceHooks,
		}
		if !reflect.DeepEqual(ev, want) {
			t.Errorf("got %+v, want %+v", ev, want)
		}
		if ev.AgentID != "" {
			t.Errorf("AgentID = %q, want empty (snapshot is session-level)", ev.AgentID)
		}
	})

	t.Run("without job id env, empty lists yield zero snapshot", func(t *testing.T) {
		t.Setenv("GROVE_FLOW_JOB_ID", "")
		ev := workflowChildrenSnapshotEvent(SubagentStopInput{
			SessionID: "sess-2",
		}, now)

		if ev.JobID != "" {
			t.Errorf("JobID = %q, want empty", ev.JobID)
		}
		if ev.LiveChildren != 0 {
			t.Errorf("LiveChildren = %d, want 0", ev.LiveChildren)
		}
		if ev.Kind != models.WorkflowChildrenSnapshot || ev.ClaudeSessionID != "sess-2" ||
			ev.Source != models.WorkflowSourceHooks {
			t.Errorf("unexpected event: %+v", ev)
		}
	})
}

func TestWorkflowEventForwardable(t *testing.T) {
	tests := []struct {
		name string
		ev   models.WorkflowEvent
		want bool
	}{
		{
			name: "snapshot with empty agent id but session id → forwardable",
			ev:   models.WorkflowEvent{Kind: models.WorkflowChildrenSnapshot, ClaudeSessionID: "sess-1"},
			want: true,
		},
		{
			name: "snapshot with only job id → forwardable",
			ev:   models.WorkflowEvent{Kind: models.WorkflowChildrenSnapshot, JobID: "job-1"},
			want: true,
		},
		{
			name: "snapshot with neither job nor session id → not forwardable",
			ev:   models.WorkflowEvent{Kind: models.WorkflowChildrenSnapshot},
			want: false,
		},
		{
			name: "non-snapshot with empty agent id → not forwardable (today's behavior)",
			ev:   models.WorkflowEvent{Kind: models.WorkflowAgentCompleted, ClaudeSessionID: "sess-1"},
			want: false,
		},
		{
			name: "non-snapshot with agent id → forwardable",
			ev:   models.WorkflowEvent{Kind: models.WorkflowAgentStarted, AgentID: "a62124203bfeb94f0"},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workflowEventForwardable(tt.ev); got != tt.want {
				t.Errorf("workflowEventForwardable(%+v) = %v, want %v", tt.ev, got, tt.want)
			}
		})
	}
}

func TestWorkflowEventFromSubagentStart(t *testing.T) {
	now := time.Date(2026, 6, 10, 17, 7, 10, 0, time.UTC)

	t.Run("with job id and agent type", func(t *testing.T) {
		t.Setenv("GROVE_FLOW_JOB_ID", "job-42")
		ev := workflowEventFromSubagentStart(SubagentStartInput{
			SessionID: "sess-1",
			AgentID:   "a1",
			AgentType: "workflow-subagent",
		}, now)

		want := models.WorkflowEvent{
			Kind:            models.WorkflowAgentStarted,
			JobID:           "job-42",
			ClaudeSessionID: "sess-1",
			AgentID:         "a1",
			AgentType:       "workflow-subagent",
			Timestamp:       now,
			Source:          models.WorkflowSourceHooks,
		}
		if !reflect.DeepEqual(ev, want) {
			t.Errorf("got %+v, want %+v", ev, want)
		}
	})

	t.Run("without job id env or optional fields", func(t *testing.T) {
		t.Setenv("GROVE_FLOW_JOB_ID", "")
		ev := workflowEventFromSubagentStart(SubagentStartInput{
			SessionID: "sess-1",
			AgentID:   "a2",
		}, now)

		if ev.JobID != "" {
			t.Errorf("JobID = %q, want empty", ev.JobID)
		}
		if ev.AgentType != "" || ev.RunID != "" || ev.TranscriptPath != "" {
			t.Errorf("expected empty enrichment fields, got %+v", ev)
		}
		if ev.Kind != models.WorkflowAgentStarted || ev.Source != models.WorkflowSourceHooks {
			t.Errorf("kind/source wrong: %+v", ev)
		}
	})
}

func TestWorkflowEventFromSubagentStop(t *testing.T) {
	now := time.Date(2026, 6, 10, 17, 7, 14, 0, time.UTC)
	transcript := "/home/u/.claude/projects/slug/6c1e876f/subagents/workflows/wf_d2a7bbf5-710/agent-a1.jsonl"
	lastMsg := "done."

	t.Run("full variant attributes run id and enrichment", func(t *testing.T) {
		t.Setenv("GROVE_FLOW_JOB_ID", "job-42")
		ev := workflowEventFromSubagentStop(SubagentStopInput{
			SessionID:            "sess-1",
			AgentID:              "a1",
			AgentType:            "workflow-subagent",
			AgentTranscriptPath:  &transcript,
			LastAssistantMessage: &lastMsg,
		}, now)

		want := models.WorkflowEvent{
			Kind:            models.WorkflowAgentCompleted,
			JobID:           "job-42",
			ClaudeSessionID: "sess-1",
			RunID:           "wf_d2a7bbf5-710",
			AgentID:         "a1",
			AgentType:       "workflow-subagent",
			TranscriptPath:  transcript,
			LastMessage:     lastMsg,
			Timestamp:       now,
			Source:          models.WorkflowSourceHooks,
		}
		if !reflect.DeepEqual(ev, want) {
			t.Errorf("got %+v, want %+v", ev, want)
		}
	})

	t.Run("minimal variant leaves enrichment empty", func(t *testing.T) {
		t.Setenv("GROVE_FLOW_JOB_ID", "")
		adhoc := "/home/u/.claude/projects/slug/agent-ad48c96.jsonl"
		ev := workflowEventFromSubagentStop(SubagentStopInput{
			SessionID:           "sess-2",
			AgentID:             "ad48c96",
			AgentTranscriptPath: &adhoc,
		}, now)

		if ev.RunID != "" {
			t.Errorf("RunID = %q, want empty (ad-hoc spawn)", ev.RunID)
		}
		if ev.TranscriptPath != adhoc {
			t.Errorf("TranscriptPath = %q, want %q", ev.TranscriptPath, adhoc)
		}
		if ev.LastMessage != "" || ev.AgentType != "" || ev.JobID != "" {
			t.Errorf("expected empty optional fields, got %+v", ev)
		}
	})

	t.Run("nil transcript path", func(t *testing.T) {
		t.Setenv("GROVE_FLOW_JOB_ID", "")
		ev := workflowEventFromSubagentStop(SubagentStopInput{
			SessionID: "sess-3",
			AgentID:   "a3",
		}, now)
		if ev.RunID != "" || ev.TranscriptPath != "" {
			t.Errorf("expected empty RunID/TranscriptPath, got %+v", ev)
		}
	})

	t.Run("legacy subagent_id fallback", func(t *testing.T) {
		t.Setenv("GROVE_FLOW_JOB_ID", "")
		ev := workflowEventFromSubagentStop(SubagentStopInput{
			SessionID:  "sess-4",
			SubagentID: "legacy-7",
		}, now)
		if ev.AgentID != "legacy-7" {
			t.Errorf("AgentID = %q, want legacy-7", ev.AgentID)
		}
	})
}

func TestResolveAgentMetaPathFromTranscript(t *testing.T) {
	tests := []struct {
		name           string
		transcriptPath string
		wantSuffix     string
	}{
		{
			name:           "simple agent transcript",
			transcriptPath: "/home/u/.claude/projects/slug/6c1e876f/subagents/agent-ab12cd3.jsonl",
			wantSuffix:     "/subagents/agent-ab12cd3.meta.json",
		},
		{
			name:           "workflow agent transcript",
			transcriptPath: "/home/u/.claude/projects/slug/6c1e876f/subagents/workflows/wf_d2a7bbf5-710/agent-ab12cd3.jsonl",
			wantSuffix:     "/workflows/wf_d2a7bbf5-710/agent-ab12cd3.meta.json",
		},
		{
			name:           "empty path",
			transcriptPath: "",
			wantSuffix:     "",
		},
		{
			name:           "non-jsonl path",
			transcriptPath: "/home/u/.claude/projects/slug/agent-ab12cd3.txt",
			wantSuffix:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAgentMetaPathFromTranscript(tt.transcriptPath)
			if tt.wantSuffix == "" {
				if got != "" {
					t.Errorf("resolveAgentMetaPathFromTranscript(%q) = %q, want empty", tt.transcriptPath, got)
				}
				return
			}
			if !filepath.IsAbs(got) {
				t.Errorf("resolveAgentMetaPathFromTranscript(%q) = %q, want absolute path", tt.transcriptPath, got)
			}
			if got[len(got)-len(tt.wantSuffix):] != tt.wantSuffix {
				t.Errorf("resolveAgentMetaPathFromTranscript(%q) = %q, want suffix %q", tt.transcriptPath, got, tt.wantSuffix)
			}
		})
	}
}

func TestReadAgentMetaDescription(t *testing.T) {
	t.Run("valid meta.json with description", func(t *testing.T) {
		tmpDir := t.TempDir()
		metaPath := filepath.Join(tmpDir, "agent-abc123.meta.json")
		content := `{"agentType":"Explore","description":"Search for config files","toolUseId":"toolu_123"}`
		if err := os.WriteFile(metaPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		got := readAgentMetaDescription(metaPath)
		want := "Search for config files"
		if got != want {
			t.Errorf("readAgentMetaDescription() = %q, want %q", got, want)
		}
	})

	t.Run("meta.json without description (workflow subagent)", func(t *testing.T) {
		tmpDir := t.TempDir()
		metaPath := filepath.Join(tmpDir, "agent-def456.meta.json")
		content := `{"agentType":"workflow-subagent"}`
		if err := os.WriteFile(metaPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		got := readAgentMetaDescription(metaPath)
		if got != "" {
			t.Errorf("readAgentMetaDescription() = %q, want empty", got)
		}
	})

	t.Run("missing file returns empty", func(t *testing.T) {
		got := readAgentMetaDescription("/nonexistent/agent-xxx.meta.json")
		if got != "" {
			t.Errorf("readAgentMetaDescription() = %q, want empty", got)
		}
	})

	t.Run("malformed JSON returns empty", func(t *testing.T) {
		tmpDir := t.TempDir()
		metaPath := filepath.Join(tmpDir, "agent-bad.meta.json")
		if err := os.WriteFile(metaPath, []byte("not valid json"), 0o644); err != nil {
			t.Fatal(err)
		}

		got := readAgentMetaDescription(metaPath)
		if got != "" {
			t.Errorf("readAgentMetaDescription() = %q, want empty", got)
		}
	})
}

func TestWorkflowEventFromSubagentStopWithNameEnrichment(t *testing.T) {
	now := time.Date(2026, 6, 10, 17, 7, 14, 0, time.UTC)

	t.Run("enriches Name from meta.json", func(t *testing.T) {
		t.Setenv("GROVE_FLOW_JOB_ID", "job-99")

		// Create a temp directory with meta.json
		tmpDir := t.TempDir()
		subagentsDir := filepath.Join(tmpDir, "subagents")
		if err := os.MkdirAll(subagentsDir, 0o755); err != nil {
			t.Fatal(err)
		}

		metaContent := `{"agentType":"Explore","description":"Find auth handlers","toolUseId":"toolu_456"}`
		metaPath := filepath.Join(subagentsDir, "agent-a1.meta.json")
		if err := os.WriteFile(metaPath, []byte(metaContent), 0o644); err != nil {
			t.Fatal(err)
		}

		transcriptPath := filepath.Join(subagentsDir, "agent-a1.jsonl")
		ev := workflowEventFromSubagentStop(SubagentStopInput{
			SessionID:           "sess-1",
			AgentID:             "a1",
			AgentType:           "Explore",
			AgentTranscriptPath: &transcriptPath,
		}, now)

		if ev.Name != "Find auth handlers" {
			t.Errorf("Name = %q, want %q", ev.Name, "Find auth handlers")
		}
		if ev.Kind != models.WorkflowAgentCompleted {
			t.Errorf("Kind = %v, want WorkflowAgentCompleted", ev.Kind)
		}
	})

	t.Run("missing meta.json leaves Name empty", func(t *testing.T) {
		t.Setenv("GROVE_FLOW_JOB_ID", "")

		tmpDir := t.TempDir()
		transcriptPath := filepath.Join(tmpDir, "agent-noMeta.jsonl")
		ev := workflowEventFromSubagentStop(SubagentStopInput{
			SessionID:           "sess-2",
			AgentID:             "noMeta",
			AgentTranscriptPath: &transcriptPath,
		}, now)

		if ev.Name != "" {
			t.Errorf("Name = %q, want empty (no meta.json)", ev.Name)
		}
	})
}
