package hooks

import (
	"reflect"
	"testing"
	"time"

	"github.com/grovetools/core/pkg/models"
)

func TestExtractBackgroundTaskID(t *testing.T) {
	// A backgrounded Bash result carries backgroundTaskId (probe-confirmed).
	resp := map[string]any{
		"backgroundTaskId": "bt3yezzj6",
		"interrupted":      false,
		"stdout":           "",
	}
	if got := extractBackgroundTaskID(resp); got != "bt3yezzj6" {
		t.Fatalf("extractBackgroundTaskID = %q, want bt3yezzj6", got)
	}
	// A foreground result carries none.
	if got := extractBackgroundTaskID(map[string]any{"stdout": "hi"}); got != "" {
		t.Fatalf("foreground result yielded id %q, want empty", got)
	}
	// A non-map (bare string) shape yields empty, never panics.
	if got := extractBackgroundTaskID("some error text"); got != "" {
		t.Fatalf("bare-string result yielded id %q, want empty", got)
	}
}

func TestWorkflowEventFromBashStart(t *testing.T) {
	t.Setenv("GROVE_FLOW_JOB_ID", "")
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	ev := workflowEventFromBashStart("sess-1", "bt3yezzj6", "sleep 120", now)
	want := models.WorkflowEvent{
		Kind:            models.WorkflowBashStarted,
		ClaudeSessionID: "sess-1",
		AgentID:         "bt3yezzj6",
		Name:            "sleep 120",
		Timestamp:       now,
		Source:          models.WorkflowSourceHooks,
	}
	if !reflect.DeepEqual(ev, want) {
		t.Fatalf("got %+v, want %+v", ev, want)
	}
	// It must be forwardable (non-empty AgentID keys the child).
	if !workflowEventForwardable(ev) {
		t.Fatal("bash_started event should be forwardable")
	}
}

func TestLiveBashChildren(t *testing.T) {
	tasks := []map[string]any{
		// A live background bash.
		{"type": "shell", "id": "bt1", "command": "sleep 120", "status": "running"},
		// A workflow entry — not bash, excluded.
		{"type": "workflow", "id": "wf1", "name": "probe", "status": "running"},
		// A subagent entry — not bash, excluded.
		{"type": "subagent", "id": "sa1", "status": "running"},
		// A terminal shell — excluded (already done).
		{"type": "shell", "id": "bt2", "command": "echo hi", "status": "completed"},
		// A shell with no id — excluded (unkeyable).
		{"type": "shell", "command": "noid", "status": "running"},
	}
	got := liveBashChildren(tasks)
	want := []models.BashChildRef{{ID: "bt1", Command: "sleep 120"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("liveBashChildren = %+v, want %+v", got, want)
	}

	// No background tasks → nil (a hook snapshot with nil clears bash downstream).
	if got := liveBashChildren(nil); got != nil {
		t.Fatalf("liveBashChildren(nil) = %+v, want nil", got)
	}
}

func TestChildrenSnapshotCarriesLiveBash(t *testing.T) {
	t.Setenv("GROVE_FLOW_JOB_ID", "job-9")
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	data := SubagentStopInput{
		SessionID: "sess-1",
		BackgroundTasks: []map[string]any{
			{"type": "shell", "id": "bt1", "command": "sleep 120", "status": "running"},
		},
	}
	ev := workflowChildrenSnapshotEvent(data, now)
	if ev.Kind != models.WorkflowChildrenSnapshot || ev.JobID != "job-9" {
		t.Fatalf("snapshot header wrong: %+v", ev)
	}
	if ev.LiveChildren != 1 {
		t.Fatalf("LiveChildren = %d, want 1", ev.LiveChildren)
	}
	want := []models.BashChildRef{{ID: "bt1", Command: "sleep 120"}}
	if !reflect.DeepEqual(ev.LiveBashChildren, want) {
		t.Fatalf("LiveBashChildren = %+v, want %+v", ev.LiveBashChildren, want)
	}
}
