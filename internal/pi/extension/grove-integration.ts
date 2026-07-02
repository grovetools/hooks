// Grove integration extension for the pi coding agent.
//
// Embedded into the grove-hooks binary and installed to
// ~/.pi/agent/extensions/grove-integration.ts by `hooks pi install`.
// pi auto-loads *.ts modules from that directory and invokes the default
// export with its ExtensionAPI (packages/coding-agent/src/core/extensions/
// loader.ts in the pi source).
//
// Design constraint: ALL session bookkeeping is routed through
// `grove hooks <event>` shell-outs (the Go stop/session-start pipeline).
// Do NOT talk to the hooks SQLite database or the daemon directly from here —
// the opencode plugin's direct-DB approach is a known mistake that is being
// unwound.
//
// Event wiring (event names/payloads per packages/coding-agent/src/core/
// extensions/types.ts):
//   - session_start     -> `grove hooks session-start` (registers the session
//                          + transcript path immediately)
//   - agent_end         -> `grove hooks stop` with empty exit_reason: pi is
//                          per-turn like opencode — the agent loop finished a
//                          prompt but the process is alive awaiting input, so
//                          the session goes IDLE, never completed.
//   - session_shutdown  -> only for reason "quit" (process teardown), sent as
//                          exit_reason "exited" so the stop pipeline can mark
//                          the session terminal. Reasons new/resume/fork/
//                          reload are session replacement within a live
//                          process: the follow-up session_start re-registers,
//                          so no stop is emitted for them.
//
// pi runs on Node (>= 22.19), so this uses node:child_process — not Bun APIs.

import { spawnSync } from "node:child_process";

interface GroveHookPayload {
	session_id: string;
	transcript_path: string;
	hook_event_name: string;
	cwd: string;
	exit_reason?: string;
	duration_ms?: number;
}

function runGroveHook(subcommand: string, payload: GroveHookPayload): void {
	try {
		const result = spawnSync("grove", ["hooks", subcommand], {
			input: JSON.stringify(payload),
			cwd: payload.cwd || process.cwd(),
			env: {
				...process.env,
				// EnsureSessionExists derives the provider from this env var
				// (default "claude"); flow exports it for flow-launched pi
				// sessions, and this fallback covers manually launched ones.
				GROVE_AGENT_PROVIDER: process.env.GROVE_AGENT_PROVIDER || "pi",
				// The Go pipeline reads PWD as the session working directory.
				PWD: payload.cwd || process.env.PWD || process.cwd(),
			},
			stdio: ["pipe", "ignore", "pipe"],
			timeout: 15000,
		});
		if (result.error) {
			console.error(`[grove-integration] grove hooks ${subcommand} failed:`, result.error.message);
		}
	} catch (e) {
		// Never break the agent because bookkeeping failed.
		console.error(`[grove-integration] grove hooks ${subcommand} threw:`, e);
	}
}

// ctx is pi's ExtensionContext: ctx.cwd plus ctx.sessionManager
// (getSessionId() / getSessionFile()). Types are erased at load time (pi
// loads extensions with jiti), so we keep this dependency-free.
function payloadFromCtx(ctx: any, eventName: string): GroveHookPayload {
	return {
		session_id: ctx?.sessionManager?.getSessionId?.() ?? "",
		transcript_path: ctx?.sessionManager?.getSessionFile?.() ?? "",
		hook_event_name: eventName,
		cwd: ctx?.cwd ?? process.cwd(),
	};
}

export default function groveIntegration(pi: any): void {
	// Register the session (and its transcript path) as soon as it starts —
	// this fires for startup/reload/new/resume/fork, and re-registration is
	// idempotent in the Go pipeline.
	pi.on("session_start", async (_event: any, ctx: any) => {
		const payload = payloadFromCtx(ctx, "SessionStart");
		if (!payload.session_id) return;
		runGroveHook("session-start", payload);
	});

	// End of each agent loop (each prompt): the pi process is still alive and
	// waiting for input, so this is a turn boundary, not completion. Empty
	// exit_reason resolves to "idle" in the stop pipeline.
	pi.on("agent_end", async (_event: any, ctx: any) => {
		const payload = payloadFromCtx(ctx, "stop");
		if (!payload.session_id) return;
		payload.exit_reason = "";
		payload.duration_ms = 0;
		runGroveHook("stop", payload);
	});

	// Process teardown. Only reason "quit" means the pi process is going away;
	// new/resume/fork/reload replace the session in a live process and are
	// followed by a session_start for the successor session.
	pi.on("session_shutdown", async (event: any, ctx: any) => {
		if (event?.reason !== "quit") return;
		const payload = payloadFromCtx(ctx, "stop");
		if (!payload.session_id) return;
		payload.exit_reason = "exited";
		payload.duration_ms = 0;
		runGroveHook("stop", payload);
	});
}
