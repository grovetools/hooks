package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	grovelogging "github.com/grovetools/core/logging"
	"github.com/spf13/cobra"

	"github.com/grovetools/hooks/internal/hooks"
)

// codexNotifyCommand is the argv codex invokes after each completed turn.
// Codex appends one extra argument containing the event JSON (see
// codex-rs/core/src/config/mod.rs `notify` and
// codex-rs/hooks/src/legacy_notify.rs for the payload shape).
var codexNotifyCommand = []string{"grove", "hooks", "codex", "notify"}

// NewCodexCmd creates the `codex` command and its subcommands.
func NewCodexCmd() *cobra.Command {
	codexCmd := &cobra.Command{
		Use:   "codex",
		Short: "Manage Codex CLI integration",
		Long:  "Commands for integrating the Codex CLI with grove-hooks for session lifecycle tracking.",
	}

	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Configure codex's notify hook to report turn completion to grove",
		Long: `Configure the Codex CLI to invoke grove-hooks after each completed turn.

This sets the top-level "notify" key in ~/.codex/config.toml (or
$CODEX_HOME/config.toml) to invoke "grove hooks codex notify". Codex spawns
that command after every completed turn with an agent-turn-complete JSON
payload, which grove translates into the standard Stop pipeline: the session
is marked idle at each end of turn, keeping codex agents visible in the TUI
and flow status instead of appearing dead after launch.`,
		RunE: runCodexInstall,
	}

	notifyCmd := &cobra.Command{
		Use:   "notify [payload-json]",
		Short: "Handle a codex notify event (invoked by codex, not by users)",
		Long: `Handle a codex notify event.

Codex invokes this command after each completed turn, appending a JSON
payload as the final argument:

  {"type":"agent-turn-complete","thread-id":"<uuid>","turn-id":"...",
   "cwd":"...","input-messages":[...],"last-assistant-message":"..."}

The event is translated into a Stop hook input (empty exit_reason = normal
end of turn) and fed through the standard stop pipeline, marking the session
idle. Flow-launched sessions resolve via the GROVE_FLOW_JOB_ID environment
variable that codex inherits; other sessions fall back to the codex thread id.`,
		Args: cobra.ArbitraryArgs,
		RunE: runCodexNotify,
	}

	codexCmd.AddCommand(installCmd)
	codexCmd.AddCommand(notifyCmd)
	return codexCmd
}

// codexConfigPath returns the codex config.toml location, honoring CODEX_HOME
// (codex-rs/utils/home-dir: CODEX_HOME overrides the default ~/.codex).
func codexConfigPath() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "config.toml"), nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not get home directory: %w", err)
	}
	return filepath.Join(homeDir, ".codex", "config.toml"), nil
}

func runCodexInstall(cmd *cobra.Command, args []string) error {
	ulog := grovelogging.NewUnifiedLogger("grove-hooks.codex")

	configPath, err := codexConfigPath()
	if err != nil {
		return err
	}

	var content string
	if raw, err := os.ReadFile(configPath); err == nil {
		content = string(raw)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read codex config: %w", err)
	}

	updated, changed, previous := upsertCodexNotify(content)
	if !changed {
		ulog.Info("Already installed").
			Field("config_path", configPath).
			Pretty(fmt.Sprintf("* Codex notify hook already configured in %s", configPath)).
			Emit()
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("failed to create codex config directory: %w", err)
	}
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("failed to write codex config: %w", err)
	}

	if previous != "" {
		ulog.Warn("Replaced existing notify setting").
			Field("previous", previous).
			Pretty(fmt.Sprintf("! Replaced previous notify setting: %s", previous)).
			Emit()
	}
	ulog.Success("Codex notify hook installed").
		Field("config_path", configPath).
		Pretty(fmt.Sprintf("* Codex notify hook configured in %s", configPath)).
		Emit()
	ulog.Info("Restart required").
		Pretty("Restart codex for the notify hook to take effect.").
		Emit()

	return nil
}

