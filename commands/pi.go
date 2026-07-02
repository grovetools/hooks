package commands

import (
	"fmt"
	"os"
	"path/filepath"

	grovelogging "github.com/grovetools/core/logging"
	"github.com/spf13/cobra"

	"github.com/grovetools/hooks/internal/pi/extension"
)

// NewPiCmd creates the `pi` command and its subcommands.
func NewPiCmd() *cobra.Command {
	piCmd := &cobra.Command{
		Use:   "pi",
		Short: "Manage pi coding agent integration",
		Long:  "Commands for integrating the pi coding agent with grove-hooks for session lifecycle tracking.",
	}

	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install the Grove integration extension for pi",
		Long: `Install the Grove integration extension for the pi coding agent.

This command writes grove-integration.ts to your global pi extension
directory (~/.pi/agent/extensions/). pi auto-loads extensions from that
directory on startup. Once installed, pi sessions register with the grove
session pipeline (via 'grove hooks session-start' / 'grove hooks stop'
shell-outs): sessions appear in the grove-hooks TUI, go idle at each end of
turn (agent_end), and are marked terminal when pi exits (session_shutdown).`,
		RunE: runPiInstall,
	}

	piCmd.AddCommand(installCmd)
	return piCmd
}

// piExtensionDir returns pi's global extension directory, honoring
// PI_CODING_AGENT_DIR (pi's override for ~/.pi/agent — see getAgentDir in
// packages/coding-agent/src/config.ts of the pi source).
func piExtensionDir() (string, error) {
	if agentDir := os.Getenv("PI_CODING_AGENT_DIR"); agentDir != "" {
		return filepath.Join(agentDir, "extensions"), nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not get home directory: %w", err)
	}
	return filepath.Join(homeDir, ".pi", "agent", "extensions"), nil
}

func runPiInstall(cmd *cobra.Command, args []string) error {
	ulog := grovelogging.NewUnifiedLogger("grove-hooks.pi")

	extensionDir, err := piExtensionDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(extensionDir, 0o755); err != nil {
		return fmt.Errorf("failed to create pi extension directory: %w", err)
	}

	extensionPath := filepath.Join(extensionDir, "grove-integration.ts")

	// Write the embedded extension content to the file.
	if err := os.WriteFile(extensionPath, extension.GroveIntegrationExtension, 0o644); err != nil {
		return fmt.Errorf("failed to write pi extension: %w", err)
	}

	ulog.Success("Grove integration extension installed").
		Field("extension_path", extensionPath).
		Pretty(fmt.Sprintf("* Grove integration extension for pi installed at: %s", extensionPath)).
		Emit()
	ulog.Info("Restart required").
		Pretty("Please restart pi for the extension to take effect.").
		Emit()

	return nil
}
