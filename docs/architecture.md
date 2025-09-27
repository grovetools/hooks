# Architecture Overview

This document provides a high-level overview of the `grove-hooks` architecture, explaining how its components interact to provide session tracking and hook execution capabilities for the Grove ecosystem.

## System Overview

Grove-hooks is designed as a dual-purpose binary that can operate both as a hook implementation (when called via symlinks) and as a CLI tool (when called directly). It serves as the central tracking and logging system for Claude Code sessions and grove-flow jobs.

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Claude Code   │    │   grove-flow    │    │   User (CLI)    │
│                 │    │                 │    │                 │
│ • Session mgmt  │    │ • Job execution │    │ • Query data    │
│ • Tool execution│    │ • Plan mgmt     │    │ • Browse sessions│
│ • Notifications │    │ • Automation    │    │ • Archive old   │
└─────────┬───────┘    └─────────┬───────┘    └─────────┬───────┘
          │                      │                      │
          │ JSON via hooks       │ JSON via oneshot     │ CLI commands
          │                      │                      │
          └──────────────────────┼──────────────────────┘
                                 │
                    ┌────────────▼────────────┐
                    │      grove-hooks        │
                    │                         │
                    │ ┌─────────────────────┐ │
                    │ │   Hook Handlers     │ │
                    │ │                     │ │
                    │ │ • Parse JSON input  │ │
                    │ │ • Validate tools    │ │
                    │ │ • Log events        │ │
                    │ │ • Execute repo hooks│ │
                    │ └─────────────────────┘ │
                    │ ┌─────────────────────┐ │
                    │ │   CLI Commands      │ │
                    │ │                     │ │
                    │ │ • sessions list     │ │
                    │ │ • sessions browse   │ │
                    │ │ • oneshot start/stop│ │
                    │ │ • install           │ │
                    │ └─────────────────────┘ │
                    │ ┌─────────────────────┐ │
                    │ │   Storage Layer     │ │
                    │ │                     │ │
                    │ │ • SQLite database   │ │
                    │ │ • Session tracking  │ │
                    │ │ • Event logging     │ │
                    │ │ • Tool statistics   │ │
                    │ └─────────────────────┘ │
                    └─────────────────────────┘
```

## Entrypoints and Routing

### Dual Binary Nature

The `main.go` file implements a sophisticated routing mechanism that allows the same binary to function in two different modes:

```go
// Check if called via symlink to determine which hook to run
execName := filepath.Base(os.Args[0])

