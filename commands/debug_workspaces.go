package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func NewDebugWorkspacesCmd() *cobra.Command {
	var jsonOutput bool
	var showRaw bool

	cmd := &cobra.Command{
		Use:   "debug-workspaces",
		Short: "Debug workspace hierarchy and node properties",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logrus.New()
			logger.SetOutput(io.Discard)

			if showRaw {
				// Show raw discovery output
				discoveryService := workspace.NewDiscoveryService(logger)
				result, err := discoveryService.DiscoverAll()
				if err != nil {
					return fmt.Errorf("failed to discover: %w", err)
				}

				fmt.Printf("=== RAW DISCOVERY OUTPUT ===\n")
				fmt.Printf("Projects: %d\n", len(result.Projects))
				for _, p := range result.Projects {
					fmt.Printf("  - %s (%s)\n", p.Name, p.Path)
				}
				fmt.Printf("\nEcosystems: %d\n", len(result.Ecosystems))
				for _, e := range result.Ecosystems {
					fmt.Printf("  - %s (%s)\n", e.Name, e.Path)
				}
				fmt.Printf("\nNonGroveDirectories: %d\n", len(result.NonGroveDirectories))
				for _, d := range result.NonGroveDirectories {
					fmt.Printf("  - %s\n", d)
				}
				return nil
			}

			workspaces, err := workspace.GetProjects(logger)
			if err != nil {
				return fmt.Errorf("failed to get workspaces: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(workspaces)
			}

			// Pretty print for debugging
			for _, ws := range workspaces {
				fmt.Printf("\n%s%s\n", ws.TreePrefix, ws.Name)
				fmt.Printf("  Path: %s\n", ws.Path)
				fmt.Printf("  Kind: %s\n", ws.Kind)
				fmt.Printf("  Depth: %d\n", ws.Depth)
				if ws.ParentProjectPath != "" {
					fmt.Printf("  ParentProjectPath: %s\n", ws.ParentProjectPath)
				}
				if ws.ParentEcosystemPath != "" {
					fmt.Printf("  ParentEcosystemPath: %s\n", ws.ParentEcosystemPath)
				}
				if ws.RootEcosystemPath != "" {
					fmt.Printf("  RootEcosystemPath: %s\n", ws.RootEcosystemPath)
				}
				parent := ws.GetHierarchicalParent()
				if parent != "" {
					fmt.Printf("  GetHierarchicalParent(): %s\n", parent)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&showRaw, "raw", false, "Show raw discovery output")

	return cmd
}
