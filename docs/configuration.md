# Configuration

This document explains the configuration options available for `grove-hooks`, including Claude Code integration, repository-specific hooks, and environment variables.

## Claude Code Integration (`.claude/settings.local.json`)

### Overview

The `.claude/settings.local.json` file is the primary integration point for Claude Code. This file is automatically managed by the `grove-hooks install` command and contains the hook configurations that tell Claude Code when and how to call grove-hooks.

### Automatic Management

The configuration is automatically managed through:

```bash
grove-hooks install
```

This command:
- Creates the `.claude` directory if it doesn't exist
- Creates or updates `settings.local.json` with grove-hooks configuration
- Preserves any existing settings in the file
- Backs up corrupted files before creating new ones

### Hook Configuration Structure

The `hooks` section that grove-hooks adds follows this structure:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": ".*",
        "hooks": [
          {
            "type": "command",
            "command": "grove-hooks pretooluse"
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": ".*",
        "hooks": [
          {
            "type": "command",
            "command": "grove-hooks posttooluse"
          }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": ".*",
        "hooks": [
          {
            "type": "command",
            "command": "grove-hooks notification"
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": ".*",
        "hooks": [
          {
            "type": "command",
            "command": "grove-hooks stop"
          }
        ]
      }
    ],
    "SubagentStop": [
      {
        "matcher": ".*",
        "hooks": [
          {
            "type": "command",
            "command": "grove-hooks subagentstop"
          }
        ]
      }
    ]
  }
}
```

### Hook Types Explained

- **PreToolUse**: Executes before any tool use to validate and log the operation. Can block tool execution if needed.
- **PostToolUse**: Executes after Edit, Write, MultiEdit, Bash, or Read tools complete to log results and update statistics.
- **Notification**: Executes when Claude Code sends notifications, allowing for custom notification handling and logging.
- **Stop**: Executes when a Claude Code conversation or session stops, triggering cleanup and repository-specific hooks.
- **SubagentStop**: Executes when a subagent completes its task, logging the results and status.

### Matcher Patterns

The `"matcher": ".*"` pattern means the hooks apply to all contexts. You can customize this to apply hooks only to specific repositories or file patterns if needed.

## Repository-Specific Hooks (`.canopy.yaml`)

### Overview

Grove-hooks can execute custom commands defined in a `.canopy.yaml` file at the root of a repository when a session stops. This allows for repository-specific cleanup, validation, or automated tasks.

### Configuration Structure

The `.canopy.yaml` file uses the following structure:

```yaml
hooks:
  on_stop:
    - name: "Run linter"
      command: "npm run lint"
      run_if: "changes"
    - name: "Run tests"
      command: "npm test"
      run_if: "changes"
    - name: "Cleanup temp files"
      command: "rm -rf tmp/"
```

### Hook Command Options

#### Required Fields

- **name**: A descriptive name for the hook command (used in logs)
- **command**: The shell command to execute

#### Optional Fields

- **run_if**: Condition for when to run the command
  - `"changes"`: Only run if there are git changes (staged, unstaged, or untracked files)
  - If omitted: Always run the command

### Execution Details

Repository hooks are executed:
- When a Claude Code session stops (via the Stop hook)
- Only if the session has a working directory
- In the order they are defined in the YAML file
- Each command is executed in the repository's working directory

### Git Changes Detection

When `run_if: "changes"` is specified, grove-hooks checks for:
- Staged changes (`git diff --cached`)
- Unstaged changes (`git diff`)
- Untracked files (`git ls-files --others --exclude-standard`)

If any of these conditions are true, the command will execute.

### Example Configurations

#### Basic Linting and Testing

```yaml
hooks:
  on_stop:
    - name: "Format code"
      command: "prettier --write ."
      run_if: "changes"
    - name: "Run ESLint"
      command: "npm run lint:fix"
      run_if: "changes"
    - name: "Run unit tests"
      command: "npm test"
      run_if: "changes"
```

#### Multi-language Project

```yaml
hooks:
  on_stop:
    - name: "Format Go code"
      command: "gofmt -w ."
      run_if: "changes"
    - name: "Format JavaScript"
      command: "prettier --write '**/*.{js,ts,tsx}'"
      run_if: "changes"
    - name: "Run Go tests"
      command: "go test ./..."
      run_if: "changes"
    - name: "Run Node.js tests"
      command: "npm test"
      run_if: "changes"
```

#### Documentation and Cleanup

```yaml
hooks:
  on_stop:
    - name: "Generate documentation"
      command: "make docs"
      run_if: "changes"
    - name: "Clean build artifacts"
      command: "make clean"
    - name: "Update dependency cache"
      command: "go mod tidy"
      run_if: "changes"
```

#### Blocking Hooks (Advanced)

Commands that exit with code 2 will block the session from completing:

```yaml
hooks:
  on_stop:
    - name: "Mandatory security scan"
      command: "security-tool scan || exit 2"
      run_if: "changes"
```

If this command fails, the session stop will be blocked and an error will be returned to Claude Code.

### Error Handling

- **Exit code 0**: Command succeeded, continue with next command
- **Exit code 1**: Command failed, log error but continue with next command
- **Exit code 2**: Command failed, block session completion and return error
- **Other exit codes**: Treated as non-blocking failures

### Logging

All hook command executions are logged with:
- Command name and command string
- Success/failure status
- Whether the command was blocking
- Any error messages

## Database Path (Environment Variable)

### GROVE_HOOKS_DB_PATH

The database path can be overridden using the `GROVE_HOOKS_DB_PATH` environment variable. This is primarily useful for testing purposes or when you need to store the database in a specific location.

#### Default Path

If not specified, grove-hooks uses:
```
~/.local/share/grove-hooks/state.db
```

#### Custom Path Example

```bash
export GROVE_HOOKS_DB_PATH="/custom/path/to/hooks.db"
grove-hooks sessions list
```

#### Testing Configuration

```bash
# Use temporary database for testing
export GROVE_HOOKS_DB_PATH="/tmp/test-hooks.db"
grove-hooks sessions list

# Clean up after tests
rm /tmp/test-hooks.db
```

#### Directory Creation

Grove-hooks will automatically create the directory structure for the database path if it doesn't exist. Ensure the parent directory is writable by the user running grove-hooks.

### Configuration Precedence

1. `GROVE_HOOKS_DB_PATH` environment variable (highest priority)
2. Default path: `~/.local/share/grove-hooks/state.db`

## Integration Examples

### Complete Repository Setup

1. **Install grove-hooks in the repository:**
   ```bash
   cd /path/to/my/repo
   grove-hooks install
   ```

2. **Create repository-specific hooks:**
   ```bash
   cat > .canopy.yaml << EOF
   hooks:
     on_stop:
       - name: "Run linter"
         command: "npm run lint"
         run_if: "changes"
       - name: "Run tests"
         command: "npm test"
         run_if: "changes"
   EOF
   ```

3. **Verify configuration:**
   ```bash
   # Check that hooks are installed
   cat .claude/settings.local.json | jq .hooks

   # Test repository hooks
   grove-hooks sessions list
   ```

### Development Workflow

With this configuration, every time a Claude Code session ends in your repository:

1. Grove-hooks will check for git changes
2. If changes exist, it will run your linter and tests
3. All activity is logged to the local database
4. You can review session history and tool usage statistics

This ensures code quality and provides visibility into AI-assisted development activities.