# Examples

This document provides practical examples for using `grove-hooks`.

## Example 1: Basic Session Monitoring

This example covers integrating `grove-hooks` with a project and monitoring an interactive session.

1.  **Install Hooks in Your Project**

    Navigate to the project's root directory and run the `install` command.

    ```bash
    # In your project's root directory
    hooks install
    ```

    This command modifies or creates `.claude/settings.local.json` to configure the Claude CLI to send lifecycle events to `grove-hooks`.

2.  **Start a Claude Session**

    Begin a development session using an integrated tool.

    ```bash
    claude -p "Refactor the main database connection logic."
    ```

    When the session starts, `grove-hooks` creates a new record in its local SQLite database.

3.  **List Active Sessions**

    While the session is running, open another terminal and use `hooks sessions list`. The `--active` flag hides completed or failed sessions.

    ```bash
    hooks sessions list --active
    ```

    **Expected Output:**

    ```
    SESSION ID      TYPE      STATUS     CONTEXT              USER    STARTED               DURATION    IN STATE
    claude-axb...   claude    running    my-project/main      dev     2023-10-27 10:30:05   running     2m15s
    ```

    The output table shows the session's type, `running` status, repository and branch context, user, start time, and duration in the current state.

## Example 2: Analyzing a Session

This example shows how to use `grove-hooks` to investigate a specific session's history.

1.  **Find a Specific Session**

    To find a session that has finished its work but has not been terminated, filter the list by status.

    ```bash
    hooks sessions list --status idle
    ```

    **Expected Output:**

    ```
    SESSION ID      TYPE      STATUS     CONTEXT               USER    STARTED               DURATION    IN STATE
    claude-cyz...   claude    idle     api-refactor/feat...  dev     2023-10-27 11:00:00   15m30s      15m30s
    ```

2.  **Get Detailed Information**

    Use the `sessions get` command with the session ID to retrieve the full record, including tool usage statistics.

    ```bash
    hooks sessions get claude-cyz...
    ```

    **Expected Output:**

    ```
    Session ID: claude-cyz...
    Type: claude_session
    Status: idle
    Repository: api-refactor
    Branch: feature/new-auth
    User: dev
    Working Directory: /home/dev/projects/api-refactor
    PID: 12345
    Started: 2023-10-27T11:00:00Z
    Ended: 2023-10-27T11:15:30Z
    Duration: 15m30s

    Tool Statistics:
      Total Calls: 5
      Bash Commands: 2
      File Modifications: 3
      File Reads: 8
      Search Operations: 1
    ```

    The output includes the working directory, PID, user, timing, and a summary of tool usage.

3.  **Browse Sessions Interactively**

    The `sessions browse` command opens a terminal interface for filtering and exploring sessions.

    ```bash
    hooks sessions browse
    ```

    The interface provides keybindings for navigation and actions:
    *   **Type to filter:** Narrows the list by repository, branch, or user.
    *   **Press `Tab`:** Cycles through status filters (`running`, `idle`, `completed`, `failed`).
    *   **Press `Enter`:** Views the detailed information for the selected session.

## Example 3: Integration with Grove Flow

This example shows how `grove-hooks` tracks automated jobs orchestrated by `grove-flow`.

1.  **Create and Run a Grove Flow Plan**

    Use `grove-flow` to define and execute a multi-step plan. `grove-flow` is configured to emit start and stop events to `grove-hooks` for each job.

    ```bash
    # In a project configured for grove-flow
    # Initialize a new plan
    flow plan init new-feature-docs

    # Add a 'oneshot' job to generate documentation
    flow plan add new-feature-docs --title "Generate API Docs" --type oneshot \
      -p "Generate OpenAPI documentation for the new endpoints in user_controller.go"

    # Run the plan
    flow plan run
    ```

2.  **View the Unified Session List**

    While the `flow` plan is running or after it has finished, use `hooks` to view the activity.

    ```bash
    hooks sessions list
    ```

    **Expected Output:**

    ```
    SESSION ID       TYPE      STATUS       CONTEXT                     USER    STARTED               DURATION    IN STATE
    job-gen-api-...  job       completed    new-feature-docs            dev     2023-10-27 12:05:10   1m5s        1m5s
    claude-axb...    claude    completed    my-project/main             dev     2023-10-27 10:30:05   25m10s      25m10s
    ```

    The list includes entries for both interactive (`claude`) and automated (`job`) sessions. For `job` type entries, the `CONTEXT` column displays the `grove-flow` plan name. This provides a single interface to monitor all AI-driven activity in a project.
