# Documentation Generation Task: Configuration

You are an expert technical writer documenting the configuration options for `grove-hooks`. Based on the codebase, explain the following configuration files:

**1. Claude Code Integration (`.claude/settings.local.json`)**:
   - Explain that this file is the primary integration point for Claude Code.
   - State that it's automatically managed by the `grove-hooks install` command.
   - Show an example of the `hooks` section that `grove-hooks` adds.

**2. Repository-Specific Hooks (`.canopy.yaml`)**:
   - Reference `internal/hooks/hooks.go` (specifically the `RunStopHook` and `ExecuteRepoHookCommands` functions).
   - Explain that `grove-hooks` can execute custom commands defined in a `.canopy.yaml` file at the root of a repository when a session stops.
   - Document the `hooks.on_stop` section.
   - Explain the `name`, `command`, and `run_if: changes` options.
   - Provide an example of a `.canopy.yaml` that runs a linter only if there are git changes.

**3. Database Path (Environment Variable)**:
   - Mention that the database path can be overridden for testing purposes using the `GROVE_HOOKS_DB_PATH` environment variable, as seen in `internal/storage/disk/sqlite.go`.