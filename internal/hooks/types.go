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
	CurrentUUID            string `json:"current_uuid,omitempty"`
	ParentUUID             string `json:"parent_uuid,omitempty"`
}

type PreToolUseInput struct {
	SessionID      string         `json:"session_id"`
	TranscriptPath string         `json:"transcript_path"`
	HookEventName  string         `json:"hook_event_name"`
	ToolName       string         `json:"tool_name"`
	ToolInput      map[string]any `json:"tool_input"`
	CurrentUUID    string         `json:"current_uuid,omitempty"`
	ParentUUID     string         `json:"parent_uuid,omitempty"`
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
	CurrentUUID    string  `json:"current_uuid,omitempty"`
	ParentUUID     string  `json:"parent_uuid,omitempty"`
}

type StopInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	HookEventName  string `json:"hook_event_name"`
	ExitReason     string `json:"exit_reason"`
	DurationMs     int64  `json:"duration_ms"`
	CurrentUUID    string `json:"current_uuid,omitempty"`
	ParentUUID     string `json:"parent_uuid,omitempty"`
}

type SubagentStopInput struct {
	SessionID      string  `json:"session_id"`
	TranscriptPath string  `json:"transcript_path"`
	HookEventName  string  `json:"hook_event_name"`
	SubagentID     string  `json:"subagent_id"`
	SubagentTask   string  `json:"subagent_task"`
	DurationMs     int64   `json:"duration_ms"`
	Status         string  `json:"status"`
	Result         any     `json:"result"`
	Error          *string `json:"error"`
	CurrentUUID    string  `json:"current_uuid,omitempty"`
	ParentUUID     string  `json:"parent_uuid,omitempty"`
}

type PreToolUseResponse struct {
	Approved bool   `json:"approved"`
	Message  string `json:"message,omitempty"`
}