# Examples

This document provides practical examples for using `grove-hooks`.

## Example 1: Basic Session Monitoring

This example covers installing `grove-hooks` in a project and monitoring an interactive AI agent session.

1.  **Install Hooks in a Project**

    Navigate to the project's root directory and run the `install` command.

    ```bash
    # In your project's root directory
    grove-hooks install
    ```

    This command creates or modifies `.claude/settings.local.json`, configuring the agent driver (e.g., Claude CLI) to send lifecycle events to `grove-hooks`.

2.  **Start an Agent Session**

    Begin a development session using an integrated tool.

    ```bash
    claude -p "Refactor the main database connection logic."
    ```

    When the session starts and the agent first uses a tool, `grove-hooks` creates a new session record in its local SQLite database and tracks its process ID (PID) via a lock file in `~/.grove/hooks/sessions/`.

3.  **List Active Sessions**

    While the session is running, open another terminal and use `grove-hooks sessions list`. The `--active` flag hides sessions that are already completed or have failed.

    ```bash
    grove-hooks sessions list --active
    ```

    **Expected Output:**

    ```
    SESSION ID      TYPE          STATUS     CONTEXT              USER    AGE
    c2e5a7b1b3f9    claude_code   running    my-project (wt:main) myuser  3m15s
    ```

    The output shows the session's type, its `running` status, and the repository context, including the worktree or branch. The `AGE` column indicates the time since the session's last activity.

## Example 2: Advanced Analysis

This example demonstrates how to investigate a specific session's history and export its data.

1.  **Find a Specific Session**

    To find a session that has finished its turn but is awaiting further input, filter the list by the `idle` status.

    ```bash
    grove-hooks sessions list --status idle
    ```

    **Expected Output:**

    ```
    SESSION ID      TYPE          STATUS     CONTEXT                    USER    AGE
    a9b8c7d6e5f4    claude_code   idle       api-refactor (wt:feat...)  myuser  15m30s
    ```

2.  **Get Detailed Information**

    Use the `sessions get` command with the session ID to retrieve the full record, including tool usage statistics.

    ```bash
    grove-hooks sessions get a9b8c7d6e5f4
    ```

    **Expected Output:**

    ```
    Session ID: a9b8c7d6e5f4...
    Type: claude_code
    Status: idle
    Repository: api-refactor
    Branch: feature/new-auth
    User: myuser
    Working Directory: /home/dev/projects/api-refactor
    PID: 12345
    Started: 2023-10-27T11:00:00Z
    Ended: <nil>
    Duration: 15m30.001s
    Tmux Key: api-refactor

    Tool Statistics:
      Total Calls: 5
      Bash Commands: 2
      File Modifications: 3
      File Reads: 8
      Search Operations: 1
    ```

    This output provides the working directory, PID, user, timing information, and a summary of tool usage, which is useful for debugging agent behavior or understanding task complexity.

3.  **Export Data as JSON**

    To perform programmatic analysis or archive session data, export the full record as JSON.

    ```bash
    grove-hooks sessions list --limit 1 --json
    ```

    **Expected Output (Truncated):**

    ```json
    [
      {
        "id": "a9b8c7d6e5f4...",
        "type": "claude_code",
        "status": "idle",
        "repo": "api-refactor",
        "branch": "feature/new-auth",
        ...
        "duration_seconds": null,
        "duration_human": "",
        "age_seconds": 930.001,
        "age_human": "15m30s",
        "last_activity_seconds_ago": 930.001,
        "last_activity_human": "15m30s"
      }
    ]
    ```
    The JSON output includes structured data with both machine-readable (seconds) and human-readable time formats.

## Example 3: Grove Integration

This example shows how `grove-hooks` provides unified observability for activities orchestrated by `grove-flow` alongside interactive sessions.

1.  **Create and Run a Grove Flow Plan**

    Use `grove-flow` to define and execute an automated job. `grove-flow` is configured to report job status, allowing `grove-hooks` to discover and track it.

    ```bash
    # In a project configured for grove-flow
    # Initialize a new plan
    flow plan init new-feature-docs

    # Add a 'oneshot' job to generate documentation
    flow plan add new-feature-docs --title "Generate API Docs" --type oneshot \
      -p "Generate OpenAPI documentation for the new endpoints in user_controller.go"

    # Run the plan
    flow plan run new-feature-docs --yes
    ```

2.  **View the Unified Session List**

    While the `flow` plan is running or after it has completed, use `grove-hooks` to view all tracked activities.

    ```bash
    grove-hooks sessions list
    ```

    **Expected Output:**

    ```
    SESSION ID      TYPE          STATUS       CONTEXT                         USER    AGE
    gen-api-docs    job           completed    api-refactor (wt:feat...):Ge... myuser  1m5s
    c2e5a7b1b3f9    claude_code   running      my-project (wt:main)            myuser  12m5s
    ```

    The list includes entries for both interactive (`claude_code`) and automated (`job`) sessions. For `job` type entries, the `CONTEXT` column displays the repository and job title. This provides a single location to monitor all AI-driven activity.

3.  **Browse All Sessions Interactively**

    The `sessions browse` command (alias: `tui`) launches a terminal user interface for filtering and exploring all discovered sessions and their associated workspaces.

    ```bash
    grove-hooks sessions browse
    ```

    This interface organizes sessions within a workspace hierarchy and provides keybindings for navigation and actions:
    *   **Tree View**: Sessions are nested under their respective projects, worktrees, and `grove-flow` plans.
    *   **Filtering**: Press `f` to toggle status/type filters.
    *   **Search**: Press `/` to search across all session metadata.
    *   **Actions**: Press `enter` to view details, `o` to open a running session's tmux window, or `e` to edit a job's source file.