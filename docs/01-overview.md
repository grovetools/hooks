# Grove-Hooks: Local-First AI Observability

The `grove-hooks` repository provides an observability and state management tool designed for the Grove ecosystem. It offers developers a unified CLI-based view of all AI agent activity, capturing data from both interactive sessions and automated jobs in a local database.

The tool serves developers using Grove ecosystem tools like Claude Code for interactive development and `grove-flow` for automated tasks.

The `grove-hooks` system serves as both a passive data collection framework and an active analysis interface for understanding AI agent behavior patterns.

*   **Local SQLite Storage**: All session and event data is stored in a local SQLite database (`~/.local/share/grove-hooks/state.db`), enabling offline access and fast queries.
*   **Automatic Session Tracking**: It integrates with Claude Code to automatically capture the lifecycle of interactive sessions, including tool usage and notifications.
*   **`grove-flow` Job Monitoring**: It tracks the lifecycle of non-interactive "oneshot" jobs executed by `grove-flow`, providing a single interface for observing both manual and automated AI tasks.
*   **Interactive Terminal UI**: An interactive browser (`sessions browse`) allows developers to filter, search, and inspect session data directly from the terminal.
