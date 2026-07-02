// Grove integration plugin for opencode — version 2.0.0 (see GROVE_PLUGIN_VERSION).
//
// Embedded into the grove-hooks binary and installed to
// ~/.config/opencode/plugin/grove-integration.ts by `hooks opencode install`.
// Compare installed vs embedded versions with `hooks opencode status`.
//
// Design constraint (v2): ALL session bookkeeping is routed through
// `grove hooks <event>` shell-outs (the Go session pipeline). This plugin
// does NOT open the hooks database or talk to the daemon directly — v1's
// direct SQLite writes duplicated Go schema knowledge and wrote to a store
// the Go pipeline no longer reads. The only filesystem writes left
// here are (a) the flow-preseeded session rename dance, (b) metadata
// enrichment with the opencode transcript pointer (native_session_id +
// opencode_storage_root), and (c) a thin registration fallback when the
// `grove` binary is unavailable, so sessions stay discoverable.
//
// Event wiring (event names/payloads per @opencode-ai/sdk Event types;
// verified against packages/schema/src/v1/session.ts and
// session-status-event.ts in the opencode source):
//   - session.created      -> flow rename dance (if GROVE_FLOW_JOB_ID) +
//                             `grove hooks session-start` + pointer enrichment
//   - session.status(busy/retry) -> `grove hooks session-status` (running)
//   - session.idle         -> `grove hooks stop` with empty exit_reason
//                             (end of turn -> idle; opencode never
//                             auto-completes — deliberate)
//   - session.deleted      -> `grove hooks session-end` (terminal + cleanup)
//   - tool.execute.before  -> `grove hooks session-status` (running),
//                             throttled
//
// The Go pipeline resolves the provider from GROVE_AGENT_PROVIDER (exported
// on every shell-out below, and by flow for flow-launched sessions) and maps
// the native opencode session id to the flow job id via the session
// directory's metadata.json.

export const GROVE_PLUGIN_VERSION = "2.0.0";

import type { Plugin } from "@opencode-ai/plugin";
import { join } from "path";
import {
  mkdirSync,
  writeFileSync,
  existsSync,
  appendFileSync,
  renameSync,
  readFileSync,
} from "fs";

// --- XDG Path Resolution ---
// Call `core paths` to get XDG-compliant paths for Grove directories

interface GrovePaths {
  config_dir: string;
  data_dir: string;
  state_dir: string;
  cache_dir: string;
  bin_dir: string;
}

function getGrovePaths(): GrovePaths | null {
  try {
    const result = Bun.spawnSync(["core", "paths"], {
      stdout: "pipe",
      stderr: "pipe",
    });
    if (result.exitCode === 0) {
      return JSON.parse(result.stdout.toString()) as GrovePaths;
    }
    console.error("[Grove Plugin] Failed to get paths from core:", result.stderr.toString());
    return null;
  } catch (e) {
    console.error("[Grove Plugin] Failed to call core paths:", e);
    return null;
  }
}

// opencodeStorageRoot mirrors opencode's Global.Path.data ("xdg-basedir"
// data dir + "/opencode") + "/storage" — the fragment store that holds
// session/<projectID>/<ses_*>.json, message/<sessionID>/msg_*.json and
// part/<messageID>/prt_*.json. Recorded into session metadata so agentlogs
// can assemble the transcript without scanning.
function opencodeStorageRoot(homeDir: string): string {
  const xdgData = process.env.XDG_DATA_HOME;
  const base = xdgData && xdgData !== "" ? xdgData : join(homeDir, ".local", "share");
  return join(base, "opencode", "storage");
}

// --- Structured Logging ---
// Writes JSON logs to XDG state directory for compatibility with `core logs`.
// Prefers GROVE_LOG_DIR env var (set by grove-flow when launching agents),
// falls back to state_dir/logs from `core paths`.

type LogLevel = "debug" | "info" | "warn" | "error";

interface LogEntry {
  time: string;
  level: LogLevel;
  component: string;
  msg: string;
  [key: string]: unknown;
}

function getLogFilePath(homeDir: string): string {
  const today = new Date().toISOString().split("T")[0]; // YYYY-MM-DD
  let logsDir = process.env.GROVE_LOG_DIR;
  if (!logsDir) {
    const paths = getGrovePaths();
    logsDir = paths
      ? join(paths.state_dir, "logs")
      : join(homeDir, ".local", "state", "grove", "logs");
  }
  if (!existsSync(logsDir)) {
    mkdirSync(logsDir, { recursive: true });
  }
  return join(logsDir, `workspace-${today}.log`);
}

