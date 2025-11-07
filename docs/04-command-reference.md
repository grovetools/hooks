# Command Reference

This document provides a reference for the `grove-hooks` command-line interface, covering all subcommands and their options.

## Hook Execution Mechanism

`grove-hooks` is an integration tool for the Claude Code agent. Its functions are executed in two ways:

1.  **Via Symlinks:** When installed in a repository, `grove-hooks` is called by the Claude agent through symlinks (e.g., `pretooluse`, `stop`). The binary inspects how it was called (`os.Args[0]`) and runs the corresponding hook logic, receiving a JSON payload on standard input.
2.  **Directly:** Users can run `grove-hooks` with subcommands (e.g., `grove-hooks sessions list`) to manage and inspect the data collected by the hooks.

## `grove-hooks install`

Initializes the hook configuration within a repository.

### Syntax

```bash
grove-hooks install [flags]
```

### Description

The `install` command configures a repository to use `grove-hooks`. It creates a `.claude` directory if one does not exist and then creates or updates the `.claude/settings.local.json` file. This file is modified to direct Claude Code's hook events (such as tool usage and session completion) to the `grove-hooks` binary.

Existing settings in the file are preserved, but the `hooks` section is overwritten with the `grove-hooks` configuration.

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

Manages and queries locally stored AI agent sessions. Session data is stored in a local SQLite database located at `~/.local/share/grove-hooks/state.db`.

### `grove-hooks sessions list`

Lists all tracked sessions in a table format.

#### Syntax

```bash
grove-hooks sessions list [flags]
```

#### Description

This command displays a summary of all sessions recorded by `grove-hooks`, including interactive Claude sessions and automated `grove-flow` jobs. It discovers sessions from multiple sources: `grove-flow` markdown files, live session directories (`~/.grove/hooks/sessions/`), and the local SQLite database. The output is sorted to show active sessions first.

#### Flags

| Flag | Shorthand | Description |
| :--- | :--- | :--- |
| `--active` | | Show only active sessions (hides `completed`, `failed`, `error`). |
| `--json` | | Output the full session data as a structured JSON array. |
| `--limit` | `-l` | Limit the number of sessions returned. |
| `--status` | `-s` | Filter by status (e.g., `running`, `idle`, `completed`). |
| `--plan` | `-p` | Filter by `grove-flow` plan name. |
| `--type` | `-t` | Filter by type (e.g., `claude_code`, `chat`, `oneshot`, `agent`). |

#### Example

```bash
# List the 10 most recent active sessions
grove-hooks sessions list --active --limit 10
```

#### Example Output

```
SESSION ID    TYPE           STATUS        CONTEXT                         USER    AGE
<uuid>...     claude_code    running       my-repo (wt:feature-branch)     matt    3m10s
<uuid>...     job            completed     my-project:Refactor Database    matt    5s
```

- **CONTEXT**: Shows repository and worktree for Claude sessions, or project and job title for `grove-flow` jobs.
- **AGE**: For active sessions, this is the time since last activity. For terminal sessions, it is the total execution duration.

### `grove-hooks sessions get`

Retrieves and displays detailed information for a specific session.

#### Syntax

```bash
grove-hooks sessions get <session-id> [flags]
```

#### Description

