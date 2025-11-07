# Grove Hooks

Grove Hooks is a command-line tool that captures lifecycle events from local AI agent sessions and automated jobs, storing the data in a local SQLite database.

<!-- placeholder for animated gif -->

## Key Features

*   **Local Data Storage**: All event data is written to a local SQLite database, located by default at `~/.local/share/grove-hooks/state.db`.
*   **Session Tracking**: Records state for interactive agent sessions and for automated jobs executed by `grove-flow`.
*   **State Management**: Records the status of a session (`running`, `idle`, `completed`, `failed`) and provides commands to clean up inactive or terminated sessions.
*   **Terminal Interface**: The `hooks sessions browse` command launches a terminal UI for filtering, searching, and viewing session data.
*   **Configuration**: An `install` command modifies project-local configuration files (e.g., `.claude/settings.local.json`) to add hook execution calls.
*   **System Notifications**: Can be configured to deliver desktop notifications for events, such as when a background job requires user input.

## How It Works

Grove Hooks is a command-line binary executed as a subprocess by other tools at specific lifecycle events. It is not a long-running daemon.

On each execution, `grove-hooks` reads a JSON payload from standard input that contains event details. It parses this data to determine the event type, then writes or updates corresponding records in its SQLite database before exiting.

## Ecosystem Integration

Grove Hooks functions as an observability component within the Grove tool suite.

*   **Grove Flow (`flow`)**: When configured, `grove-flow` executes `grove-hooks` as a subprocess to signal the start and stop of automated jobs, allowing their status to be tracked.

*   **Claude Code**: The `grove-hooks install` command adds hook configurations to a repository's `.claude/settings.local.json` file. This causes the Claude CLI to report events like tool usage, notifications, and session termination to the Grove Hooks database.

*   **Grove Meta-CLI (`grove`)**: The `grove` command manages the installation and versioning of the `grove-hooks` binary, placing it in a location on the user's `PATH` where other tools can execute it.

### Installation

Install via the Grove meta-CLI:
```bash
grove install hooks
```

Verify installation:
```bash
hooks version
```

Requires the `grove` meta-CLI. See the [Grove Installation Guide](https://github.com/mattsolo1/grove-meta/blob/main/docs/02-installation.md) if you don't have it installed.