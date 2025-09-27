# Usage Patterns & Integrations

This document covers common usage patterns and integrations for `grove-hooks`, including how to integrate with Claude Code projects, grove-flow, and various session management workflows.

## Integrating with a Claude Code Project

### Installation

The primary way to integrate grove-hooks with a Claude Code project is through the `grove-hooks install` command:

```bash
grove-hooks install
```

This command will:
- Create a `.claude` directory if it doesn't exist
- Create or update `.claude/settings.local.json` with grove-hooks configuration
- Preserve existing settings when updating

### Before and After Example

**Before installation** (empty or non-existent `.claude/settings.local.json`):
```json
{}
```

**After installation** (`.claude/settings.local.json`):
```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": ".*",
        "hooks": [
          {
            "type": "command",
            "command": "grove-hooks pretooluse"
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": ".*",
        "hooks": [
          {
            "type": "command",
            "command": "grove-hooks posttooluse"
          }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": ".*",
        "hooks": [
          {
            "type": "command",
            "command": "grove-hooks notification"
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": ".*",
        "hooks": [
          {
            "type": "command",
            "command": "grove-hooks stop"
          }
        ]
      }
    ],
    "SubagentStop": [
      {
        "matcher": ".*",
        "hooks": [
          {
            "type": "command",
            "command": "grove-hooks subagentstop"
          }
        ]
      }
    ]
  }
}
```

### How Claude Code Automatically Calls the Hooks

Once installed, Claude Code will automatically call the grove-hooks commands at the appropriate times:

- **PreToolUse**: Called before any tool execution to validate and log the operation
- **PostToolUse**: Called after Edit, Write, MultiEdit, Bash, or Read tools complete
- **Notification**: Called when Claude Code sends notifications to the user
- **Stop**: Called when a Claude Code conversation or session stops
- **SubagentStop**: Called when a subagent completes its task

The hooks receive JSON data via stdin and some return JSON responses via stdout for approval/blocking decisions.

## Integrating with grove-flow

### Oneshot Command Integration

The `oneshot` command is specifically designed for integration with `grove-flow`. Grove-flow calls grove-hooks to track job lifecycle events during automated plan execution.

### Expected Integration Pattern

Grove-flow is expected to call grove-hooks at two key points:

1. **Job Start**: `grove-hooks oneshot start` with job details
2. **Job Stop**: `grove-hooks oneshot stop` with completion status

### JSON Payload Structures

#### Start Event Payload

```json
{
  "job_id": "unique-job-identifier",
  "plan_name": "my-feature-plan",
  "plan_directory": "/path/to/plan/directory",
  "job_title": "Implement user authentication",
  "job_file_path": "/path/to/job/file.md",
  "repository": "my-app",
  "branch": "feature/auth",
  "status": "running"
}
```

#### Stop Event Payload

```json
{
  "job_id": "unique-job-identifier",
  "status": "completed",
  "error": "Optional error message if status is 'failed'"
}
```

### Example Integration

```bash
# Start tracking a job
echo '{
  "job_id": "job_123",
  "plan_name": "user-auth",
  "job_title": "Add login form",
  "repository": "my-app",
  "branch": "feature/auth",
  "status": "running"
}' | grove-hooks oneshot start

# Complete the job
echo '{
  "job_id": "job_123",
  "status": "completed"
}' | grove-hooks oneshot stop
```

### Status Values

- **Start statuses**: `running`, `pending_user`
- **Stop statuses**: `completed`, `failed`, `success`

When status is `pending_user`, grove-hooks will send a notification to alert the user that input is required.

## Interactive Session Management

### Sessions Browse Command

The `sessions browse` command provides an interactive terminal UI for managing Claude sessions and grove-flow jobs:

```bash
grove-hooks sessions browse
```

### Common Workflows

#### Filtering for Active Sessions

1. Launch the browse interface: `grove-hooks sessions browse --active`
2. Use **Tab** to cycle through status filters:
   - All sessions
   - Running only
   - Idle only
   - Completed only
   - Failed only

