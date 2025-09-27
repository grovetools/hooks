# Documentation Generation Task: Usage Patterns & Integrations

You are documenting common usage patterns and integrations for `grove-hooks`.

Create sections for the following:

**1. Integrating with a Claude Code Project**:
   - Detail the `grove-hooks install` command.
   - Show a before-and-after example of a `.claude/settings.local.json` file.
   - Explain how Claude Code automatically calls the hooks.

**2. Integrating with `grove-flow`**:
   - Explain the `oneshot` command's role.
   - Describe how `grove-flow` is expected to call `grove-hooks oneshot start` and `grove-hooks oneshot stop` with a JSON payload.
   - Reference `internal/commands/oneshot.go` for the expected JSON structure for start and stop events.

**3. Interactive Session Management**:
   - Focus on the `sessions browse` command.
   - Explain common workflows like filtering for active sessions, searching for a specific repository, selecting and archiving multiple sessions, and copying a session ID.

**4. Scripting and Automation**:
   - Show how to use `grove-hooks sessions list --json` combined with tools like `jq` to programmatically query session data. Provide an example, such as finding all failed sessions for a specific user.