package commands

import (
	"context"
	"encoding/json"
	"fmt"

	grovelogging "github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/version"
	"github.com/spf13/cobra"
)

func NewVersionCmd() *cobra.Command {
	var jsonOutput bool

	ulog := grovelogging.NewUnifiedLogger("grove-hooks.version")

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version information for this binary",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
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
					Log(ctx)
			} else {
				ulog.Info("Version information").
					Field("version", info.Version).
					Pretty(info.String()).
					Log(ctx)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output version information in JSON format")

	return cmd
}