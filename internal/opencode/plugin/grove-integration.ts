import type { Plugin } from "@opencode-ai/plugin";
import { Database } from "bun:sqlite";
import { join } from "path";
import {
  mkdirSync,
  rmSync,
  writeFileSync,
  existsSync,
  appendFileSync,
  renameSync,
  readFileSync,
} from "fs";

// This plugin is embedded into the grove-hooks binary and installed via `grove-hooks opencode install`.
// It connects to the grove-hooks SQLite database and creates a file-based
// session registry to make opencode sessions discoverable by grove-hooks.

// --- Structured Logging ---
// Writes JSON logs to .grove/logs/workspace-YYYY-MM-DD.log for compatibility with `core logs`

type LogLevel = "debug" | "info" | "warn" | "error";

interface LogEntry {
  time: string;
  level: LogLevel;
  component: string;
  msg: string;
  [key: string]: unknown;
}

function getLogFilePath(homeDir: string, workingDir: string): string {
  const today = new Date().toISOString().split("T")[0]; // YYYY-MM-DD
  const logsDir = join(workingDir, ".grove", "logs");
  if (!existsSync(logsDir)) {
    mkdirSync(logsDir, { recursive: true });
  }
  return join(logsDir, `workspace-${today}.log`);
}

function createLogger(homeDir: string, workingDir: string) {
  const component = "opencode.grove-plugin";

  const log = (
    level: LogLevel,
    msg: string,
    fields: Record<string, unknown> = {}
  ) => {
    const entry: LogEntry = {
      time: new Date().toISOString(),
      level,
      component,
      msg,
      ...fields,
    };

    try {
      const logPath = getLogFilePath(homeDir, workingDir);
      appendFileSync(logPath, JSON.stringify(entry) + "\n");
    } catch (e) {
      // Fallback to console if file logging fails
      console.error(`[${component}] Log write failed:`, e);
      console.log(JSON.stringify(entry));
    }
  };

  return {
    debug: (msg: string, fields?: Record<string, unknown>) =>
      log("debug", msg, fields),
    info: (msg: string, fields?: Record<string, unknown>) =>
      log("info", msg, fields),
    warn: (msg: string, fields?: Record<string, unknown>) =>
      log("warn", msg, fields),
    error: (msg: string, fields?: Record<string, unknown>) =>
      log("error", msg, fields),
  };
}

