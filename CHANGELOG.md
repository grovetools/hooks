## v0.0.5 (2025-08-26)

### Features

* **hooks:** enhance stop hook to support oneshot job lifecycle tracking

## v0.0.4 (2025-08-25)

### Features

* **oneshot:** add notifications for job status changes
* **browse:** improve sessions browse display with lipgloss table
* **browse:** add auto-refresh to sessions browse command

### Bug Fixes

* fix oneshot running state

### Continuous Integration

* add Git LFS disable to release workflow
* disable linting in workflow

### Chores

* bump dependencies
* update formatting

## v0.0.3 (2025-08-15)

### Chores

* **deps:** bump dependencies
* bump deps

## v0.0.2 (2025-08-15)

### Bug Fixes

* resolve oneshot job field compilation errors
* skip lfs
* skip lfs
* ignore worktrees
* improve session cleanup to use PID checks before inactivity timeout
* improve session cleanup to use inactivity timeout instead of PID checks
* isolate test database from production data
* error in grove.yml

### Continuous Integration

* switch to Linux runners to reduce costs
* consolidate to single test job on macOS
* reduce test matrix to macOS with Go 1.24.4 only

### Chores

* bump deps
* change binary name
* bump deps

### Features

* add oneshot job tracking for grove-flow integration
* add state duration to JSON output
* improve session list/browse with better sorting and state duration
* enhance browse command with active filtering and smart sorting
* add automatic cleanup of dead sessions
* add interactive session browser with searchable table UI
* decouple grove-hooks from Canopy API with local SQLite storage

### Code Refactoring

* standardize E2E binary naming and use grove.yml for binary discovery

