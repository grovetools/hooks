Grove Hooks is built upon a few fundamental concepts that enable its local-first observability and state management capabilities. Understanding these concepts is key to effectively using the tool and integrating it within the Grove ecosystem.

## Local-First Storage

All data captured by `grove-hooks` is stored in a single SQLite database file located by default at `~/.local/share/grove-hooks/state.db`. This design choice is central to the tool's philosophy and provides several key benefits:

*   **Offline Access:** Since the database is a local file, all `grove-hooks` commands function without requiring network access. You can browse, query, and manage session data from anywhere.
*   **Privacy:** Session data, which may include sensitive information from development work, remains entirely on your local machine.
*   **Speed:** Interacting with a local SQLite database is extremely fast, resulting in a responsive command-line and TUI experience.
*   **Simplicity:** The entire state is contained within one file, making it easy to back up, inspect with standard SQLite tools, or delete if necessary.

The storage layer, including the database schema and query logic, is primarily defined in `internal/storage/disk/sqlite.go`.

## Session Lifecycle

A "session" is the primary unit of tracking in `grove-hooks`. It represents a complete interaction, either with an AI agent or an automated job. The tool tracks two distinct types of sessions: `claude_session` for interactive work with Claude Code, and `oneshot_job` for automated tasks run by `grove-flow`.

Each session progresses through a lifecycle managed by its status. The primary statuses are:

*   **`running`**: The session is actively processing or awaiting tool execution.
*   **`idle`**: A Claude session is waiting for user input but has not been terminated.
*   **`completed`**: The session or job finished successfully.
*   **`failed`**: The session or job terminated due to an error.

The status of a session is updated automatically by the hooks as they are triggered. For example, the `pretooluse` hook ensures a session is marked as `running`, while the `stop` hook transitions it to either `idle` or `completed` based on the context.

## Hooks

`grove-hooks` integrates with Claude Code by acting as a set of command-line hooks. The binary is designed to be a single executable that behaves differently based on how it is invoked. As detailed in `main.go`, the application inspects the name used to execute it (e.g., `pretooluse`, `posttooluse`, `stop`).

When a user runs the `grove-hooks install` command, it configures the repository's `.claude/settings.local.json` to call the `grove-hooks` binary for various lifecycle events.

These hook handlers, implemented in `internal/hooks/hooks.go`, are the primary data collection mechanism. They typically perform the following actions:
1.  Read a JSON payload from standard input containing context about the event.
2.  Parse the payload to understand the event details.
3.  Interact with the local SQLite database to create, update, or query session data.
4.  For certain hooks like `pretooluse`, write a JSON response to standard output to communicate back to Claude Code.

## Oneshot Jobs

In addition to tracking interactive Claude sessions, `grove-hooks` provides state management for non-interactive, automated tasks executed by tools like `grove-flow`. These are referred to as "oneshot jobs".

Unlike the hook-based integration with Claude Code, `grove-flow` manages the job lifecycle by directly invoking the `grove-hooks` CLI with specific commands. The implementation for this is found in `internal/commands/oneshot.go`.

The typical interaction is as follows:
1.  Before executing a job, `grove-flow` calls `grove-hooks oneshot start`, passing a JSON payload with details about the job (e.g., ID, plan name, job title) via standard input. This creates a new session of type `oneshot_job` with a status of `running`.
2.  After the job finishes, `grove-flow` calls `grove-hooks oneshot stop` with a JSON payload indicating the final status (`completed` or `failed`) and any associated errors. This updates the session record accordingly.

This mechanism provides a unified view for both interactive AI sessions and automated backend jobs within the `grove-hooks` interface.