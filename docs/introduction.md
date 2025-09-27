`grove-hooks` is a local-first observability and state management tool designed for the Grove ecosystem. It provides developers with a unified, local, and offline-capable view of all AI agent activity, capturing data from both interactive sessions and automated jobs.

The primary problem `grove-hooks` solves is the lack of a centralized, developer-controlled system for tracking the lifecycle and actions of AI agents. By storing all data in a local SQLite database, it ensures that observability is fast, private, and available without a network connection.

Its target audience is developers using tools within the Grove ecosystem, such as Claude Code for interactive development and `grove-flow` for automated tasks.

### Key Features

*   **Local SQLite Storage**: All session and event data is stored in a local SQLite database (`~/.local/share/grove-hooks/state.db`), enabling offline access and fast queries.
*   **Automatic Session Tracking**: It integrates with Claude Code to automatically capture the lifecycle of interactive sessions, including tool usage and notifications.
*   **`grove-flow` Job Monitoring**: It tracks the lifecycle of non-interactive "oneshot" jobs executed by `grove-flow`, providing a single interface for observing both manual and automated AI tasks.
*   **Interactive Terminal UI**: An interactive browser (`sessions browse`) allows developers to filter, search, and inspect session data directly from the terminal.