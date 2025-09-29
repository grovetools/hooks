This document provides practical examples to demonstrate how to use `grove-hooks` for observability, from basic session monitoring to integrated workflow analysis.

## Example 1: Basic Session Monitoring

This example demonstrates the fundamental workflow of integrating `grove-hooks` with a project using the Claude CLI and monitoring an interactive development session.

1.  **Install Hooks in Your Project**

    Navigate to your project's root directory and run the `install` command. This configures the Claude CLI to automatically send lifecycle events to `grove-hooks`.

    ```bash
    # In your project's root directory
    grove-hooks install
    ```

    This command finds or creates the `.claude/settings.local.json` file and injects the necessary hook configurations, such as `PreToolUse`, `PostToolUse`, and `Stop`.

2.  **Start a Claude Session**

    Begin a development session with Claude as you normally would. For example:

    ```bash
    claude -p "Refactor the main database connection logic."
    ```

    As soon as the session starts, `grove-hooks` creates a new record in its local SQLite database to track it.

3.  **List Active Sessions**

    While the Claude session is running, open another terminal and use `grove-hooks sessions list` to see all tracked activity. The `--active` flag is a convenient way to hide completed or failed sessions.

    ```bash
    grove-hooks sessions list --active
    ```

    **Expected Output:**

    ```
    SESSION ID      TYPE      STATUS     CONTEXT              USER    STARTED               DURATION    IN STATE
    claude-axb...   claude    running    my-project/main      dev     2023-10-27 10:30:05   running     2m15s
    ```

    **Observability Insights:**
    *   **Real-time Status:** You can immediately see that a `claude` session is `running`.
    *   **Context at a Glance:** The `CONTEXT` column shows the repository (`my-project`) and branch (`main`), providing clear context for the work being done.
    *   **Timeliness:** The `IN STATE` column shows how long the session has been in its current state, which is useful for identifying stalled or unexpectedly long-running tasks.

## Example 2: Advanced Analysis and Debugging

This example shows how to use `grove-hooks` to investigate a specific session, which is useful for debugging or reviewing the history of an agent's actions.

1.  **Find a Specific Session**

    Imagine a session ended unexpectedly. You can filter the session list by status to find it.

    ```bash
    grove-hooks sessions list --status idle
    ```

    **Expected Output:**

    ```
    SESSION ID      TYPE      STATUS     CONTEXT               USER    STARTED               DURATION    IN STATE
    claude-cyz...   claude    idle     api-refactor/feat...  dev     2023-10-27 11:00:00   15m30s      15m30s
    ```

2.  **Get Detailed Session Information**

    Use the `sessions get` command with the session ID to retrieve the full record for that session, including tool usage statistics.

    ```bash
    grove-hooks sessions get claude-cyz...
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

    **Observability Insights:**
    *   **Detailed Context:** You get the exact working directory, PID, and user associated with the session.
    *   **Tool Usage Breakdown:** The `Tool Statistics` section provides a summary of the agent's actions, helping you understand what it was trying to do. 

3.  **Browse Sessions Interactively**

    For a more fluid investigation, the `sessions browse` command launches a terminal UI that allows for real-time filtering and exploration.

    ```bash
    grove-hooks sessions browse
    ```

    Within this interface, you can:
    *   **Type to filter:** Instantly narrow down the list by repository, branch, or user.
    *   **Press `Tab`:** Cycle through status filters (`running`, `idle`, `completed`, `failed`).
    *   **Press `Enter`:** View the same detailed information provided by the `get` command.

## Example 3: Integration with Grove Flow

A key feature of `grove-hooks` is its ability to provide unified observability across the entire Grove ecosystem. This example shows how it automatically tracks automated jobs orchestrated by `grove-flow`.

1.  **Create and Run a Grove Flow Plan**

    First, use `grove-flow` to define and execute a multi-step plan. `grove-flow` is configured to automatically emit start and stop events to `grove-hooks` for each job it runs.

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

    While the `flow` plan is running (or after it has finished), use `grove-hooks` to view the activity.

    ```bash
    grove-hooks sessions list
    ```

    **Expected Output:**

    ```
    SESSION ID       TYPE      STATUS       CONTEXT                     USER    STARTED               DURATION    IN STATE
    job-gen-api-...  job       completed    new-feature-docs            dev     2023-10-27 12:05:10   1m5s        1m5s
    claude-axb...    claude    completed    my-project/main             dev     2023-10-27 10:30:05   25m10s      25m10s
    ```

    **Observability Insights:**
    *   **Unified View:** The list now includes both interactive `claude` sessions and automated `job` sessions from `grove-flow`. This provides a single place to monitor all AI-driven activity in your project.
    *   **Job-Specific Context:** For the `job` session, the `CONTEXT` column displays the name of the `grove-flow` plan (`new-feature-docs`), distinguishing it from the repository/branch context of a Claude session.
    *   **End-to-End Tracking:** You can trace a feature's lifecycle from an interactive `claude` session where it was developed to the automated `job` that generated its documentation, all within the same observability tool.
