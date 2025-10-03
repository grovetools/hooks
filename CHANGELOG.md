## v0.0.8-nightly.f075336 (2025-10-03)

## v0.1.0 (2025-10-01)

This release introduces a significant documentation overhaul and new filtering capabilities. The entire documentation structure has been simplified and regenerated, now organized into four sections: Overview, Examples, Configuration, and Command Reference (c798c88, a6e5385). The documentation generation process now supports automatic Table of Contents creation in the README (0760e3c).

Observability features have been enhanced with new filters for the `sessions list` command, allowing users to query by session type and plan name (ad21d50). The interactive session browser now includes a "select all" feature (Ctrl+A) for easier management of multiple sessions (411fded).

The developer experience has been improved by adding structured logging to the `install` command for better diagnostics (32649b7) and centralizing Git repository detection to more reliably handle worktrees (e615015). The release workflow was also updated to extract release notes directly from the `CHANGELOG.md` file, ensuring consistency between documentation and GitHub releases (bfacd3d).

### Features

- Add filtering for sessions by type and plan name (ad21d50)
- Add select-all functionality (Ctrl+A) to the interactive session browser (411fded)
- Update release workflow to extract release notes from CHANGELOG.md (bfacd3d)
- Enhance `install` command with structured logging for better diagnostics (32649b7)
- Add structured logging to `install` command (228f5b4)
- Add TOC generation and docgen configuration updates for improved documentation (0760e3c)
- Add default context rules file (.cx-rules) (4501c64)
- Add new simplified project documentation (a6e5385)

### Bug Fixes

- Update CI workflow to disable execution on branches correctly (dfbd54e)
- Clean up `README.md.tpl` template formatting (27208e5)
- Correctly handle `pretooluse` installation matcher (f8c8c7c)
- Update `grove.yml` to use the correct binary name `grove-hooks` (01ed2f0)
- Add missing `version` command (f63adb3)

### Code Refactoring

- Standardize docgen.config.yml key order and settings (7dc366e)
- Centralize git repository detection logic to improve consistency and handle worktrees (e615015)

### Continuous Integration

- Remove redundant tests from the release workflow (a27b7e2)

### Documentation

- Make documentation more succinct and add strip lines option (21b8d49)
- Update docgen configuration and README templates for TOC generation (97c733a)
- Update docgen config and enhance overview prompt (453a89c)
- Simplify documentation structure to 4 sections (c798c88)
- Rename "Introduction" sections to "Overview" for clarity (17789e7)
- Simplify installation instructions to point to the main Grove guide (199e3c4)
- Add initial documentation structure (a4fb4d8)

### Chores

- Temporarily disable CI workflow (4d49ef4)
- Standardize documentation filenames to a numbered convention (23ab87f)
- Update .gitignore rules (0dac916)
- Bump and sync Grove ecosystem dependencies (09bfcfe, 0c8533b, 9662389)

### File Changes

```
 .cx-rules                               |  13 +
 .github/workflows/ci.yml                |   4 +-
 .github/workflows/release.yml           |  27 +-
 .gitignore                              |   3 +
 CHANGELOG.md                            | 129 ++++++
 CLAUDE.md                               |  30 ++
 Makefile                                |  10 +-
 README.md                               | 162 ++-----
 docs/01-overview.md                     |  46 ++
 docs/02-examples.md                     | 149 ++++++
 docs/03-configuration.md                |  95 ++++
 docs/04-command-reference.md            | 199 ++++++++
 docs/README.md.tpl                      |   6 +
 docs/docgen.config.yml                  |  40 ++
 docs/docs.rules                         |   1 +
 docs/images/grove-hooks.svg             | 270 +++++++++++
 docs/prompts/01-overview.md             |  31 ++
 docs/prompts/02-examples.md             |  14 +
 docs/prompts/03-configuration.md        |  21 +
 docs/prompts/04-command-reference.md    |  61 +++
 go.mod                                  |  35 +-
 go.sum                                  | 142 +-----
 grove.yml                               |   7 +-
 internal/api/client.go                  |  33 +-
 internal/commands/browse.go             |  49 +-
 internal/commands/install.go            |  47 +-
 internal/commands/oneshot.go            |  24 +-
 internal/commands/sessions.go           |  33 ++
 internal/commands/version.go            |  36 ++
 internal/git/info.go                    |  44 ++
 internal/hooks/context.go               |  21 +-
 main.go                                 |   1 +
 pkg/docs/docs.json                      | 112 +++++
 tests/e2e/main.go                       |   3 +
 tests/e2e/scenarios_flow_integration.go | 785 ++++++++++++++++++++++++++++++++
 35 files changed, 2311 insertions(+), 372 deletions(-)
```