export const GroveIntegrationPlugin: Plugin = async ({
  project,
  directory,
  worktree,
}) => {
  const homeDir = process.env.HOME;
  if (!homeDir) {
    console.error(
      "[Grove Plugin] HOME environment variable not set. Plugin disabled."
    );
    return {};
  }

  const workingDir = worktree || directory || process.cwd();
  const log = createLogger(homeDir, workingDir);

  log.info("Plugin initializing", {
    home_dir: homeDir,
    working_dir: workingDir,
    worktree: worktree || null,
    directory: directory || null,
  });

  // Paths for grove-hooks integration
  const hooksDbPath = join(
    homeDir,
    ".local",
    "share",
    "grove-hooks",
    "state.db"
  );
  const sessionsDir = join(homeDir, ".grove", "hooks", "sessions");

  log.debug("Configured paths", {
    db_path: hooksDbPath,
    sessions_dir: sessionsDir,
  });

  // --- Database Setup ---
  let db: Database | null = null;
  try {
    // Ensure the database directory exists
    const dbDir = join(homeDir, ".local", "share", "grove-hooks");
    if (!existsSync(dbDir)) {
      log.info("Creating database directory", { path: dbDir });
      mkdirSync(dbDir, { recursive: true });
    }

    db = new Database(hooksDbPath);
    log.info("Database opened successfully", { path: hooksDbPath });
    // Note: sessions table is created by grove-hooks, we just use it
  } catch (e) {
    log.error("Failed to open grove-hooks database", {
      error: String(e),
      path: hooksDbPath,
    });
    return {}; // Disable plugin if DB can't be opened
  }

  // --- Helper Functions ---
  const getOpencodePID = () => process.pid;

  const getGitInfo = async (): Promise<{ repo: string; branch: string }> => {
    try {
      const { $ } = await import("bun");
      const repoResult =
        await $`git rev-parse --show-toplevel 2>/dev/null`.text();
      const branchResult =
        await $`git rev-parse --abbrev-ref HEAD 2>/dev/null`.text();

      const repoPath = repoResult.trim();
      const repo =
        repoPath ? repoPath.split("/").pop() || "unknown" : "unknown";
      const branch = branchResult.trim() || "unknown";

      return { repo, branch };
    } catch {
      return { repo: "unknown", branch: "unknown" };
    }
  };

  // Update the status field in a grove-flow job file's YAML frontmatter
  const updateJobFileStatus = (jobFilePath: string, newStatus: string): boolean => {
    if (!jobFilePath) return false;

    try {
      const content = readFileSync(jobFilePath, "utf8");
      const lines = content.split("\n");

      // Find the frontmatter boundaries and the status line
      let inFrontmatter = false;
      let frontmatterStart = -1;
      let frontmatterEnd = -1;
      let statusLineIdx = -1;

      for (let i = 0; i < lines.length; i++) {
        const trimmed = lines[i].trim();
        if (trimmed === "---") {
          if (!inFrontmatter) {
            inFrontmatter = true;
            frontmatterStart = i;
          } else {
            frontmatterEnd = i;
            break;
          }
        } else if (inFrontmatter && trimmed.startsWith("status:")) {
          statusLineIdx = i;
        }
      }

      if (frontmatterStart === -1 || frontmatterEnd === -1 || statusLineIdx === -1) {
        log.warn("Could not find frontmatter or status field in job file", {
          job_file_path: jobFilePath,
          frontmatter_start: frontmatterStart,
          frontmatter_end: frontmatterEnd,
          status_line_idx: statusLineIdx,
        });
        return false;
      }

      // Get the indentation from the original line
      const originalLine = lines[statusLineIdx];
      let indent = "";
      for (const ch of originalLine) {
        if (ch === " " || ch === "\t") {
          indent += ch;
        } else {
          break;
        }
      }

      // Update the status line
      lines[statusLineIdx] = `${indent}status: ${newStatus}`;

      // Write the file back
      writeFileSync(jobFilePath, lines.join("\n"));
      log.info("Updated job file status", {
        job_file_path: jobFilePath,
        new_status: newStatus,
      });
      return true;
    } catch (e) {
      log.error("Failed to update job file status", {
        job_file_path: jobFilePath,
        new_status: newStatus,
        error: String(e),
      });
      return false;
    }
  };

  // Store active session ID for tracking
  let activeSessionId: string | null = null;
  // Store flow job ID if this is a grove-flow managed session
  let flowJobId: string | null = process.env.GROVE_FLOW_JOB_ID || null;
  // Store flow job path for updating status in YAML frontmatter
  let flowJobPath: string | null = process.env.GROVE_FLOW_JOB_PATH || null;

  log.info("Plugin initialized, returning hooks");

  return {
    event: async ({ event }) => {
      // Skip noisy events that don't need logging
      const noisyEvents = ["file.watcher.updated", "file.edited", "lsp.client.diagnostics", "lsp.updated"];
      if (noisyEvents.includes(event.type)) {
        return;
      }

      log.debug("Event received", {
        event_type: event.type,
        active_session: activeSessionId,
      });

      if (!db) {
        log.warn("Event received but database not available", {
          event_type: event.type,
        });
        return;
      }

      if (event.type === "session.created") {
        // Extract native opencode session ID from event
        const props = event.properties as Record<string, unknown>;
        const info = props?.info as Record<string, unknown> | undefined;
        const opencodeSessionId =
          info?.id ||
          props?.id ||
          props?.session_id ||
          props?.sessionId ||
          (props?.session as Record<string, unknown>)?.id ||
          `opencode-${Date.now()}`;

        activeSessionId = String(opencodeSessionId);
        const opencodePID = getOpencodePID();
        const { repo, branch } = await getGitInfo();

        // Check if this is a grove-flow managed session
        // flowJobId and flowJobPath are already set at module level from env vars
        const flowPlanName = process.env.GROVE_FLOW_PLAN_NAME;
        const flowJobTitle = process.env.GROVE_FLOW_JOB_TITLE;

        if (flowJobId) {
          // This is a grove-flow managed session.
          // grove-flow already created a session directory at ~/.grove/hooks/sessions/{flowJobId}
          // We need to:
          // 1. Rename that directory to use the native opencode session ID
          // 2. Update the metadata with the opencode session ID and PID
          // 3. Update the database entry

          const flowSessionDir = join(sessionsDir, flowJobId);
          const opencodeSessionDir = join(sessionsDir, activeSessionId);

          log.info("Flow-managed session created", {
            flow_job_id: flowJobId,
            opencode_session_id: activeSessionId,
            flow_session_dir: flowSessionDir,
            opencode_session_dir: opencodeSessionDir,
            pid: opencodePID,
            working_dir: workingDir,
          });

          try {
            // Check if the flow session directory exists
            if (existsSync(flowSessionDir)) {
              // Read existing metadata
              const existingMetadataPath = join(flowSessionDir, "metadata.json");
              let existingMetadata: Record<string, unknown> = {};
              if (existsSync(existingMetadataPath)) {
                try {
                  existingMetadata = JSON.parse(readFileSync(existingMetadataPath, "utf8"));
                } catch (e) {
                  log.warn("Failed to parse existing metadata", { error: String(e) });
                }
              }

              // Rename the directory from job ID to opencode session ID
              renameSync(flowSessionDir, opencodeSessionDir);
              log.info("Renamed session directory", {
                from: flowSessionDir,
                to: opencodeSessionDir,
              });

              // Update metadata with opencode session ID and PID
              const updatedMetadata = {
                ...existingMetadata,
                claude_session_id: activeSessionId, // Store the native opencode session ID
                pid: opencodePID,
              };
              writeFileSync(
                join(opencodeSessionDir, "metadata.json"),
                JSON.stringify(updatedMetadata, null, 2)
              );
              log.debug("Updated metadata.json", updatedMetadata);

              // Update pid.lock
              writeFileSync(join(opencodeSessionDir, "pid.lock"), String(opencodePID));
              log.debug("Updated pid.lock", { pid: opencodePID });
            } else {
              // Flow session directory doesn't exist (race condition or grove-flow failed)
              // Fall back to creating a new session directory
              log.warn("Flow session directory not found, creating new session", {
                expected_dir: flowSessionDir,
              });
              mkdirSync(opencodeSessionDir, { recursive: true });
              const metadata = {
                session_id: flowJobId,
                claude_session_id: activeSessionId,
                provider: "opencode",
                pid: opencodePID,
                repo,
                branch,
                working_directory: workingDir,
                user: process.env.USER || "unknown",
                started_at: new Date().toISOString(),
                job_title: flowJobTitle,
                plan_name: flowPlanName,
                job_file_path: flowJobPath,
                type: "interactive_agent",
              };
              writeFileSync(
                join(opencodeSessionDir, "metadata.json"),
                JSON.stringify(metadata, null, 2)
              );
              writeFileSync(join(opencodeSessionDir, "pid.lock"), String(opencodePID));
            }

            // Update the database - use flow job ID as the primary session ID
            db.prepare(
              `UPDATE sessions SET claude_session_id = ?, pid = ?, status = 'running', last_activity = ? WHERE id = ?`
            ).run(activeSessionId, opencodePID, new Date().toISOString(), flowJobId);
            log.info("Updated database with opencode session details", {
              flow_job_id: flowJobId,
              opencode_session_id: activeSessionId,
              pid: opencodePID,
            });
          } catch (e) {
            log.error("Failed to enrich flow session", {
              flow_job_id: flowJobId,
              opencode_session_id: activeSessionId,
              error: String(e),
            });
          }
        } else {
          // Standalone opencode session (not managed by grove-flow)
          // Create a new session directory
          const sessionDir = join(sessionsDir, activeSessionId);

          log.info("Standalone session created", {
            session_id: activeSessionId,
            pid: opencodePID,
            repo,
            branch,
            working_dir: workingDir,
            session_dir: sessionDir,
          });

          // 1. Create session directory for file-based discovery
          mkdirSync(sessionDir, { recursive: true });
          log.debug("Session directory created", { path: sessionDir });

          // 2. Write pid.lock file
          writeFileSync(join(sessionDir, "pid.lock"), String(opencodePID));
          log.debug("pid.lock written", { pid: opencodePID });

          // 3. Write metadata.json
          const metadata = {
            session_id: activeSessionId,
            claude_session_id: activeSessionId,
            provider: "opencode",
            type: "opencode_session", // Mark as opencode session so stop hook knows not to auto-complete
            pid: opencodePID,
            repo,
            branch,
            working_directory: workingDir,
            user: process.env.USER || "unknown",
            started_at: new Date().toISOString(),
          };
          writeFileSync(
            join(sessionDir, "metadata.json"),
            JSON.stringify(metadata, null, 2)
          );
          log.debug("metadata.json written", metadata);

          // 4. Insert into SQLite database (match actual schema with tmux_key column)
          try {
            db.prepare(
              `INSERT OR REPLACE INTO sessions (id, type, pid, repo, branch, tmux_key, working_directory, user, status, started_at, last_activity, provider, claude_session_id)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
            ).run(
              activeSessionId,
              "opencode_session",
              opencodePID,
              repo,
              branch,
              null, // tmux_key - not used for opencode
              workingDir,
              metadata.user,
              "running",
              metadata.started_at,
              metadata.started_at,
              "opencode",
              activeSessionId // claude_session_id
            );
            log.info("Session inserted into database", {
              session_id: activeSessionId,
              status: "running",
            });
          } catch (e) {
            log.error("Failed to insert session into database", {
              session_id: activeSessionId,
              error: String(e),
            });
          }
        }
      }

      if (event.type === "session.status") {
        const props = event.properties as { status?: { type?: string } | string };
        const statusObj = props.status;
        const status = typeof statusObj === "object" ? (statusObj?.type === "busy" ? "running" : statusObj?.type || "running") : (statusObj || "running");
        log.debug("Session status event", {
          status,
          active_session: activeSessionId,
          flow_job_id: flowJobId,
          working_dir: workingDir,
        });

        // Update the session status in the database
        try {
          // If this is a flow-managed session, update by flow job ID
          if (flowJobId) {
            db.prepare(
              "UPDATE sessions SET status = ?, last_activity = ? WHERE id = ?"
            ).run(status, new Date().toISOString(), flowJobId);
            log.info("Flow session status updated", {
              flow_job_id: flowJobId,
              status,
            });
          }
          // Also update by opencode session ID if available
          if (activeSessionId && activeSessionId !== flowJobId) {
            db.prepare(
              "UPDATE sessions SET status = ?, last_activity = ? WHERE id = ?"
            ).run(status, new Date().toISOString(), activeSessionId);
          }
          // Also update any running/idle sessions in the same working directory (grove-flow job sessions)
          // Include both 'opencode' and 'claude' providers since grove-flow may use either
          const result = db.prepare(
            "UPDATE sessions SET status = ?, last_activity = ? WHERE working_directory = ? AND status IN ('running', 'idle', 'pending_user') AND provider IN ('opencode', 'claude')"
          ).run(status, new Date().toISOString(), workingDir);
          log.info("Session status updated", {
            session_id: activeSessionId,
            flow_job_id: flowJobId,
            status,
            working_dir: workingDir,
            rows_updated: result.changes,
          });

          // Also update the job YAML file if this is a flow-managed session transitioning to running
          // TODO: Re-enable once testing confirms stability
          // if (flowJobPath && status === "running") {
          //   updateJobFileStatus(flowJobPath, "running");
          // }
        } catch (e) {
          log.error("Failed to update session status", {
            session_id: activeSessionId,
            flow_job_id: flowJobId,
            error: String(e),
          });
        }
      }

      if (event.type === "session.idle") {
        log.info("Session idle event - calling stop hook", {
          active_session: activeSessionId,
          flow_job_id: flowJobId,
          working_dir: workingDir,
        });

        try {
          // Call grove-hooks stop to update filesystem session state and trigger proper status flow
          // This is the equivalent of what Claude's hook system does automatically
          const stopInput = JSON.stringify({
            session_id: activeSessionId,
            hook_event_name: "stop",
            exit_reason: "", // Empty = normal end-of-turn, not a completion
            duration_ms: 0,
          });

          const { $ } = await import("bun");
          const result = await $`echo ${stopInput} | grove-hooks stop`.quiet();

          log.info("Stop hook called successfully", {
            session_id: activeSessionId,
            flow_job_id: flowJobId,
            exit_code: result.exitCode,
          });

          // Also update database directly as backup
          if (flowJobId) {
            db.prepare(
              "UPDATE sessions SET status = 'idle', last_activity = ? WHERE id = ?"
            ).run(new Date().toISOString(), flowJobId);
          }
          if (activeSessionId && activeSessionId !== flowJobId) {
            db.prepare(
              "UPDATE sessions SET status = 'idle', last_activity = ? WHERE id = ?"
            ).run(new Date().toISOString(), activeSessionId);
          }
        } catch (e) {
          log.error("Failed to call stop hook or update session", {
            session_id: activeSessionId,
            flow_job_id: flowJobId,
            error: String(e),
          });
        }
      }

      if (event.type === "session.deleted") {
        // Use activeSessionId if available, otherwise try to extract from event
        const sessionIdToDelete = activeSessionId || (() => {
          const props = event.properties as Record<string, unknown>;
          return String(
            props?.id ||
            props?.session_id ||
            props?.sessionId ||
            (props?.session as Record<string, unknown>)?.id ||
            "unknown"
          );
        })();

        log.info("Session deleted event", {
          session_id: sessionIdToDelete,
          flow_job_id: flowJobId,
        });

        try {
          // If this is a flow-managed session, update by flow job ID
          if (flowJobId) {
            db.prepare(
              "UPDATE sessions SET status = 'completed', ended_at = ?, last_activity = ? WHERE id = ?"
            ).run(new Date().toISOString(), new Date().toISOString(), flowJobId);
            log.info("Flow session marked completed", { flow_job_id: flowJobId });
          }
          // Also update by opencode session ID
          db.prepare(
            "UPDATE sessions SET status = 'completed', ended_at = ?, last_activity = ? WHERE id = ?"
          ).run(new Date().toISOString(), new Date().toISOString(), sessionIdToDelete);

          // Clean up session directory
          const sessionDir = join(sessionsDir, sessionIdToDelete);
          rmSync(sessionDir, { recursive: true, force: true });
          log.info("Session completed and cleaned up", {
            session_id: sessionIdToDelete,
            flow_job_id: flowJobId,
          });
        } catch (e) {
          log.error("Failed to complete session", {
            session_id: sessionIdToDelete,
            flow_job_id: flowJobId,
            error: String(e),
          });
        }
        activeSessionId = null;
        flowJobId = null;
        flowJobPath = null;
      }
    },

    "tool.execute.before": async () => {
      log.debug("Tool execute before", {
        has_db: db !== null,
        active_session: activeSessionId,
        flow_job_id: flowJobId,
      });

      if (!db) return;
      // Any tool execution means the session is active
      try {
        // Update by flow job ID if this is a flow-managed session
        if (flowJobId) {
          db.prepare(
            "UPDATE sessions SET status = 'running', last_activity = ? WHERE id = ?"
          ).run(new Date().toISOString(), flowJobId);
        }
        // Also update by opencode session ID if available
        if (activeSessionId && activeSessionId !== flowJobId) {
          db.prepare(
            "UPDATE sessions SET status = 'running', last_activity = ? WHERE id = ?"
          ).run(new Date().toISOString(), activeSessionId);
        }
        log.debug("Session activity updated", {
          session_id: activeSessionId,
          flow_job_id: flowJobId,
        });

        // Also update the job YAML file if this is a flow-managed session
        // TODO: Re-enable once testing confirms stability
        // if (flowJobPath) {
        //   updateJobFileStatus(flowJobPath, "running");
        // }
      } catch (e) {
        log.error("Failed to update activity", {
          session_id: activeSessionId,
          flow_job_id: flowJobId,
          error: String(e),
        });
      }
    },
  };
};
