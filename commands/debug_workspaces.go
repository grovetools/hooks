package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	grovelogging "github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func NewDebugWorkspacesCmd() *cobra.Command {
	var jsonOutput bool
	var showRaw bool

	ulog := grovelogging.NewUnifiedLogger("grove-hooks.debug-workspaces")

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

				ulog.Info("Raw discovery output").
					Field("projects_count", len(result.Projects)).
					Pretty("=== RAW DISCOVERY OUTPUT ===").
					Emit()
				ulog.Info("Projects discovered").
					Field("count", len(result.Projects)).
					Pretty(fmt.Sprintf("Projects: %d", len(result.Projects))).
					Emit()
				for _, p := range result.Projects {
					ulog.Info("Project").
						Field("name", p.Name).
						Field("path", p.Path).
						Pretty(fmt.Sprintf("  - %s (%s)", p.Name, p.Path)).
						Emit()
				}
				ulog.Info("Ecosystems discovered").
					Field("count", len(result.Ecosystems)).
					Pretty(fmt.Sprintf("\nEcosystems: %d", len(result.Ecosystems))).
					Emit()
				for _, e := range result.Ecosystems {
					ulog.Info("Ecosystem").
						Field("name", e.Name).
						Field("path", e.Path).
						Pretty(fmt.Sprintf("  - %s (%s)", e.Name, e.Path)).
						Emit()
				}
				ulog.Info("Non-Grove directories").
					Field("count", len(result.NonGroveDirectories)).
					Pretty(fmt.Sprintf("\nNonGroveDirectories: %d", len(result.NonGroveDirectories))).
					Emit()
				for _, d := range result.NonGroveDirectories {
					ulog.Info("Non-Grove directory").
						Field("path", d).
						Pretty(fmt.Sprintf("  - %s", d)).
						Emit()
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
				ulog.Info("Workspace node").
					Field("name", ws.Name).
					Field("path", ws.Path).
					Field("kind", ws.Kind).
					Field("depth", ws.Depth).
					Pretty(fmt.Sprintf("\n%s%s\n  Path: %s\n  Kind: %s\n  Depth: %d", ws.TreePrefix, ws.Name, ws.Path, ws.Kind, ws.Depth)).
					Emit()
				if ws.ParentProjectPath != "" {
					ulog.Info("Parent project path").
						Field("parent_project_path", ws.ParentProjectPath).
						Pretty(fmt.Sprintf("  ParentProjectPath: %s", ws.ParentProjectPath)).
						Emit()
				}
				if ws.ParentEcosystemPath != "" {
					ulog.Info("Parent ecosystem path").
						Field("parent_ecosystem_path", ws.ParentEcosystemPath).
						Pretty(fmt.Sprintf("  ParentEcosystemPath: %s", ws.ParentEcosystemPath)).
						Emit()
				}
				if ws.RootEcosystemPath != "" {
					ulog.Info("Root ecosystem path").
						Field("root_ecosystem_path", ws.RootEcosystemPath).
						Pretty(fmt.Sprintf("  RootEcosystemPath: %s", ws.RootEcosystemPath)).
						Emit()
				}
				parent := ws.GetHierarchicalParent()
				if parent != "" {
					ulog.Info("Hierarchical parent").
						Field("hierarchical_parent", parent).
						Pretty(fmt.Sprintf("  GetHierarchicalParent(): %s", parent)).
						Emit()
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&showRaw, "raw", false, "Show raw discovery output")

	return cmd
}