#### Searching for a Specific Repository

1. Type to filter by repository, branch, user, or session ID
2. The search includes job-specific fields like plan name and job title
3. Example: Type "my-app" to find all sessions related to the "my-app" repository

#### Selecting and Archiving Multiple Sessions

1. Use **Space** to select individual sessions (marked with `[*]`)
2. Use **Ctrl+A** to select/deselect all filtered sessions
3. Use **Ctrl+X** to archive all selected sessions
4. Archived sessions are removed from the active list

#### Copying a Session ID

1. Navigate to the desired session using arrow keys
2. Press **Ctrl+Y** to copy the session ID to clipboard
3. The ID can then be used with other commands like `grove-hooks sessions get <id>`

#### Additional Browse Features

- **Enter**: View detailed session information
- **Ctrl+O**: Open the session's working directory in file manager
- **Ctrl+J**: Export session data as JSON file
- Auto-refresh every second to show real-time status updates

## Scripting and Automation

### JSON Output for Programmatic Access

Most grove-hooks commands support `--json` output for integration with scripts and automation tools:

```bash
# Get all sessions as JSON
grove-hooks sessions list --json

# Get specific session details
grove-hooks sessions get <session-id> --json
```

### Using jq for Advanced Queries

#### Find All Failed Sessions for a Specific User

```bash
grove-hooks sessions list --json | jq '.[] | select(.status == "failed" and .user == "john")'
```

#### Get Running Sessions by Repository

```bash
grove-hooks sessions list --json | jq '.[] | select(.status == "running" and .repo == "my-project")'
```

#### Calculate Total Session Duration by Status

```bash
grove-hooks sessions list --json | jq '
  group_by(.status) | 
  map({
    status: .[0].status, 
    count: length,
    total_duration: map(.state_duration_seconds) | add
  })'
```

#### Find Sessions with High Tool Usage

```bash
grove-hooks sessions list --json | jq '.[] | select(.tool_stats.total_calls > 50)'
```

#### Export Sessions from Last Week

```bash
grove-hooks sessions list --json | jq --arg week_ago "$(date -d '7 days ago' -Iseconds)" '
  .[] | select(.started_at > $week_ago)'
```

### Automation Examples

#### Clean Up Old Completed Sessions

```bash
#!/bin/bash
# Archive sessions completed more than 30 days ago

old_sessions=$(grove-hooks sessions list --json | jq -r '
  .[] | 
  select(.status == "completed" and (.ended_at // .started_at) < now - (30 * 24 * 3600)) |
  .id'
)

if [ -n "$old_sessions" ]; then
  echo "$old_sessions" | while read -r session_id; do
    echo "Archiving old session: $session_id"
    # Note: Individual archiving would require extending the CLI
    # Currently, archiving is done through the browse interface
  done
fi
```

#### Monitor for Failed Jobs

```bash
#!/bin/bash
# Check for failed oneshot jobs and send alerts

failed_jobs=$(grove-hooks sessions list --json --status failed | jq -r '
  .[] | 
  select(.type == "oneshot_job") |
  "\(.id) - \(.job_title // .plan_name) in \(.repo)"'
)

if [ -n "$failed_jobs" ]; then
  echo "Failed jobs detected:"
  echo "$failed_jobs"
  # Send notification via your preferred method
fi
```

#### Generate Session Reports

```bash
#!/bin/bash
# Generate a daily session summary report

today=$(date -Iseconds | cut -d'T' -f1)

grove-hooks sessions list --json | jq --arg today "$today" '
  map(select(.started_at | startswith($today))) |
  {
    date: $today,
    total_sessions: length,
    by_status: group_by(.status) | map({status: .[0].status, count: length}),
    by_type: group_by(.type // "claude_session") | map({type: .[0].type // "claude_session", count: length}),
    total_tools: map(.tool_stats.total_calls // 0) | add
  }'
```

These patterns enable powerful automation and monitoring capabilities while maintaining the flexibility to integrate grove-hooks into various development workflows.