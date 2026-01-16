package commands

import (
	"encoding/json"
	"fmt"

	grovelogging "github.com/grovetools/core/logging"
	"github.com/grovetools/core/version"
	"github.com/spf13/cobra"
)

func NewVersionCmd() *cobra.Command {
	var jsonOutput bool

	ulog := grovelogging.NewUnifiedLogger("grove-hooks.version")

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version information for this binary",
		RunE: func(cmd *cobra.Command, args []string) error {
			info := version.GetInfo()

			if jsonOutput {
				jsonData, err := json.MarshalIndent(info, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to marshal version info to JSON: %w", err)
				}
				ulog.Info("Version information").
					Field("version", info.Version).
					Field("commit", info.Commit).
					Pretty(string(jsonData)).
					Emit()
			} else {
				ulog.Info("Version information").
					Field("version", info.Version).
					Pretty(info.String()).
					Emit()
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output version information in JSON format")

	return cmd
}