# Documentation Generation Task: Architecture Overview

You are an expert technical writer providing a high-level overview of the `grove-hooks` architecture.

Using the entire codebase as context, describe the following components and their interactions:

- **Entrypoints**: Explain the dual nature of the binary. How `main.go` routes to either a hook implementation (when called via symlink like `stop`) or to a Cobra command (when called as `grove-hooks`).
- **Hook Handlers**: Briefly describe the role of the functions in `internal/hooks/hooks.go`. Explain that they parse JSON from stdin, interact with the storage layer, and sometimes output JSON to stdout.
- **CLI Commands**: Describe the role of the files in `internal/commands/`. Explain that these are user-facing commands for querying and managing the data collected by the hooks.
- **Storage Layer**: Explain the SQLite database as the central state store. Reference `internal/storage/disk/sqlite.go` and the schema it defines. Mention it's designed to be purely local.
- **Ecosystem Interaction**: Use text to illustrate how `Claude Code` and `grove-flow` invoke `grove-hooks`.