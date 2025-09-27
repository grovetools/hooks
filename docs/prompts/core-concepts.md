# Documentation Generation Task: Core Concepts

You are documenting the fundamental concepts of `grove-hooks`. Based on the entire codebase, explain the following core concepts:
- **Local-First Storage**: Explain that all data is stored in a local SQLite database (`~/.local/share/grove-hooks/state.db`). Reference `internal/storage/disk/sqlite.go`. Emphasize the benefits: offline access, privacy, and speed.
- **Session Lifecycle**: Describe how a session is tracked from start to finish. A session can be a `claude_session` or a `oneshot_job`. Explain the different statuses (`running`, `idle`, `completed`, `failed`) and how they are updated by the hooks.
- **Hooks**: Explain that `grove-hooks` integrates with Claude Code by being invoked as hooks (`pretooluse`, `posttooluse`, `stop`, etc.). Reference `main.go` and `internal/hooks/hooks.go`. Explain that these hooks are responsible for capturing events and updating the database.
- **Oneshot Jobs**: Describe how `grove-hooks` also tracks non-interactive jobs from `grove-flow`. Reference `internal/commands/oneshot.go`. Explain that `grove-flow` calls `grove-hooks oneshot start` and `grove-hooks oneshot stop` to manage the job lifecycle.