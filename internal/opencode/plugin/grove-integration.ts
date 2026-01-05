import type { Plugin } from "@opencode-ai/plugin";
import { Database } from "bun:sqlite";
import { join } from "path";
import {
  mkdirSync,
  rmSync,
  writeFileSync,
  existsSync,
  appendFileSync,
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

  // Store active session ID for tracking
  let activeSessionId: string | null = null;

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

        // Extract session ID - opencode uses event.properties.info.id
        const props = event.properties as Record<string, unknown>;
        const info = props?.info as Record<string, unknown> | undefined;
        const sessionId =
          info?.id ||
          props?.id ||
          props?.session_id ||
          props?.sessionId ||
          (props?.session as Record<string, unknown>)?.id ||
          `opencode-${Date.now()}`;

        activeSessionId = String(sessionId);
        const opencodePID = getOpencodePID();
        const sessionDir = join(sessionsDir, activeSessionId);
        const { repo, branch } = await getGitInfo();

        log.info("Session created", {
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

      if (event.type === "session.status") {
        const props = event.properties as { status?: { type?: string } | string };
        const statusObj = props.status;
        const status = typeof statusObj === "object" ? (statusObj?.type === "busy" ? "running" : statusObj?.type || "running") : (statusObj || "running");
        log.debug("Session status event", {
          status,
          active_session: activeSessionId,
          working_dir: workingDir,
        });

        // Update both the opencode session AND any grove-flow job sessions in the same directory
        try {
          // Update by session ID
          if (activeSessionId) {
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
            status,
            working_dir: workingDir,
            rows_updated: result.changes,
          });
        } catch (e) {
          log.error("Failed to update session status", {
            session_id: activeSessionId,
            error: String(e),
          });
        }
      }

      if (event.type === "session.idle") {
        log.debug("Session idle event", { active_session: activeSessionId, working_dir: workingDir });

        try {
          // Update by session ID
          if (activeSessionId) {
            db.prepare(
              "UPDATE sessions SET status = 'idle', last_activity = ? WHERE id = ?"
            ).run(new Date().toISOString(), activeSessionId);
          }
          // Also update any running sessions in the same working directory
          // Include both 'opencode' and 'claude' providers since grove-flow may use either
          const result = db.prepare(
            "UPDATE sessions SET status = 'idle', last_activity = ? WHERE working_directory = ? AND status = 'running' AND provider IN ('opencode', 'claude')"
          ).run(new Date().toISOString(), workingDir);
          log.info("Session marked idle", {
            session_id: activeSessionId,
            working_dir: workingDir,
            rows_updated: result.changes,
          });
        } catch (e) {
          log.error("Failed to update session to idle", {
            session_id: activeSessionId,
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

        log.info("Session deleted event", { session_id: sessionIdToDelete });

        try {
          db.prepare(
            "UPDATE sessions SET status = 'completed', ended_at = ?, last_activity = ? WHERE id = ?"
          ).run(new Date().toISOString(), new Date().toISOString(), sessionIdToDelete);

          const sessionDir = join(sessionsDir, sessionIdToDelete);
          rmSync(sessionDir, { recursive: true, force: true });
          log.info("Session completed and cleaned up", {
            session_id: sessionIdToDelete,
          });
        } catch (e) {
          log.error("Failed to complete session", {
            session_id: sessionIdToDelete,
            error: String(e),
          });
        }
        activeSessionId = null;
      }
    },

    "tool.execute.before": async () => {
      log.debug("Tool execute before", {
        has_db: db !== null,
        active_session: activeSessionId,
      });

      if (!db || !activeSessionId) return;
      // Any tool execution means the session is active
      try {
        db.prepare(
          "UPDATE sessions SET status = 'running', last_activity = ? WHERE id = ?"
        ).run(new Date().toISOString(), activeSessionId);
        log.debug("Session activity updated", { session_id: activeSessionId });
      } catch (e) {
        log.error("Failed to update activity", {
          session_id: activeSessionId,
          error: String(e),
        });
      }
    },
  };
};
