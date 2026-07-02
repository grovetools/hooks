package plugin

import (
	"bytes"
	"testing"
)

// TestEmbeddedVersion guards the version stamp: the TS file is the single
// source of truth, so a refactor that drops or mangles the
// GROVE_PLUGIN_VERSION export would silently disable drift detection.
func TestEmbeddedVersion(t *testing.T) {
	if v := EmbeddedVersion(); v == "" {
		t.Fatal("embedded opencode plugin has no parseable GROVE_PLUGIN_VERSION stamp")
	}
}

// TestNoDirectDatabaseAccess enforces the v2 architecture: all session
// bookkeeping goes through `grove hooks <event>` shell-outs; the plugin must
// never reopen the hooks SQLite database (the v1 mistake).
func TestNoDirectDatabaseAccess(t *testing.T) {
	for _, forbidden := range []string{"bun:sqlite", "sessions.db"} {
		if bytes.Contains(GroveIntegrationPlugin, []byte(forbidden)) {
			t.Errorf("embedded plugin contains forbidden reference %q — direct DB access was removed in v2", forbidden)
		}
	}
}

// TestFlowRenameDancePreserved guards the load-bearing flow-preseeded-session
// rename: flow registers the session dir under the flow job ID before
// opencode starts, and the plugin must rename it to the native session ID.
func TestFlowRenameDancePreserved(t *testing.T) {
	for _, required := range []string{
		"GROVE_FLOW_JOB_ID",
		"renameSync",
		"claude_session_id",
	} {
		if !bytes.Contains(GroveIntegrationPlugin, []byte(required)) {
			t.Errorf("embedded plugin missing %q — the flow rename dance must be preserved", required)
		}
	}
}

// TestShellOutHandlers checks the grove hooks events the plugin routes
// through, and the transcript pointer fields it records.
func TestShellOutHandlers(t *testing.T) {
	for _, required := range []string{
		`"session-start"`,
		`"session-status"`,
		`"session-end"`,
		`"stop"`,
		"native_session_id",
		"opencode_storage_root",
		"GROVE_AGENT_PROVIDER",
	} {
		if !bytes.Contains(GroveIntegrationPlugin, []byte(required)) {
			t.Errorf("embedded plugin missing expected marker %q", required)
		}
	}
}