// If called directly as 'hooks' or 'grove-hooks', show help
if execName == "hooks" || execName == "grove-hooks" {
    // Add CLI subcommands
    rootCmd.AddCommand(newNotificationCmd())
    rootCmd.AddCommand(newPreToolUseCmd())
    // ... other commands
} else {
    // Called via symlink, execute the corresponding hook
    switch execName {
    case "notification":
        runNotificationHook()
    case "pretooluse", "pre-tool-use":
        runPreToolUseHook()
    // ... other hooks
    }
}
```

### Hook Mode (Symlink Execution)

When called via symlinks (e.g., `notification`, `pretooluse`, `stop`), grove-hooks operates as a hook handler:

1. **Reads JSON from stdin**: All hook data is passed via standard input
2. **Executes immediately**: No subcommand parsing, direct hook execution
3. **Minimal output**: Returns JSON responses or exits silently
4. **Fast execution**: Optimized for low-latency hook processing

### CLI Mode (Direct Execution)

When called as `grove-hooks` or `hooks`, it operates as a full CLI tool:

1. **Cobra command parsing**: Full subcommand and flag support
2. **Interactive capabilities**: Supports complex user interactions
3. **Rich output**: Tables, JSON, interactive browsing
4. **Extended functionality**: Session management, installation, browsing

## Hook Handlers

The hook handlers in `/internal/hooks/hooks.go` are the core of the grove-hooks system. Each handler follows a similar pattern:

### Hook Handler Pattern

```go
func RunXxxHook() {
    // 1. Initialize hook context (storage, input parsing)
    ctx, err := NewHookContext()
    
    // 2. Parse JSON input from stdin
    var data XxxInput
    json.Unmarshal(ctx.RawInput, &data)
    
    // 3. Process the hook-specific logic
    // (validation, computation, repository actions)
    
    // 4. Log the event to storage
    ctx.LogEvent(eventType, eventData)
    
    // 5. Return response (if required)
    // Some hooks return JSON approval/blocking decisions
}
```

### Individual Hook Functions

#### `RunPreToolUseHook()`
- **Purpose**: Validates tool usage before execution
- **Input**: Tool name, parameters, session context
- **Processing**: Apply tool-specific validation rules
- **Output**: Approval/blocking decision as JSON
- **Storage**: Creates tool execution records, logs validation events

#### `RunPostToolUseHook()`
- **Purpose**: Records tool execution results and statistics
- **Input**: Tool results, duration, success/failure status
- **Processing**: Build result summaries, extract file modifications
- **Output**: Silent (no JSON response)
- **Storage**: Updates tool execution records with completion data

#### `RunNotificationHook()`
- **Purpose**: Handles Claude Code notifications
- **Input**: Notification type, message, level
- **Processing**: Determines if system notifications should be sent
- **Output**: Silent (no JSON response)
- **Storage**: Logs notifications with metadata

#### `RunStopHook()`
- **Purpose**: Processes session completion
- **Input**: Session exit reason, duration, summary data
- **Processing**: Execute repository hooks, update session status
- **Output**: May exit with error code if repository hooks block
- **Storage**: Updates session status, logs completion events

#### `RunSubagentStopHook()`
- **Purpose**: Tracks subagent task completion
- **Input**: Subagent ID, task details, results
- **Processing**: Categorize task types, extract results
- **Output**: Silent (no JSON response)
- **Storage**: Logs subagent completion events

### Hook Context Management

The `HookContext` struct provides shared functionality:

```go
type HookContext struct {
    Storage      interfaces.SessionStorer
    RawInput     []byte
    WorkingDir   string
    User         string
    // ... other context fields
}
```

## CLI Commands Structure

The CLI commands in `/internal/commands/` provide user-facing functionality:

### Command Categories

#### Session Management Commands
- **`sessions list`**: Query and filter session data
  - Supports JSON output, status filtering, limiting results
  - Automatically sorts by priority (running → idle → completed)
  - Includes state duration calculations

- **`sessions get <id>`**: Retrieve detailed session information
  - Shows comprehensive session metadata
  - Includes tool statistics and session summaries
  - Supports both regular and extended (oneshot) sessions

- **`sessions browse`**: Interactive terminal UI for session management
  - Real-time filtering and search capabilities
  - Multi-selection and bulk archiving
  - Clipboard integration and file manager opening

#### Installation Commands
- **`install`**: Configure Claude Code integration
  - Creates or updates `.claude/settings.local.json`
  - Preserves existing settings during updates
  - Handles corrupted configuration files gracefully

#### Oneshot Commands
- **`oneshot start`**: Begin tracking grove-flow jobs
  - Accepts job metadata via JSON stdin
  - Creates extended session records with job-specific fields
  - Sends notifications for pending user input

- **`oneshot stop`**: Complete grove-flow job tracking
  - Updates job status and error information
  - Triggers completion notifications
  - Logs job completion events

### Command Architecture

Each command follows the pattern:

1. **Create storage connection**: Initialize SQLite store
2. **Parse command-line arguments**: Handle flags and parameters
3. **Execute business logic**: Perform the requested operation
4. **Format output**: Table, JSON, or interactive display
5. **Clean up resources**: Close database connections

## Storage Layer

### SQLite Database Design

The storage layer centers around a SQLite database located at `~/.local/share/grove-hooks/state.db` (or `$GROVE_HOOKS_DB_PATH` if set).

#### Core Tables

**sessions**: Primary session tracking
```sql
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    type TEXT DEFAULT 'claude_session',  -- or 'oneshot_job'
    pid INTEGER,
    repo TEXT,
    branch TEXT,
    status TEXT NOT NULL,              -- 'running', 'idle', 'completed', 'failed'
    started_at DATETIME NOT NULL,
    ended_at DATETIME,
    last_activity DATETIME,
    working_directory TEXT,
    user TEXT,
    -- Extended fields for oneshot jobs
    plan_name TEXT,
    plan_directory TEXT,
    job_title TEXT,
    job_file_path TEXT,
    last_error TEXT,
    -- Metadata fields
    tool_stats TEXT,                   -- JSON blob
    session_summary TEXT,              -- JSON blob
    is_test BOOLEAN DEFAULT 0,
    is_deleted BOOLEAN DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