## v0.0.8 (2025-09-17)

### Bug Fixes

* update grove.yml to use correct binary name
* add version cmd

### Documentation

* **changelog:** update CHANGELOG.md for v0.0.8
* **changelog:** update CHANGELOG.md for v0.0.8
* **changelog:** update CHANGELOG.md for v0.0.8
* **changelog:** update CHANGELOG.md for v0.0.8
* **changelog:** update CHANGELOG.md for v0.0.9
* **changelog:** update CHANGELOG.md for v0.0.8

### Features

* add select all functionality to session browse

### Chores

* bump dependencies
* update Grove dependencies to latest versions
* **deps:** sync Grove dependencies to latest versions
* update readme

## v0.0.8 (2025-09-17)

### Features

* add select all functionality to session browse

### Documentation

* **changelog:** update CHANGELOG.md for v0.0.8
* **changelog:** update CHANGELOG.md for v0.0.8
* **changelog:** update CHANGELOG.md for v0.0.8
* **changelog:** update CHANGELOG.md for v0.0.9
* **changelog:** update CHANGELOG.md for v0.0.8

### Chores

* bump dependencies
* update Grove dependencies to latest versions
* **deps:** sync Grove dependencies to latest versions
* update readme

### Bug Fixes

* update grove.yml to use correct binary name
* add version cmd

## v0.0.8 (2025-09-17)

### Features

* add select all functionality to session browse

### Bug Fixes

* update grove.yml to use correct binary name
* add version cmd

### Documentation

* **changelog:** update CHANGELOG.md for v0.0.8
* **changelog:** update CHANGELOG.md for v0.0.8
* **changelog:** update CHANGELOG.md for v0.0.9
* **changelog:** update CHANGELOG.md for v0.0.8

### Chores

* bump dependencies
* update Grove dependencies to latest versions
* **deps:** sync Grove dependencies to latest versions
* update readme

## v0.0.8 (2025-09-13)

### Chores

* **deps:** sync Grove dependencies to latest versions
* update readme

### Bug Fixes

* update grove.yml to use correct binary name
* add version cmd

### Documentation

* **changelog:** update CHANGELOG.md for v0.0.8
* **changelog:** update CHANGELOG.md for v0.0.9
* **changelog:** update CHANGELOG.md for v0.0.8

## v0.0.8 (2025-09-12)

### Chores

* **deps:** sync Grove dependencies to latest versions
* update readme

### Bug Fixes

* update grove.yml to use correct binary name
* add version cmd

### Documentation

* **changelog:** update CHANGELOG.md for v0.0.9
* **changelog:** update CHANGELOG.md for v0.0.8

## v0.0.9 (2025-08-27)

### Bug Fixes

* add version cmd

### Chores

* **deps:** sync Grove dependencies to latest versions

## v0.0.8 (2025-08-27)

### Chores

* update readme

## v0.0.7 (2025-08-26)

### Bug Fixes

* add readme, fix makefile/release with cgo

## v0.0.6 (2025-08-26)

### Chores

* standardize binary name to 'hooks'

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

