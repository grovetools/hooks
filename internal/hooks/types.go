package hooks

// Common types used by hooks

type NotificationInput struct {
	SessionID              string `json:"session_id"`
	TranscriptPath         string `json:"transcript_path"`
	HookEventName          string `json:"hook_event_name"`
	Type                   string `json:"type"`
	Message                string `json:"message"`
	Level                  string `json:"level"` // info, warning, error
	SystemNotificationSent bool   `json:"system_notification_sent"`
	AgentID                string `json:"agent_id,omitempty"`
	AgentType              string `json:"agent_type,omitempty"`
	CurrentUUID            string `json:"current_uuid,omitempty"`
	ParentUUID             string `json:"parent_uuid,omitempty"`
}

type PreToolUseInput struct {
	SessionID      string         `json:"session_id"`
	TranscriptPath string         `json:"transcript_path"`
	HookEventName  string         `json:"hook_event_name"`
	ToolName       string         `json:"tool_name"`
	ToolInput      map[string]any `json:"tool_input"`
	// NOTE: Claude Code does NOT send tool_use_id on the PreToolUse payload
	// (only PostToolUse carries it), so it is intentionally absent here.
	AgentID     string `json:"agent_id,omitempty"`
	AgentType   string `json:"agent_type,omitempty"`
	CurrentUUID string `json:"current_uuid,omitempty"`
	ParentUUID  string `json:"parent_uuid,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
}

type PostToolUseInput struct {
	SessionID      string  `json:"session_id"`
	TranscriptPath string  `json:"transcript_path"`
	HookEventName  string  `json:"hook_event_name"`
	ToolName       string  `json:"tool_name"`
	ToolInput      any     `json:"tool_input"`
	ToolResponse   any     `json:"tool_response"`
	ToolOutput     any     `json:"tool_output"` // Legacy field
	ToolDurationMs int64   `json:"tool_duration_ms"`
	ToolError      *string `json:"tool_error"`
	ToolUseID      string  `json:"tool_use_id,omitempty"`
	AgentID        string  `json:"agent_id,omitempty"`
	AgentType      string  `json:"agent_type,omitempty"`
	CurrentUUID    string  `json:"current_uuid,omitempty"`
	ParentUUID     string  `json:"parent_uuid,omitempty"`
	Cwd            string  `json:"cwd,omitempty"`
}

type StopInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	HookEventName  string `json:"hook_event_name"`
	ExitReason     string `json:"exit_reason"`
	DurationMs     int64  `json:"duration_ms"`
	AgentID        string `json:"agent_id,omitempty"`
	AgentType      string `json:"agent_type,omitempty"`
	CurrentUUID    string `json:"current_uuid,omitempty"`
	ParentUUID     string `json:"parent_uuid,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
}

// SessionStartInput is the payload delivered to the SessionStart hook.
// Observed field set (CC v2.1.172 probe): session_id, transcript_path, cwd,
// hook_event_name.
type SessionStartInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
	HookEventName  string `json:"hook_event_name"`
}

// SubagentStartInput is the payload delivered to the SubagentStart hook.
// Observed field set (CC v2.1.172 probe): session_id, transcript_path, cwd,
// hook_event_name, agent_id, agent_type. The payload is minimal by design —
// no prompt, no per-agent transcript path, no run id. AgentType
// discriminates spawn sources: "workflow-subagent" for workflow-spawned
// agents vs the subagent type name (e.g. "Explore") for Agent-tool spawns.
type SubagentStartInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
	HookEventName  string `json:"hook_event_name"`
	AgentID        string `json:"agent_id,omitempty"`
	AgentType      string `json:"agent_type,omitempty"`
}

type SubagentStopInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	HookEventName  string `json:"hook_event_name"`
	Cwd            string `json:"cwd,omitempty"`

	// Real payload fields. agent_id/agent_type ship since Claude Code
	// v2.1.69; the rest are version-gated and may be absent, so they are
	// pointers/omitempty and must be treated as optional.
	StopHookActive       bool             `json:"stop_hook_active,omitempty"`
	AgentID              string           `json:"agent_id,omitempty"`
	AgentType            string           `json:"agent_type,omitempty"`
	AgentTranscriptPath  *string          `json:"agent_transcript_path,omitempty"`
	LastAssistantMessage *string          `json:"last_assistant_message,omitempty"`
	BackgroundTasks      []map[string]any `json:"background_tasks,omitempty"`
	SessionCrons         []map[string]any `json:"session_crons,omitempty"`

	// Legacy fields the handler historically expected. Real payloads have
	// never been observed carrying them; kept as a fallback only.
	SubagentID   string  `json:"subagent_id,omitempty"`
	SubagentTask string  `json:"subagent_task,omitempty"`
	DurationMs   int64   `json:"duration_ms,omitempty"`
	Status       string  `json:"status,omitempty"`
	Result       any     `json:"result,omitempty"`
	Error        *string `json:"error,omitempty"`

	CurrentUUID string `json:"current_uuid,omitempty"`
	ParentUUID  string `json:"parent_uuid,omitempty"`
}

// SessionStatusInput is the payload for the session-status hook, sent by
// provider integrations (opencode plugin) reporting a non-terminal status
// transition. Status carries the provider's raw status word; the handler
// normalizes it via NormalizeProviderSessionStatus.
type SessionStatusInput struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
	Status        string `json:"status"`
	Cwd           string `json:"cwd,omitempty"`
}

// SessionEndInput is the payload for the session-end hook, sent by provider
// integrations when the provider destroyed the session (e.g. opencode's
// session.deleted event).
type SessionEndInput struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
	Reason        string `json:"reason,omitempty"`
	Cwd           string `json:"cwd,omitempty"`
}

type PreToolUseResponse struct {
	Approved bool   `json:"approved"`
	Message  string `json:"message,omitempty"`
}
