package commands

import (
	"fmt"
	"os"
	"path/filepath"

	grovelogging "github.com/grovetools/core/logging"
	"github.com/spf13/cobra"

	"github.com/grovetools/hooks/internal/opencode/plugin"
)

// NewOpencodeCmd creates the `opencode` command and its subcommands.
func NewOpencodeCmd() *cobra.Command {
	opencodeCmd := &cobra.Command{
		Use:   "opencode",
		Short: "Manage opencode integration",
		Long:  "Commands for integrating opencode with grove-hooks for session monitoring in the TUI.",
	}

	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install the Grove integration plugin for opencode",
		Long: `Install the Grove integration plugin for opencode.

This command writes the grove-integration.ts plugin to your global opencode plugin
directory (~/.config/opencode/plugin/). Once installed, opencode sessions will
automatically appear in the grove-hooks TUI with live status updates.

The plugin carries a version stamp (GROVE_PLUGIN_VERSION); install reports the
previously installed version so drift is visible. Use 'hooks opencode status'
to check for staleness without reinstalling.`,
		RunE: runOpencodeInstall,
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Report installed vs embedded plugin version",
		Long: `Report the opencode integration plugin's drift status.

Compares the installed ~/.config/opencode/plugin/grove-integration.ts against
the copy embedded in this binary and reports current/stale/modified/not-installed.`,
		RunE: runOpencodeStatus,
	}

	opencodeCmd.AddCommand(installCmd)
	opencodeCmd.AddCommand(statusCmd)
	return opencodeCmd
}

// opencodePluginPath returns the global opencode plugin install path.
func opencodePluginPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not get home directory: %w", err)
	}
	return filepath.Join(homeDir, ".config", "opencode", "plugin", "grove-integration.ts"), nil
}

func runOpencodeInstall(cmd *cobra.Command, args []string) error {
	ulog := grovelogging.NewUnifiedLogger("grove-hooks.opencode")

	pluginPath, err := opencodePluginPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(pluginPath), 0o755); err != nil {
		return fmt.Errorf("failed to create opencode plugin directory: %w", err)
	}

	// Inspect what is currently installed before overwriting so version
	// drift is reported instead of silently papered over.
	before := inspectArtifact(pluginPath, plugin.GroveIntegrationPlugin)

	if before.Verdict == "current" {
		ulog.Info("Already up to date").
			Field("plugin_path", pluginPath).
			Field("version", before.EmbeddedVersion).
			Pretty(fmt.Sprintf("* Grove integration plugin already up to date (version %s)", describeVersion(before.EmbeddedVersion))).
			Emit()
		return nil
	}

	// Write the embedded plugin content to the file.
	if err := os.WriteFile(pluginPath, plugin.GroveIntegrationPlugin, 0o644); err != nil {
		return fmt.Errorf("failed to write opencode plugin: %w", err)
	}

	switch before.Verdict {
	case "not-installed":
		ulog.Success("Grove integration plugin installed").
			Field("plugin_path", pluginPath).
			Field("version", plugin.EmbeddedVersion()).
			Pretty(fmt.Sprintf("* Grove integration plugin for opencode installed at: %s (version %s)", pluginPath, describeVersion(plugin.EmbeddedVersion()))).
			Emit()
	default:
		ulog.Success("Grove integration plugin updated").
			Field("plugin_path", pluginPath).
			Field("previous_version", before.InstalledVersion).
			Field("version", plugin.EmbeddedVersion()).
			Pretty(fmt.Sprintf("* Grove integration plugin updated: %s -> %s", describeVersion(before.InstalledVersion), describeVersion(plugin.EmbeddedVersion()))).
			Emit()
	}

	ulog.Info("Restart required").
		Pretty("Please restart opencode for the plugin to take effect.").
		Emit()

	return nil
}

func runOpencodeStatus(cmd *cobra.Command, args []string) error {
	ulog := grovelogging.NewUnifiedLogger("grove-hooks.opencode")

	pluginPath, err := opencodePluginPath()
	if err != nil {
		return err
	}

	status := inspectArtifact(pluginPath, plugin.GroveIntegrationPlugin)
	emitArtifactStatus(ulog, "opencode plugin", "grove hooks opencode install", status)
	return nil
}
