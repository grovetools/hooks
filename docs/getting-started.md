This guide provides a step-by-step tutorial for integrating `grove-hooks` with a Claude Code project to begin tracking your AI agent sessions.

### Step 1: Installation

Before proceeding, ensure that `grove-hooks` is installed on your system. For detailed instructions, please refer to the [Installation](./installation.md) guide.

### Step 2: Integrate with a Project

The primary way to use `grove-hooks` is by integrating it into a project where you use Claude Code. This is done with the `install` command.

Navigate to your project's root directory and run the following command:

```bash
cd /path/to/your/project
grove-hooks install
```

This command performs the following actions:
1.  It locates the current directory.
2.  It creates a `.claude` directory if one does not already exist.
3.  It creates or updates a `.claude/settings.local.json` file, injecting the necessary hook configurations that point to the `grove-hooks` binary.

Any pre-existing settings in `settings.local.json` are preserved. The command makes `grove-hooks` an integral part of Claude's lifecycle within that repository.

### Step 3: Run a Claude Session

With the hooks installed, you can now run a Claude Code session as you normally would. `grove-hooks` will be invoked automatically in the background to track the session's activity.

Start a simple session:

```bash
claude -p "Write a hello world program in Go"
```

As Claude executes, the hooks configured in `settings.local.json` will trigger at various points (e.g., before and after tool use, at the end of the session), sending data to the local `grove-hooks` database.

### Step 4: View the Session

Once your Claude session has started or completed, you can verify that it was tracked by listing the sessions recorded by `grove-hooks`.

Run the `sessions list` command:

```bash
grove-hooks sessions list
```

You should see an output table that includes the session you just ran.

**Example Output:**
```
SESSION ID      TYPE      STATUS      CONTEXT               USER      STARTED               DURATION      IN STATE
claude-abc12... claude    completed   your-project/main     your-user 2023-10-27 10:30:05   1m25s         1m25s
```

This confirms that the hook system is working correctly and your session data is being stored locally.

### Step 5: Explore the Session Interactively

To inspect sessions in more detail, you can use the interactive session browser. This provides a terminal-based user interface for filtering, searching, and viewing session data.

Launch the browser with the following command:

```bash
grove-hooks sessions browse
```

This interface allows for real-time exploration of all tracked sessions and is the recommended next step for managing your session history.