# Grove Hooks

<img src="./images/grove-hooks.svg" width="60%" />

Grove Hooks captures information about local AI agent sessions and automated job lifecycles, storing all data in a local SQLite database.

<!-- placeholder for animated gif -->

## Key Features

*   **Local Data Storage**: All session and event data is stored in a local SQLite database, typically at `~/.local/share/grove-hooks/state.db`.
*   **Session Monitoring**: Tracks interactive agent sessions (from tools like Claude Code) and automated `oneshot` jobs (from `grove-flow`) in a unified list.
*   **State Tracking**: Records the lifecycle status of a session, such as `running`, `idle`, `completed`, or `failed`, and includes a mechanism to clean up inactive or terminated sessions.
*   **Terminal Interface**: The `hooks sessions browse` command provides an interactive terminal UI for filtering, searching, and inspecting session data.
*   **Configuration Automation**: The `install` command modifies local project settings (e.g., `.claude/settings.local.json`) to integrate with AI agent CLIs.
*   **System Notifications**: Delivers desktop notifications for events like job failures or tasks that require user input.

## How It Works

Grove Hooks is a command-line binary that other tools execute at specific lifecycle events. For example, `grove-flow` calls `grove-hooks oneshot start` before executing a job and `grove-hooks oneshot stop` after it finishes.

On each execution, `grove-hooks` receives a JSON payload via standard input containing event details. It parses this data and writes or updates records in its SQLite database. The `install` command automates this process for tools like the Claude CLI by adding the necessary command calls to its local configuration file.

## Ecosystem Integration

Grove Hooks serves as an observability component within the Grove tool suite.

*   **Grove Flow (`flow`)**: When `grove-flow` runs `oneshot` jobs, it calls Grove Hooks to signal the start and stop of each job. This allows the progress and status of automated plans to be monitored.

*   **Claude Code**: The `grove-hooks install` command configures the local repository to report events such as tool usage, notifications, and session termination to the Grove Hooks database.

*   **Grove Meta-CLI (`grove`)**: The `grove` command manages the installation and versioning of the `grove-hooks` binary, making it available in the user's `PATH` for other tools to execute.

## Installation

Install via the Grove meta-CLI:
```bash
grove install hooks
```

Verify installation:
```bash
hooks version
```

Requires the `grove` meta-CLI. See the [Grove Installation Guide](https://github.com/mattsolo1/grove-meta/blob/main/docs/02-installation.md) if you don't have it installed.