// codexNotifyLineRe matches a top-level notify assignment line in config.toml.
var codexNotifyLineRe = regexp.MustCompile(`(?m)^[ \t]*notify[ \t]*=.*$`)

// upsertCodexNotify sets the top-level notify key in the given config.toml
// content to the grove notify command, editing line-wise so user comments and
// formatting survive. It returns the updated content, whether anything
// changed, and any previous notify value that was replaced. Only a notify
// assignment in the top-level section (before the first [table] header) is
// considered; a missing key is inserted at the top of the file so it can't
// land inside a trailing table.
func upsertCodexNotify(content string) (updated string, changed bool, previous string) {
	quoted := make([]string, len(codexNotifyCommand))
	for i, tok := range codexNotifyCommand {
		quoted[i] = fmt.Sprintf("%q", tok)
	}
	notifyLine := fmt.Sprintf("notify = [%s]", strings.Join(quoted, ", "))

	// Only the content before the first table header is top-level TOML.
	topLevelEnd := len(content)
	if idx := regexp.MustCompile(`(?m)^\s*\[`).FindStringIndex(content); idx != nil {
		topLevelEnd = idx[0]
	}

	if loc := codexNotifyLineRe.FindStringIndex(content[:topLevelEnd]); loc != nil {
		existing := content[loc[0]:loc[1]]
		if strings.TrimSpace(existing) == notifyLine {
			return content, false, ""
		}
		return content[:loc[0]] + notifyLine + content[loc[1]:], true, strings.TrimSpace(existing)
	}

	if content == "" {
		return notifyLine + "\n", true, ""
	}
	return notifyLine + "\n" + content, true, ""
}

// codexNotifyPayload is the JSON codex appends as the final notify argument
// (codex-rs/hooks/src/legacy_notify.rs UserNotification::AgentTurnComplete).
type codexNotifyPayload struct {
	Type                 string   `json:"type"`
	ThreadID             string   `json:"thread-id"`
	TurnID               string   `json:"turn-id"`
	Cwd                  string   `json:"cwd"`
	InputMessages        []string `json:"input-messages"`
	LastAssistantMessage string   `json:"last-assistant-message"`
}

func runCodexNotify(cmd *cobra.Command, args []string) error {
	ulog := grovelogging.NewUnifiedLogger("grove-hooks.codex.notify")

	stopInput, ok := buildCodexStopInput(args, os.Getenv("GROVE_FLOW_JOB_ID"))
	if !ok {
		// Not an event we handle (or malformed) — exit quietly; codex ignores
		// notify failures and there is no user at this terminal.
		ulog.Debug("Ignoring codex notify invocation").
			Field("arg_count", len(args)).
			Emit()
		return nil
	}

	hooks.RunStopHookWithInput(stopInput)
	return nil
}

// buildCodexStopInput translates a codex notify argv into a Stop hook payload.
// Codex appends the event JSON as the final argument. Only agent-turn-complete
// events are handled: they mark the end of a turn (the codex process is still
// alive awaiting input), so exit_reason stays empty and the stop pipeline
// resolves the session to idle. Flow-launched codex sessions resolve via the
// inherited GROVE_FLOW_JOB_ID; other sessions use the codex thread id, which
// flow stores as the session's native id.
func buildCodexStopInput(args []string, flowJobID string) ([]byte, bool) {
	if len(args) == 0 {
		return nil, false
	}
	var payload codexNotifyPayload
	if err := json.Unmarshal([]byte(args[len(args)-1]), &payload); err != nil {
		return nil, false
	}
	if payload.Type != "agent-turn-complete" {
		return nil, false
	}

	sessionID := flowJobID
	if sessionID == "" {
		sessionID = payload.ThreadID
	}
	if sessionID == "" {
		return nil, false
	}

	stop := map[string]any{
		"session_id":      sessionID,
		"hook_event_name": "stop",
		"exit_reason":     "", // end of turn, not process exit → idle
		"duration_ms":     0,
		"cwd":             payload.Cwd,
	}
	raw, err := json.Marshal(stop)
	if err != nil {
		return nil, false
	}
	return raw, true
}