function createLogger(homeDir: string) {
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
      const logPath = getLogFilePath(homeDir);
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
  const log = createLogger(homeDir);

  log.info("Plugin initializing", {
    plugin_version: GROVE_PLUGIN_VERSION,
    home_dir: homeDir,
    working_dir: workingDir,
    worktree: worktree || null,
    directory: directory || null,
  });

  // Get XDG-compliant paths from core
  const grovePaths = getGrovePaths();

  // The grove-hooks filesystem session registry. The Go pipeline owns this
  // layout; the plugin only touches it for the flow rename dance, pointer
  // enrichment, and the no-grove-binary fallback.
  const sessionsDir = grovePaths
    ? join(grovePaths.state_dir, "hooks", "sessions")
    : join(homeDir, ".local", "state", "grove", "hooks", "sessions");
  const storageRoot = opencodeStorageRoot(homeDir);

  log.debug("Configured paths", {
    sessions_dir: sessionsDir,
    opencode_storage_root: storageRoot,
    xdg_paths_available: grovePaths !== null,
  });

  // --- grove hooks shell-out ---
  // All session state transitions go through `grove hooks <event>` so the Go
  // pipeline (daemon registration, status resolution, flow job mapping)
  // stays the single owner of session semantics. Synchronous so ordering is
  // deterministic w.r.t. the filesystem dance in session.created.
  const runGroveHook = (
    subcommand: string,
    payload: Record<string, unknown>
  ): boolean => {
    try {
      const result = Bun.spawnSync(["grove", "hooks", subcommand], {
        stdin: new TextEncoder().encode(JSON.stringify(payload)),
        cwd: workingDir,
        env: {
          ...process.env,
          // The Go pipeline derives the provider from this env var (default
          // "claude"); flow exports it for flow-launched sessions and this
          // fallback covers manually launched ones.
          GROVE_AGENT_PROVIDER: process.env.GROVE_AGENT_PROVIDER || "opencode",
          // getClaudePID prefers CLAUDE_PID over the (short-lived) hook
          // process's parent PID; hand it the live opencode PID.
          CLAUDE_PID: String(process.pid),
          // EnsureSessionExists reads PWD as the session working directory.
          PWD: workingDir,
        },
        stdout: "ignore",
        stderr: "pipe",
      });
      if (result.exitCode !== 0) {
        log.warn(`grove hooks ${subcommand} exited non-zero`, {
          exit_code: result.exitCode,
          stderr: result.stderr?.toString().slice(0, 500),
        });
        return false;
      }
      return true;
    } catch (e) {
      // Never break the agent because bookkeeping failed.
      log.error(`grove hooks ${subcommand} failed to spawn`, {
        error: String(e),
      });
      return false;
    }
  };

  // Merge extra fields into a session's metadata.json (read-modify-write).
  // Used to record the opencode transcript pointer alongside the standard
  // fields the Go pipeline writes.
  const enrichSessionMetadata = (
    sessionDir: string,
    fields: Record<string, unknown>
  ): boolean => {
    const metadataPath = join(sessionDir, "metadata.json");
    try {
      let metadata: Record<string, unknown> = {};
      if (existsSync(metadataPath)) {
        metadata = JSON.parse(readFileSync(metadataPath, "utf8"));
      }
      writeFileSync(
        metadataPath,
        JSON.stringify({ ...metadata, ...fields }, null, 2)
      );
      return true;
    } catch (e) {
      log.error("Failed to enrich session metadata", {
        metadata_path: metadataPath,
        error: String(e),
      });
      return false;
    }
  };

  // Store active session ID for tracking
  let activeSessionId: string | null = null;
  // Store flow job ID if this is a grove-flow managed session
  let flowJobId: string | null = process.env.GROVE_FLOW_JOB_ID || null;
  // Store flow job path (recorded in fallback metadata for job association)
  let flowJobPath: string | null = process.env.GROVE_FLOW_JOB_PATH || null;
  // Throttle for tool.execute.before activity updates
  let lastActivityUpdate = 0;
  const activityThrottleMs = 15_000;

  // Extract a session id from event properties across payload shapes
  // (session.created/deleted carry {info: Session}; status/idle carry
  // {sessionID}).
  const sessionIdFromProps = (props: Record<string, unknown>): string | null => {
    const info = props?.info as Record<string, unknown> | undefined;
    const id =
      info?.id ||
      props?.sessionID ||
      props?.id ||
      props?.session_id ||
      props?.sessionId ||
      (props?.session as Record<string, unknown>)?.id;
    return id ? String(id) : null;
  };

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

      if (event.type === "session.created") {
        // Extract native opencode session ID (ses_...) from the event
        const props = event.properties as Record<string, unknown>;
        const opencodeSessionId =
          sessionIdFromProps(props) || `opencode-${Date.now()}`;

        activeSessionId = String(opencodeSessionId);
        const opencodePID = process.pid;

        // The opencode transcript pointer: agentlogs resolves the fragment
        // store from these fields instead of scanning storage.
        const transcriptPointer = {
          native_session_id: activeSessionId,
          opencode_storage_root: storageRoot,
        };

        if (flowJobId) {
          // This is a grove-flow managed session (flow-preseeded rename
          // dance — load-bearing, keep in sync with flow's
          // OpencodeAgentProvider.Launch which registers the session dir
          // under the flow job ID before opencode starts):
          // 1. Rename sessions/{flowJobId} to sessions/{nativeSessionId}
          // 2. Update metadata with the native session ID, PID and pointer
          // 3. Report to the Go pipeline via `grove hooks session-start`

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

              // Update metadata with opencode session ID, PID and pointer
              const updatedMetadata = {
                ...existingMetadata,
                claude_session_id: activeSessionId, // native agent id slot (registry keys on this)
                pid: opencodePID,
                ...transcriptPointer,
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
              // Flow session directory doesn't exist (race condition or
              // grove-flow failed). Fall back to creating a new session
              // directory so the session is still discoverable.
              log.warn("Flow session directory not found, creating new session", {
                expected_dir: flowSessionDir,
              });
              mkdirSync(opencodeSessionDir, { recursive: true });
              const metadata = {
                session_id: flowJobId,
                claude_session_id: activeSessionId,
                provider: "opencode",
                pid: opencodePID,
                working_directory: workingDir,
                user: process.env.USER || "unknown",
                started_at: new Date().toISOString(),
                job_title: process.env.GROVE_FLOW_JOB_TITLE,
                plan_name: process.env.GROVE_FLOW_PLAN_NAME,
                job_file_path: flowJobPath,
                type: "interactive_agent",
                ...transcriptPointer,
              };
              writeFileSync(
                join(opencodeSessionDir, "metadata.json"),
                JSON.stringify(metadata, null, 2)
              );
              writeFileSync(join(opencodeSessionDir, "pid.lock"), String(opencodePID));
            }

            // Let the Go pipeline register/refresh daemon state. It resolves
            // the flow job id from the (just-renamed) session directory's
            // metadata and flips idle/pending sessions back to running.
            runGroveHook("session-start", {
              session_id: activeSessionId,
              transcript_path: "",
              hook_event_name: "SessionStart",
              cwd: workingDir,
            });
          } catch (e) {
            log.error("Failed to enrich flow session", {
              flow_job_id: flowJobId,
              opencode_session_id: activeSessionId,
              error: String(e),
            });
          }
        } else {
          // Standalone opencode session (not managed by grove-flow).
          // Register through the Go pipeline, then record the transcript
          // pointer next to the standard metadata it wrote.
          const sessionDir = join(sessionsDir, activeSessionId);

          log.info("Standalone session created", {
            session_id: activeSessionId,
            pid: opencodePID,
            working_dir: workingDir,
            session_dir: sessionDir,
          });

          const registered = runGroveHook("session-start", {
            session_id: activeSessionId,
            transcript_path: "",
            hook_event_name: "SessionStart",
            cwd: workingDir,
          });

          if (registered) {
            enrichSessionMetadata(sessionDir, {
              ...transcriptPointer,
              // Mark as opencode session so downstream consumers know this
              // session never auto-completes.
              type: "opencode_session",
            });
            try {
              writeFileSync(join(sessionDir, "pid.lock"), String(opencodePID));
            } catch (e) {
              log.warn("Failed to write pid.lock", { error: String(e) });
            }
          } else {
            // Thin documented fallback: `grove` unavailable or failed.
            // Write the filesystem registry entry directly (metadata.json +
            // pid.lock only — never the database) so the session remains
            // discoverable by the TUI and agentlogs.
            log.warn("session-start shell-out failed; writing filesystem registry fallback", {
              session_id: activeSessionId,
            });
            try {
              mkdirSync(sessionDir, { recursive: true });
              const metadata = {
                session_id: activeSessionId,
                claude_session_id: activeSessionId,
                provider: "opencode",
                type: "opencode_session",
                pid: opencodePID,
                working_directory: workingDir,
                user: process.env.USER || "unknown",
                started_at: new Date().toISOString(),
                ...transcriptPointer,
              };
              writeFileSync(
                join(sessionDir, "metadata.json"),
                JSON.stringify(metadata, null, 2)
              );
              writeFileSync(join(sessionDir, "pid.lock"), String(opencodePID));
            } catch (e) {
              log.error("Fallback registration failed", {
                session_id: activeSessionId,
                error: String(e),
              });
            }
          }
        }
      }

      if (event.type === "session.status") {
        const props = event.properties as Record<string, unknown>;
        const statusObj = props.status as { type?: string } | string | undefined;
        const rawStatus =
          typeof statusObj === "object" ? statusObj?.type || "" : statusObj || "";
        const sessionId = sessionIdFromProps(props) || activeSessionId;

        log.debug("Session status event", {
          status: rawStatus,
          session_id: sessionId,
          flow_job_id: flowJobId,
        });

        // busy/retry mean the agent is working -> running. idle is handled
        // by the session.idle event through the full stop pipeline, so it
        // is deliberately skipped here to avoid double handling.
        if (sessionId && (rawStatus === "busy" || rawStatus === "retry")) {
          runGroveHook("session-status", {
            session_id: sessionId,
            hook_event_name: "SessionStatus",
            status: rawStatus,
            cwd: workingDir,
          });
          lastActivityUpdate = Date.now();
        }
      }

      if (event.type === "session.idle") {
        const props = event.properties as Record<string, unknown>;
        const sessionId = sessionIdFromProps(props) || activeSessionId;

        log.info("Session idle event - calling stop hook", {
          session_id: sessionId,
          flow_job_id: flowJobId,
          working_dir: workingDir,
        });

        // End of turn: the stop pipeline resolves opencode's empty
        // exit_reason to idle (never auto-complete) and handles flow job
        // file status.
        if (sessionId) {
          runGroveHook("stop", {
            session_id: sessionId,
            hook_event_name: "stop",
            exit_reason: "", // Empty = normal end-of-turn, not a completion
            duration_ms: 0,
            cwd: workingDir,
          });
        }
      }

      if (event.type === "session.deleted") {
        const props = event.properties as Record<string, unknown>;
        const sessionIdToDelete =
          sessionIdFromProps(props) || activeSessionId || "unknown";

        log.info("Session deleted event", {
          session_id: sessionIdToDelete,
          flow_job_id: flowJobId,
        });

        // The Go pipeline marks the session terminal and cleans up the
        // registry directory.
        runGroveHook("session-end", {
          session_id: sessionIdToDelete,
          hook_event_name: "SessionEnd",
          reason: "deleted",
          cwd: workingDir,
        });

        if (sessionIdToDelete === activeSessionId) {
          activeSessionId = null;
        }
      }
    },

    "tool.execute.before": async (input) => {
      // Any tool execution means the session is active. Throttled so a
      // burst of tool calls doesn't spawn a subprocess per call — the Go
      // pipeline only needs to flip idle/pending back to running.
      const now = Date.now();
      if (now - lastActivityUpdate < activityThrottleMs) {
        return;
      }
      const sessionId =
        (input as { sessionID?: string })?.sessionID || activeSessionId;
      if (!sessionId) return;

      lastActivityUpdate = now;
      log.debug("Tool execute before - activity update", {
        session_id: sessionId,
        flow_job_id: flowJobId,
      });
      runGroveHook("session-status", {
        session_id: sessionId,
        hook_event_name: "SessionStatus",
        status: "busy",
        cwd: workingDir,
      });
    },
  };
};
