# Configuring Grove Hooks

Grove Hooks provides observability by integrating with other tools in the Grove ecosystem, primarily through a system of event-driven hooks. Configuration involves defining which commands are executed at specific points in an AI agent's lifecycle.

## Primary Configuration: Claude Code Integration

The most common use case for `grove-hooks` is integration with the Claude Code CLI. This is managed through a `settings.local.json` file in your project's `.claude` directory. The `grove-hooks install` command automates this setup.

```bash
# Navigate to your project root
cd /path/to/your/project

# Run the install command
grove-hooks install
```

This command creates or updates the `.claude/settings.local.json` file, injecting the necessary hook configurations. It preserves any other existing settings you may have.

### Example `settings.local.json`

After running `install`, your configuration file will contain a `hooks` section similar to this:

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
    ]
  }
}
```

This configuration tells the Claude CLI to execute the `grove-hooks` binary at different lifecycle events, passing relevant context via standard input.

## Hook Triggers and Types

Grove Hooks responds to specific, named lifecycle events triggered by the host application (e.g., Claude Code). Each hook captures a distinct stage of an agent session.

-   **`PreToolUse`**: Executes *before* an agent uses a tool. This hook logs the intent to use a tool and its parameters. It can also be used to implement validation or approval logic.
-   **`PostToolUse`**: Executes *after* a tool has been used. This hook captures the tool's output, execution duration, and whether it succeeded or failed, providing a complete record of the action.
-   **`Notification`**: Triggers when the agent generates a notification, such as a warning or an error. This allows for centralized logging and can be configured to send system-level desktop notifications for important events.
-   **`Stop`**: Triggers when a session ends for any reason (e.g., completion, interruption, or error). This hook is responsible for finalizing the session's status in the local database.
-   **`SubagentStop`**: Triggers when a sub-agent completes its delegated task.


## Environment Variables

-   **`GROVE_HOOKS_DB_PATH`**: Overrides the default path to the SQLite database. This is particularly useful for isolating data in testing environments or for custom storage setups.
    -   **Default**: `~/.local/share/grove-hooks/state.db`
-   **`GROVE_DEBUG`**: If set to a non-empty string, enables verbose debug logging to standard output, which can help in troubleshooting hook execution.
