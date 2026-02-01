# Configuring Grove Hooks

`grove-hooks` uses a hook system to execute commands at specific points in an AI agent's lifecycle. Configuration is handled through files in the host application's directory, a repository-specific file, and environment variables.

## Configuration Mechanisms

There are two primary file-based configuration contexts.

### 1. Host Application Integration

The `grove-hooks` binary is invoked by a host application (e.g., `claude-code`). The `grove-hooks install` command automates this setup.

```bash
# In a project's root directory
grove-hooks install
```

This command creates or modifies `.claude/settings.local.json`. This file instructs the host application to execute a specific `grove-hooks` subcommand when a corresponding event occurs.

**Example `.claude/settings.local.json`:**

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": ".*",
      "hooks": [{"type": "command", "command": "grove-hooks pretooluse"}]
    }],
    "PostToolUse": [{
      "matcher": "(Edit|Write|MultiEdit|Bash|Read)",
      "hooks": [{"type": "command", "command": "grove-hooks posttooluse"}]
    }],
    "Stop": [{
      "matcher": ".*",
      "hooks": [{"type": "command", "command": "grove-hooks stop"}]
    }]
  }
}
```

### 2. Repository-Specific Hooks

The `hooks` section in a project's `grove.yml` (or `.grove.yml`) can define shell commands to be executed when a session stops. This is used for actions like running linters, tests, or cleanup scripts.

**Example `grove.yml`:**

```yaml
hooks:
  on_stop:
    - name: "Show Git Status"
      command: "git status"
      run_if: "changes"
    - name: "Run Linter"
      command: "make lint"
```

-   **`on_stop`**: A list of commands to run when the `stop` hook is triggered for a session in this repository.
-   **`name`**: A descriptive name for the command.
-   **`command`**: The shell command to execute.
-   **`run_if`**: (Optional) A condition for execution. The only current value is `"changes"`, which runs the command only if `git status` reports uncommitted changes.

If a command in `on_stop` exits with code `2`, it is treated as a blocking error. The error message from the command's stderr is passed back to the host application, which may prevent the session from closing.

## Hook Event Reference

`grove-hooks` responds to events triggered by the host application. Each event corresponds to a subcommand that receives a JSON payload via stdin.

-   **`PreToolUse`**
    -   **Trigger**: Before the agent executes a tool.
    -   **Action**: Records the session's start time, working directory, and tool parameters in a local SQLite database.

-   **`PostToolUse`**
    -   **Trigger**: After a tool has been executed.
    -   **Action**: Records the tool's output, execution duration, and success or failure status. The `tool_error` field in the input JSON is logged if present.

-   **`Notification`**
    -   **Trigger**: When the agent generates a notification message.
    -   **Action**: Logs the notification content.

-   **`Stop`**
    -   **Trigger**: When a session ends for any reason (e.g., completion, interruption).
    -   **Action**: Updates the final session status in the database. Executes `on_stop` commands defined in the `hooks` section of `grove.yml` if present in the session's working directory.

-   **`SubagentStop`**
    -   **Trigger**: When a sub-agent completes a delegated task.
    -   **Action**: Logs the sub-agent's task, status, and result.

## Environment Variables

-   **`GROVE_HOOKS_DB_PATH`**: Overrides the default path to the SQLite database (`~/.local/share/grove-hooks/state.db`). Used primarily for test isolation.
-   **`GROVE_DEBUG`**: If set to a non-empty string, enables verbose debug logging to stderr.
-   **`GROVE_FLOW_JOB_ID`**: When a session is initiated by `grove-flow`, this variable links the Claude session to a specific flow job ID for unified tracking.
-   **`XDG_DATA_HOME`**: If set, `grove-hooks` stores its database and session artifacts in `$XDG_DATA_HOME/grove-hooks/` and `$XDG_DATA_HOME/hooks/` respectively, following the XDG Base Directory Specification.

## Common Use Cases and Limitations

### Use Cases

-   **Session Auditing**: The primary function is to create a local, queryable record of all agent activity. `grove-hooks sessions list` and `grove-hooks browse` provide interfaces to this data.
-   **Performance Monitoring**: The `PostToolUse` and `Stop` hooks receive `duration_ms` fields, which are logged to the database. This data can be used to analyze tool and session performance.
-   **Post-Session Automation**: Using the `hooks` section in `grove.yml`, you can define commands that run automatically when an agent session concludes, such as code validation or status notifications.

### Limitations

-   **No Direct LLM Instrumentation**: `grove-hooks` does not monitor raw API requests or responses to an LLM. It operates at the level of "tool use" events as reported by the host application.
-   **Local Scope**: All data is stored in a local SQLite database on the user's machine. It is not designed for centralized, multi-user observability.