**tool_executions**: Individual tool usage tracking
```sql
CREATE TABLE tool_executions (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    command TEXT,
    args TEXT,                         -- JSON parameters
    status TEXT NOT NULL,              -- 'running', 'completed', 'failed'
    duration_ms INTEGER,
    error_message TEXT,
    started_at DATETIME NOT NULL,
    completed_at DATETIME,
    metadata TEXT,                     -- JSON result summary
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);
```

**events**: Detailed event logging
```sql
CREATE TABLE events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    type TEXT NOT NULL,                -- 'PreToolUse', 'PostToolUse', etc.
    name TEXT NOT NULL,
    metadata TEXT,                     -- JSON event data
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);
```

**notifications**: Notification history
```sql
CREATE TABLE notifications (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    type TEXT NOT NULL,
    title TEXT,
    message TEXT,
    level TEXT,
    metadata TEXT,                     -- JSON with additional context
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);
```

### Extended Session Support

The `ExtendedSession` struct wraps the core `models.Session` with oneshot-specific fields:

```go
type ExtendedSession struct {
    models.Session
    Type          string `json:"type" db:"type"`
    PlanName      string `json:"plan_name" db:"plan_name"`
    PlanDirectory string `json:"plan_directory" db:"plan_directory"`
    JobTitle      string `json:"job_title" db:"job_title"`
    JobFilePath   string `json:"job_file_path" db:"job_file_path"`
}
```

This design allows grove-hooks to handle both Claude Code sessions and grove-flow jobs in a unified manner while preserving job-specific metadata.

### Database Operations

The storage layer provides these key operations:

- **Session Management**: Create, update, retrieve, and archive sessions
- **Tool Tracking**: Log tool executions and update with results
- **Event Logging**: Record all hook-triggered events with metadata
- **Notification Storage**: Maintain notification history
- **Bulk Operations**: Archive multiple sessions efficiently

## Ecosystem Interactions

### Claude Code Integration Flow

```
Claude Code Session Start
         ↓
    Hooks trigger based on .claude/settings.local.json
         ↓
    grove-hooks creates session record
         ↓
┌─── Tool Execution Loop ───┐
│   PreToolUse Hook         │ → Validation & Logging
│         ↓                 │
│   Tool Executes           │
│         ↓                 │
│   PostToolUse Hook        │ → Result Logging & Statistics
└───────────────────────────┘
         ↓
    Session End (Stop Hook)
         ↓
    Repository hooks execute (.canopy.yaml)
         ↓
    Session marked complete
```

### Grove-Flow Integration Flow

```
grove-flow Plan Execution
         ↓
    For each job:
         ↓
    grove-hooks oneshot start
         ↓ (JSON with job metadata)
    grove-hooks creates job session
         ↓
    grove-flow executes job
         ↓
    grove-hooks oneshot stop
         ↓ (JSON with completion status)
    grove-hooks updates job status
         ↓
    Notifications sent if configured
```

### Data Flow Architecture

```
┌─────────────────┐
│ External Systems│
│ • Claude Code   │
│ • grove-flow    │ 
│ • User CLI      │
└─────────┬───────┘
          │ Commands/JSON
          ▼
┌─────────────────┐
│ grove-hooks     │
│ • Routing       │
│ • Validation    │
│ • Processing    │
└─────────┬───────┘
          │ Storage Operations
          ▼
┌─────────────────┐
│ SQLite Database │
│ • Sessions      │
│ • Tools         │
│ • Events        │
│ • Notifications │
└─────────────────┘
```

This architecture provides:

- **Low latency**: Direct binary execution for hooks
- **Rich functionality**: Full CLI capabilities for users
- **Extensibility**: Plugin-like repository hooks via `.canopy.yaml`
- **Data persistence**: Local SQLite storage for offline access
- **Integration flexibility**: JSON-based communication protocols

The design emphasizes local-first operation while providing the hooks and APIs necessary for ecosystem integration.