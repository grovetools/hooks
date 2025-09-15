# Grove Hooks

<img src="https://github.com/user-attachments/assets/eda77869-0e99-467c-a20d-4d5b8262aecf" width="80%" /> 

---

[![CI](https://github.com/mattsolo1/grove-hooks/actions/workflows/ci.yml/badge.svg)](https://github.com/mattsolo1/grove-hooks/actions/workflows/ci.yml)

Grove Hooks is a local-first observability and state management tool for AI agent sessions and API requests within the Grove ecosystem. Its hook system captures information about tool usage, session lifecycle, and notifications, storing everything in a local SQLite database for fast, offline-first access.

It provides a command-line interface and an interactive terminal UI to query, browse, and manage both interactive Claude sessions and `grove-flow` jobs.

## Key Features

- **Local-First Session Tracking:** All session data is stored in a local SQLite database (`~/.local/share/grove-hooks/state.db`), requiring no network access.
- **Claude Code Integration:** An `install` command automatically configures the necessary hooks in your repository's `.claude/settings.local.json`.
- **CLI interface:** Query and manage sessions with commands to `list`, `get`, `browse`, and `cleanup`.
- **Interactive Session Browser:** A terminal-based UI (`sessions browse`) to filter, search, and inspect sessions in real-time.
- **Oneshot Job Support:** Tracks the lifecycle of API requests executed by `grove-flow`, providing unified observability for both interactive and automated tasks.
- **System Notifications:** Sends native desktop notifications for important events like errors and warnings.

## Dependencies

Claude Code (optional)

`grove-flow` (optional)

## Installation

Todo

## Getting Started: Integration with Claude

To integrate `grove-hooks` with your project, navigate to your repository's root directory and run the `install` command.

```bash
cd /path/to/your/project
grove-hooks install
```

This command will:
1.  Create a `.claude` directory if one does not exist.
2.  Create or update the `.claude/settings.local.json` file.
3.  Inject the necessary hook configurations, pointing them to the `grove-hooks` binary. It preserves any other existing settings in the file.

After installation, the Claude CLI will automatically invoke `grove-hooks` at different stages of a session, populating the local database with observability data.

## Command Reference

### `grove-hooks sessions`

The `sessions` command is the main entrypoint for managing and viewing session data.

#### `sessions list`

List all tracked sessions in a table format.

```bash
grove-hooks sessions list
```

Example Output:
```
SESSION ID    TYPE    STATUS      CONTEXT              USER    STARTED               DURATION    IN STATE
test-job-1... job     completed   test-plan            matt    2023-10-27 10:30:05   1s          1s
claude-se...  claude  running     grove-hooks/main     matt    2023-10-27 10:29:50   running     15s
```

- **CONTEXT:** Shows the repository and branch for Claude sessions, or the plan/job title for oneshot jobs.
- **IN STATE:** Shows the time elapsed in the current status (e.g., how long it's been `running` or `idle`).

**Flags:**
- `--status <status>`: Filter by status (e.g., `running`, `idle`, `completed`, `failed`).
- `--active`: A shorthand to show only active sessions (hides `completed`, `failed`, etc.).
- `--limit <n>`: Limit the number of results.
- `--json`: Output the full session data as JSON.

---

#### `sessions get <session-id>`

View detailed information for a specific session.

```bash
grove-hooks sessions get <session-id>
```

**Flags:**
- `--json`: Output the details as a JSON object.

---

#### `sessions browse`

Launch an interactive terminal UI to browse, search, and manage sessions.

```bash
grove-hooks sessions browse
```

**Keybindings:**
- **Arrow Keys (↑/↓):** Navigate the session list.
- **Type to Filter:** Instantly filter sessions by repo, branch, user, or ID.
- **Tab:** Cycle through status filters (`all` -> `running` -> `idle` -> `completed` -> `failed`).
- **Enter:** View detailed information for the selected session.
- **Space:** Select/deselect one or more sessions.
- **Ctrl+A:** Select/deselect all currently visible sessions.
- **Ctrl+X:** Archive all currently selected sessions (removes them from view).
- **Ctrl+Y:** Copy the selected session ID to the clipboard.
- **Ctrl+O:** Open the session's working directory in your file manager.
- **Esc / Ctrl+C:** Exit the browser.

---

#### `sessions cleanup`

Marks inactive or dead sessions as `completed`. This is run automatically by `sessions list` and `sessions browse`, but can be triggered manually.

```bash
grove-hooks sessions cleanup --inactive-minutes 60
```

