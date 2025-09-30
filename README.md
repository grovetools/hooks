<!-- DOCGEN:OVERVIEW:START -->

<img src="docs/images/grove-hooks.svg" width="60%" />

Grove Hooks captures information about local AI agent sessions and automated job lifecycles, storing all data in a local SQLite database. 

<!-- placeholder for animated gif -->

## Key Features

*   **Local-First Tracking**: All session and event data is stored in a local SQLite database (`~/.local/share/grove-hooks/state.db`).

*   **Unified Session Monitoring**: It tracks both interactive AI agent sessions (e.g., from Claude Code) and automated `oneshot` jobs from `grove-flow`, presenting them in a single, consistent interface.

*   **State Management**: The tool captures the complete lifecycle of a session, from start to finish, including statuses like `running`, `idle`, `completed`, and `failed`. It also automatically cleans up dead or inactive sessions.

*   **Interactive TUI**: The `hooks sessions browse` command launches a terminal-based UI for interactively filtering, searching, and inspecting session data in real-time.

*   **AI Agent Integration**: An `install` command automates the integration with tools like Claude Code by configuring the necessary hooks in the local project settings.

*   **System Notifications**: It can deliver native desktop notifications for important events, such as job failures or tasks requiring user input, providing timely feedback on background processes.

## Ecosystem Integration

Grove Hooks functions as the central observability layer within the Grove tool suite, integrating with other components to provide a cohesive development experience.

*   **Grove Flow (`flow`)**: When `grove-flow` executes `oneshot` jobs, it communicates with Grove Hooks to signal the start and stop of each job's lifecycle. This allows developers to monitor the progress of automated plans, view their status, and diagnose failures using the same tools they use for interactive sessions.

*   **Claude Code**: Grove Hooks integrates with Claude's hook system. By running `grove-hooks install`, the tool configures the local repository to automatically report events such as tool usage, notifications, and session termination to the Grove Hooks database.

*   **Grove Meta-CLI (`grove`)**: The `grove` command manages the installation and versioning of the `grove-hooks` binary, ensuring it is available in the user's `PATH` and can be discovered by other ecosystem tools.

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

<!-- DOCGEN:OVERVIEW:END -->

## Documentation

See the [documentation](docs/) for detailed usage instructions:
- [Overview](docs/01-overview.md) - Introduction and core concepts
- [Examples](docs/02-examples.md) - Common usage patterns
- [Configuration](docs/03-configuration.md) - Configuration reference
- [Command Reference](docs/04-command-reference.md) - Complete CLI reference


<!-- DOCGEN:TOC:START -->

See the [documentation](docs/) for detailed usage instructions:
- [Overview](docs/01-overview.md)
- [Examples](docs/02-examples.md)
- [Configuration](docs/03-configuration.md)
- [Command Reference](docs/04-command-reference.md)

<!-- DOCGEN:TOC:END -->
