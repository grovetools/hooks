# Command Reference

This document provides a comprehensive reference for all commands available in the `grove-hooks` command-line interface (CLI).

## `grove-hooks install`

Installs or updates the necessary hook configurations into a project's local Claude Code settings.

**Signature**
```sh
grove-hooks install [flags]
```

**Description**

This command integrates `grove-hooks` with a repository for use with Claude Code. It locates the `.claude/settings.local.json` file (creating it if necessary) and injects the required hook definitions. It preserves any other existing settings in the file.

**Flags**

| Flag                   | Description                                  | Default |
| ---------------------- | -------------------------------------------- | ------- |
| `-d`, `--directory`    | The target directory for installation.       | `.`     |

**Example**

Install hooks into the current project repository:
```bash
grove-hooks install
```

Install hooks into a specific project directory:
```bash
grove-hooks install --directory /path/to/your/project
```

---

## `grove-hooks sessions`

The `sessions` command is the main entrypoint for managing and viewing session data for both Claude sessions and `grove-flow` jobs.

### `sessions list`

Lists all tracked sessions in a table format.

**Signature**
```sh
grove-hooks sessions list [flags]
```

**Description**

Displays a list of all sessions, with the most recent and active sessions shown first. The output includes session ID, type, status, context, user, and timing information. This command also automatically runs a cleanup of dead or timed-out sessions before displaying the list.

**Flags**

| Flag                   | Description                                                                   | Default |
| ---------------------- | ----------------------------------------------------------------------------- | ------- |
| `-s`, `--status`       | Filter the list by a specific status (e.g., `running`, `idle`, `completed`).  |         |
| `--json`               | Output the full session data as a JSON array.                                 | `false` |
| `-l`, `--limit`        | Limit the number of sessions returned.                                        | `0` (no limit) |
| `--active`             | A shorthand to show only active sessions (hides `completed`, `failed`, etc.). | `false` |

**Example**

List the 10 most recent active sessions:
```bash
grove-hooks sessions list --active --limit 10
```

List all failed sessions and output as JSON:
```bash
grove-hooks sessions list --status failed --json
```

### `sessions get`

Retrieves and displays detailed information for a specific session.

**Signature**
```sh
grove-hooks sessions get <session-id> [flags]
```

**Description**

Fetches all stored data for a single session, identified by its unique ID, and prints it in a human-readable format or as JSON.

**Flags**

| Flag     | Description                        | Default |
| -------- | ---------------------------------- | ------- |
| `--json` | Output the details as a JSON object. | `false` |

**Example**

Get details for a specific session ID:
```bash
grove-hooks sessions get test-job-12345678
```

### `sessions browse`

Launches an interactive terminal UI to browse, search, and manage sessions.

**Signature**
```sh
grove-hooks sessions browse [flags]
```

**Description**

Starts a full-screen terminal application that provides a real-time, filterable, and searchable view of all sessions. It automatically refreshes the list and allows for actions like viewing details, archiving, and copying session IDs.

**Flags**

| Flag       | Description                                                                   | Default |
| ---------- | ----------------------------------------------------------------------------- | ------- |
| `--active` | A shorthand to start the browser showing only active sessions.                | `false` |

**Example**

```bash
grove-hooks sessions browse
```

**Keybindings**

| Key(s)               | Action                                                      |
| -------------------- | ----------------------------------------------------------- |
| `↑` / `↓`            | Navigate the session list.                                  |
| `(type to filter)`   | Instantly filter sessions by any text (ID, repo, user, etc.). |
| `Tab`                | Cycle through status filters (`all` -> `running` -> `idle`...). |
| `Enter`              | View detailed information for the selected session.         |
| `Space`              | Select/deselect one or more sessions for bulk actions.      |
| `Ctrl+A`             | Select/deselect all currently visible sessions.             |
| `Ctrl+X`             | Archive all currently selected sessions.                    |
| `Ctrl+Y`             | Copy the selected session ID to the clipboard.              |
| `Ctrl+O`             | Open the session's working directory in your file manager.  |
| `Ctrl+J`             | Export the selected session's data to a JSON file.          |
| `Esc` / `Ctrl+C`     | Exit the browser.                                           |


### `sessions cleanup`

Manually triggers the cleanup process for inactive or dead sessions.

**Signature**
```sh
grove-hooks sessions cleanup [flags]
```

**Description**

Checks all `running` and `idle` sessions. If the process associated with a session (by PID) no longer exists, or if a session has been inactive for a specified duration, it is marked as `completed`. This process runs automatically before `sessions list` and `sessions browse`.

**Flags**

| Flag                   | Description                                                      | Default |
| ---------------------- | ---------------------------------------------------------------- | ------- |
| `--inactive-minutes`   | Minutes of inactivity before a session is marked as completed.   | `30`    |

**Example**

Clean up sessions that have been inactive for over an hour:
```bash
grove-hooks sessions cleanup --inactive-minutes 60
```

---

## `grove-hooks oneshot`

Manages the lifecycle of non-interactive "oneshot" jobs, typically executed by `grove-flow`. These commands are not intended for direct user interaction and expect a JSON payload from standard input.

### `oneshot start`

Signals the start of a oneshot job and creates a new session record.

**Signature**
```sh
grove-hooks oneshot start
```

**Description**

Reads a JSON payload from stdin containing details about a job (ID, plan name, title, etc.) and creates a new record in the database with a status of `running`.

**Example (for scripting)**

```bash
echo '{
  "job_id": "job-abc-123",
  "plan_name": "daily-report",
  "job_title": "Generate Sales Report"
}' | grove-hooks oneshot start
```

### `oneshot stop`

Signals the end of a oneshot job and updates its final status.

**Signature**
```sh
grove-hooks oneshot stop
```

**Description**

Reads a JSON payload from stdin containing the job ID and its final status (`completed` or `failed`) and updates the corresponding session record in the database.

**Example (for scripting)**

```bash
echo '{
  "job_id": "job-abc-123",
  "status": "completed"
}' | grove-hooks oneshot stop
```

---

## `grove-hooks version`

Prints the version information for the `grove-hooks` binary.

**Signature**
```sh
grove-hooks version [flags]
```

**Description**

Displays build information for the executable, including the version, git commit, and build date.

**Flags**

| Flag     | Description                                | Default |
| -------- | ------------------------------------------ | ------- |
| `--json` | Output version information in JSON format. | `false` |

**Example**

```bash
grove-hooks version
```