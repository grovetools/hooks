# Documentation Generation Task: Getting Started Guide

You are an expert technical writer creating a "Getting Started" guide for `grove-hooks`.
- Provide a short, step-by-step tutorial for a new user.
- The tutorial should cover the most common use case: integrating with a Claude Code project.
- **Step 1: Installation**: Briefly refer to the Installation section.
- **Step 2: Integrate with a Project**: Explain how to run `grove-hooks install` inside a project repository. Reference the `internal/commands/install.go` file to describe what this command does (creates `.claude/settings.local.json`).
- **Step 3: Run a Claude Session**: Explain that the user should now run a normal Claude Code session (`claude -p "do something"`). Mention that `grove-hooks` will be triggered automatically in the background.
- **Step 4: View the Session**: Show how to use `grove-hooks sessions list` to see the session that was just created.
- **Step 5: Explore the Session**: Briefly introduce `grove-hooks sessions browse` as the next step for interactive exploration.