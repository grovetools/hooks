package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattsolo1/grove-hooks/internal/opencode/plugin"
	"github.com/spf13/cobra"
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
automatically appear in the grove-hooks TUI with live status updates.`,
		RunE: runOpencodeInstall,
	}

	opencodeCmd.AddCommand(installCmd)
	return opencodeCmd
}

func runOpencodeInstall(cmd *cobra.Command, args []string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not get home directory: %w", err)
	}

	pluginDir := filepath.Join(homeDir, ".config", "opencode", "plugin")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return fmt.Errorf("failed to create opencode plugin directory: %w", err)
	}

	pluginPath := filepath.Join(pluginDir, "grove-integration.ts")

	// Write the embedded plugin content to the file.
	if err := os.WriteFile(pluginPath, plugin.GroveIntegrationPlugin, 0644); err != nil {
		return fmt.Errorf("failed to write opencode plugin: %w", err)
	}

	fmt.Printf("✓ Grove integration plugin for opencode installed at: %s\n", pluginPath)
	fmt.Println("Please restart opencode for the plugin to take effect.")

	return nil
}
