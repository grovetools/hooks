package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	grovelogging "github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-hooks/internal/storage/disk"
	"github.com/mattsolo1/grove-hooks/internal/tui/browse"
	"github.com/mattsolo1/grove-hooks/internal/utils"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var browseFiltersPath = utils.ExpandPath("~/.grove/hooks/browse_filters.json")

// loadFilterPreferences loads saved filter preferences from disk
func loadFilterPreferences() browse.FilterPreferences {
	prefs := browse.FilterPreferences{
		StatusFilters: map[string]bool{
			"running":      true,
			"idle":         true,
			"pending_user": true,
			"completed":    true,
			"interrupted":  true,
			"failed":       true,
			"error":        true,
			"hold":         true,
			"todo":         true,
			"abandoned":    false, // Default to not show abandoned
		},
		TypeFilters: map[string]bool{
			"claude_code":       true,
			"chat":              true,
			"interactive_agent": true,
			"oneshot":           true,
			"headless_agent":    true,
			"agent":             true,
			"shell":             true,
			"opencode_session":  true,
		},
	}

	// Try to load from file
	data, err := os.ReadFile(browseFiltersPath)
	if err != nil {
		return prefs // Return defaults if file doesn't exist
	}

	var saved browse.FilterPreferences
	if err := json.Unmarshal(data, &saved); err != nil {
		return prefs // Return defaults if JSON is invalid
	}

	// Merge saved preferences with defaults (in case new filters were added)
	for k, v := range saved.StatusFilters {
		prefs.StatusFilters[k] = v
	}
	for k, v := range saved.TypeFilters {
		prefs.TypeFilters[k] = v
	}

	return prefs
}

// saveFilterPreferences saves filter preferences to disk
func saveFilterPreferences(prefs browse.FilterPreferences) error {
	data, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return err
	}

	// Ensure directory exists
	dir := filepath.Dir(browseFiltersPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(browseFiltersPath, data, 0644)
}

func NewBrowseCmd() *cobra.Command {
	var hideCompleted bool

	ulog := grovelogging.NewUnifiedLogger("grove-hooks.browse")

	cmd := &cobra.Command{
		Use:     "browse",
		Aliases: []string{"b"},
		Short:   "Browse sessions interactively",
		Long:    `Launch an interactive terminal UI to browse all sessions (Claude sessions and grove-flow jobs). Navigate with arrow keys, search by typing, and select sessions to view details.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Enable background cache refresh for the TUI (long-running)
			EnableBackgroundRefresh()
			StartBackgroundRefresh()

			// Create storage for session cleanup
			storage, err := disk.NewSQLiteStore()
			if err != nil {
				return fmt.Errorf("failed to create storage: %w", err)
			}
			defer storage.(*disk.SQLiteStore).Close()

			// Clean up dead sessions first
			_, _ = CleanupDeadSessions(storage)

			// Fetch all sessions using the centralized discovery function
			sessions, err := GetAllSessions(storage, hideCompleted)
			if err != nil {
				return fmt.Errorf("failed to get all sessions: %w", err)
			}

			if len(sessions) == 0 {
				ulog.Info("No sessions found").Log(ctx)
				return nil
			}

			// Load filter preferences
			prefs := loadFilterPreferences()

			// Discover workspaces
			logger := logrus.New()
			logger.SetOutput(io.Discard) // Suppress logs in the TUI
			workspaces, err := workspace.GetProjects(logger)
			if err != nil {
				// If workspace discovery fails, continue with empty workspaces
				workspaces = []*workspace.WorkspaceNode{}
			}

			// Create the interactive model using the extracted browse package
			m := browse.NewModel(
				sessions,
				workspaces,
				storage,
				hideCompleted,
				prefs,
				GetAllSessions,
				DispatchStateChangeNotifications,
				saveFilterPreferences,
			)

			// Run the interactive program
			// Use alt screen only when not in Neovim (to allow editor functionality)
			var opts []tea.ProgramOption
			if os.Getenv("GROVE_NVIM_PLUGIN") != "true" {
				opts = append(opts, tea.WithAltScreen())
			}
			p := tea.NewProgram(m, opts...)
			finalModel, err := p.Run()
			if err != nil {
				return fmt.Errorf("error running program: %w", err)
			}

			// Check if we need to run a command after the TUI exits
			if bm, ok := finalModel.(browse.Model); ok {
				if bm.CommandOnExit != nil {
					// The TUI needs to exit before we can run a command that
					// might switch the tmux client.
					bm.CommandOnExit.Stdin = os.Stdin
					bm.CommandOnExit.Stdout = os.Stdout
					bm.CommandOnExit.Stderr = os.Stderr
					if err := bm.CommandOnExit.Run(); err != nil {
						ulog.Error("Error executing command on exit").
							Err(err).
							Log(ctx)
					}
					return nil // Exit cleanly after command.
				}
				if bm.MessageOnExit != "" {
					ulog.Info("Exit message").
						Pretty(bm.MessageOnExit).
						Log(ctx)
					return nil
				}

				// Check if a session was selected
				if bm.SelectedSession() != nil {
					// Output the selected session details
					s := bm.SelectedSession()
					ulog.Info("Session selected").
						Field("session_id", s.ID).
						Field("status", s.Status).
						Field("type", s.Type).
						Field("repo", s.Repo).
						Field("branch", s.Branch).
						Field("working_directory", s.WorkingDirectory).
						Pretty(fmt.Sprintf("\nSelected Session: %s\nStatus: %s\nType: %s\n%sWorking Directory: %s",
							s.ID, s.Status, s.Type,
							func() string {
								if s.Repo != "" {
									return fmt.Sprintf("Repo: %s/%s\n", s.Repo, s.Branch)
								}
								return ""
							}(),
							s.WorkingDirectory)).
						Log(ctx)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&hideCompleted, "active", false, "Show only active sessions (hide completed/failed)")

	return cmd
}