Use this command to inspect the full details of a single session, including its type, status, repository context, timing information, and collected tool usage statistics.

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
grove-hooks sessions get 018f2b3e-a1b2-c3d4-e5f6-123456789abc
```

### `grove-hooks sessions tui`

Launches an interactive terminal UI to browse, search, and manage sessions.

#### Syntax

```bash
grove-hooks sessions tui
```
*Aliases: `browse`, `b`*

#### Description

The `tui` command provides a full-screen, real-time view of all sessions, grouped by workspace. It allows for dynamic filtering, searching, and inspecting sessions. Data is refreshed automatically.

#### Keybindings

| Key | Action |
| :--- | :--- |
| `↑`/`↓` | Navigate the session list. |
| `/` | Start typing to filter sessions by any text content. `Esc` to exit search. |
| `f` | Open the filter view to toggle statuses and types. |
| `t` | Toggle between table and tree views. |
| `Enter`/`o`/`e` | Perform context-aware action (view details, open session, edit job). |
| `Space` | Select or deselect one or more sessions. |
| `Ctrl+A` | Select or deselect all currently visible sessions. |
| `Ctrl+X` | Archive all selected sessions. |
| `Ctrl+Y` | Copy the selected session ID to the clipboard. |
| `Ctrl+O` | Open the session's working directory in the file manager. |
| `Ctrl+J` | Export the selected session's data to a JSON file in the current directory. |
| `Ctrl+K` | Kill the selected Claude session process. |
| `q`/`Esc`/`Ctrl+C` | Exit the application. |

### `grove-hooks sessions cleanup`

Manually triggers a cleanup of inactive or dead sessions.

#### Syntax

```bash
grove-hooks sessions cleanup [flags]
```

#### Description

This command finds sessions that are marked as `running` or `idle` but whose corresponding process has terminated or has been inactive for an extended period. It marks these sessions as `completed` or `interrupted`. This process runs automatically with `list` and `tui`, but can be invoked manually if needed.

#### Flags

| Flag | Description | Default |
| :--- | :--- | :--- |
| `--inactive-minutes` | Minutes of inactivity before marking a session as completed. | `30` |

### `grove-hooks sessions archive`

Archives one or more sessions by marking them as deleted in the database.

#### Syntax
```bash
grove-hooks sessions archive [session-id...] [flags]
```

#### Description
Archived sessions are hidden from normal queries. You can archive specific sessions by providing their IDs, or use flags to archive sessions in bulk based on their status.

#### Flags
| Flag | Description |
| :--- | :--- |
| `--all` | Archive all sessions regardless of status. |
| `--completed` | Archive only completed sessions. |
| `--failed` | Archive only failed sessions. |
| `--running` | Archive only running sessions. |
| `--idle` | Archive only idle sessions. |

### `grove-hooks sessions kill`

Terminates a running Claude session process.

#### Syntax
```bash
grove-hooks sessions kill <session-id> [flags]
```
#### Description
This command sends a `SIGTERM` signal to the process associated with a Claude session and cleans up its session directory. This action is immediate and cannot be undone. It does not apply to `grove-flow` jobs.

#### Flags
| Flag | Shorthand | Description |
| :--- | :--- | :--- |
| `--force` | `-f` | Skip the confirmation prompt before killing the process. |

### `grove-hooks sessions mark-interrupted`
Finds and marks stale `grove-flow` jobs as interrupted.

#### Syntax
```bash
grove-hooks sessions mark-interrupted [flags]
```
#### Description
This utility scans for `grove-flow` job files (`.md`) with a status of `running`. If a job's corresponding `.lock` file is missing or contains the PID of a dead process, it updates the job's frontmatter status to `interrupted`.

#### Flags
| Flag | Description |
| :--- | :--- |
| `--dry-run` | Show which job files would be updated without making changes. |

### `grove-hooks sessions mark-old-completed`
Bulk-updates the status of old jobs to `completed`.

#### Syntax
```bash
grove-hooks sessions mark-old-completed [flags]
```
#### Description
This command finds `grove-flow` jobs and chats that are not in a terminal state (e.g., `running`, `idle`) and were created before a specified date. It then updates their frontmatter status to `completed`.

#### Flags
| Flag | Description | Default |
| :--- | :--- | :--- |
| `--before` | Mark jobs created before this date (YYYY-MM-DD). | Today |
| `--dry-run` | Show which job files would be updated without making changes. | |

### `grove-hooks sessions set-status`
Directly sets the status field in a `grove-flow` job's frontmatter.

#### Syntax
```bash
grove-hooks sessions set-status <job-file-path> <status>
```
#### Arguments
| Argument | Description |
| :--- | :--- |
| `<job-file-path>` | Path to the job's `.md` file. |
| `<status>` | The new status to set (e.g., `pending`, `running`, `completed`). |

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

## Programmatic Hook Commands

These subcommands are the direct entry points for Claude's hook system and are not intended for direct user interaction. They expect a JSON payload on standard input.

| Command | Trigger | Purpose |
| :--- | :--- | :--- |
| `pretooluse` | Before a tool is executed. | Creates session record, logs tool attempt. |
| `posttooluse`| After a tool is executed. | Updates tool execution record with result and duration. |
| `notification`| On an agent notification. | Logs notification events. |
| `stop` | When an agent session ends a turn or completes. | Updates session status to `idle` or `completed`. |
| `subagent-stop`| When a sub-agent completes a task. | Logs sub-agent task completion. |

## Environment Variables

| Variable | Description |
| :--- | :--- |
| `GROVE_HOOKS_DB_PATH` | Specifies a custom path to the SQLite database file. |
| `GROVE_DEBUG` | If set, enables additional verbose logging to stderr. |
| `XDG_DATA_HOME` | If set, the database and session directories will be stored in `$XDG_DATA_HOME/grove-hooks/` instead of `~/.local/share/grove-hooks/`. |