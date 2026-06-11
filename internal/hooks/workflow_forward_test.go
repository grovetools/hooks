package hooks

import (
	"testing"
	"time"

	"github.com/grovetools/core/pkg/models"
)

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
		if ev != want {
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
		if ev != want {
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
