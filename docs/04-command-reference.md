# Command Reference

This document provides a reference for the `grove-hooks` command-line interface, covering all subcommands and their options.

## `grove-hooks install`

Initializes the hook configuration within a repository.

### Syntax

```bash
grove-hooks install [flags]
```

### Description

The `install` command configures the current repository to use `grove-hooks` for observability. It creates a `.claude` directory if one does not exist and creates or updates the `.claude/settings.local.json` file. This file is modified to direct Claude Code's hook events (such as tool usage and session completion) to the `grove-hooks` binary.

Existing settings in the file that do not conflict with the hooks configuration are preserved.

### Flags

| Flag | Shorthand | Description | Default |
| :--- | :--- | :--- | :--- |
| `--directory` | `-d` | The target repository directory to install hooks in. | `.` |

### Example

```bash
# Install hooks in the current directory
grove-hooks install

# Install hooks in a specific project directory
grove-hooks install --directory ~/projects/my-app
```

## `grove-hooks sessions`

Manages and queries locally stored AI agent sessions.

### `grove-hooks sessions list`

Lists all tracked sessions in a table format.

#### Syntax

```bash
grove-hooks sessions list [flags]
```

#### Description

This command displays a summary of all sessions recorded by `grove-hooks`, including both interactive Claude sessions and automated `grove-flow` jobs. It automatically cleans up any dead or zombie sessions before displaying the list. The output is sorted to show active sessions first.

#### Flags

| Flag | Shorthand | Description |
| :--- | :--- | :--- |
| `--active` | | A shorthand to show only active sessions (hides `completed`, `failed`). |
| `--json` | | Output the full session data as a structured JSON array. |
| `--limit` | `-l` | Limit the number of sessions returned. |
| `--status` | `-s` | Filter the list by a specific status (e.g., `running`, `idle`, `completed`). |
| `--plan` | `-p` | Filter by plan name. |
| `--type` | `-t` | Filter by session type (`claude`, `job`). |

#### Example

```bash
# List the 10 most recent active sessions
grove-hooks sessions list --active --limit 10
```

#### Example Output

```
SESSION ID    TYPE    STATUS      CONTEXT              USER    STARTED               DURATION    IN STATE
test-job-1... job     completed   test-plan            matt    2023-10-27 10:30:05   1s          1s
claude-se...  claude  running     grove-hooks/main     matt    2023-10-27 10:29:50   running     15s
```

- **CONTEXT**: Shows the repository and branch for Claude sessions, or the plan name for `grove-flow` jobs.
- **IN STATE**: The time elapsed in the current status (e.g., how long a session has been `running`).

### `grove-hooks sessions get`

Retrieves and displays detailed information for a specific session.

#### Syntax

```bash
grove-hooks sessions get <session-id> [flags]
```

#### Description

Use this command to inspect the full details of a single session, including its type, status, repository context, timing information, and any collected tool usage statistics.

#### Arguments

| Argument | Description |
| :--- | :--- |
| `<session-id>` | The unique identifier of the session. |

#### Flags

| Flag | Description |
| :--- | :--- |
| `--json` | Output the session details as a JSON object. |

#### Example

```bash
grove-hooks sessions get claude-session-abcdef123456
```

### `grove-hooks sessions browse`

Launches an interactive terminal UI to browse, search, and manage sessions.

#### Syntax

```bash
grove-hooks sessions browse
```

#### Description

The `browse` command provides a full-screen, real-time view of all sessions. It allows for dynamic filtering, searching, and inspecting sessions without leaving the terminal. Data is refreshed automatically every second.

#### Keybindings

| Key | Action |
| :--- | :--- |
| `↑`/`↓` | Navigate the session list. |
| `(type to filter)`| Instantly filter sessions by any text content. |
| `Tab` | Cycle through status filters (`all`, `running`, etc.). |
| `Enter` | View detailed information for the selected session. |
| `Space` | Select or deselect one or more sessions. |
| `Ctrl+A` | Select or deselect all currently visible sessions. |
| `Ctrl+X` | Archive all selected sessions, removing them from view. |
| `Ctrl+Y` | Copy the selected session ID to the clipboard. |
| `Ctrl+O` | Open the session's working directory in your file manager. |
| `Ctrl+J` | Export the selected session's data to a JSON file. |
| `Esc`/`Ctrl+C` | Exit the browser. |

### `grove-hooks sessions cleanup`

Manually triggers a cleanup of inactive or dead sessions.

#### Syntax

```bash
grove-hooks sessions cleanup [flags]
```

#### Description

This command finds sessions that are marked as `running` or `idle` but whose corresponding process has terminated or has been inactive for an extended period. It marks these sessions as `completed`. This process runs automatically with `list` and `browse`, but can be invoked manually if needed.

#### Flags

| Flag | Description | Default |
| :--- | :--- | :--- |
| `--inactive-minutes` | The number of minutes a session must be inactive to be cleaned up. | `30` |

## `grove-hooks oneshot`

Provides an integration point for `grove-flow` to track the lifecycle of automated jobs. This command is intended for programmatic use, not direct user interaction.

### `grove-hooks oneshot start`

Receives a JSON payload via standard input to signal the beginning of a `grove-flow` job. It creates a new session record in the database with a type of `oneshot_job`.

### `grove-hooks oneshot stop`

Receives a JSON payload via standard input to signal the end of a `grove-flow` job. It updates the corresponding session's status to `completed` or `failed`.

## `grove-hooks version`

Prints the version information for the `grove-hooks` binary.

### Syntax

```bash
grove-hooks version [flags]
```

### Flags

| Flag | Description |
| :--- | :--- |
| `--json` | Output the version information as a JSON object. |

## Environment Variables

| Variable | Description |
| :--- | :--- |
| `GROVE_HOOKS_DB_PATH` | Specifies a custom path to the SQLite database file for testing or alternate configurations. |
| `GROVE_DEBUG` | If set, enables additional verbose logging for debugging. |
