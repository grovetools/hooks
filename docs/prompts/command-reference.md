# Documentation Generation Task: Command Reference

You are an expert technical writer creating a comprehensive command reference for the `grove-hooks` CLI. Analyze the `internal/commands/` directory and `main.go` to document every command and its subcommands.

For each command, provide:
- The full command signature (e.g., `grove-hooks sessions list [flags]`).
- A clear description of what it does.
- All available flags, their purpose, and default values if applicable.
- Example usage in a code block.

Structure the document with clear headings for each main command (`install`, `sessions`, `oneshot`, `version`). Under `sessions` and `oneshot`, create subheadings for their respective subcommands (`list`, `get`, `browse`, `cleanup`, `start`, `stop`).

Pay special attention to the `sessions browse` command, listing its keybindings as described in `internal/commands/browse.go` and the `README.md`.