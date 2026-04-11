package commands

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/compositor"
	"github.com/grovetools/core/config"
	grovelogging "github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/tui/embed"
	view "github.com/grovetools/hooks/pkg/tui/view"
	"github.com/spf13/cobra"
)

// NewBrowseCmd is the standalone CLI entry point for the embedded
// session-browser meta-panel. It is now a thin shim around view.New +
// embed.RunStandalone — the historical session preflight, workspace
// discovery, and exit-command-running boilerplate all moved into the
// view package or became unnecessary now that the model owns its own
// initial fetch and SSE-driven refresh loop.
func NewBrowseCmd() *cobra.Command {
	var hideCompleted bool

	ulog := grovelogging.NewUnifiedLogger("grove-hooks.browse")

	cmd := &cobra.Command{
		Use:     "browse",
		Aliases: []string{"b"},
		Short:   "Browse sessions interactively",
		Long: `Launch an interactive terminal UI to browse all sessions ` +
			`(Claude sessions and grove-flow jobs). Navigate with arrow ` +
			`keys, search by typing, and select sessions to view details.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Daemon client is shared with the view's session loader
			// and SSE subscription. Closed on RunE exit so the daemon
			// auto-stop logic kicks in for short-lived runs.
			client := daemon.NewWithAutoStart()
			defer client.Close()

			cfg, _ := config.LoadDefault()

			model := view.New(view.Config{
				DaemonClient:          client,
				Cfg:                   cfg,
				HideCompleted:         hideCompleted,
				GetAllSessions:        GetAllSessions,
				DispatchNotifications: DispatchStateChangeNotifications,
			})
			defer func() { _ = model.Close() }()

			// Use alt screen unless we're running under the Neovim
			// plugin (which needs the parent process to keep its
			// terminal grid intact for the editor handoff).
			var opts []tea.ProgramOption
			if os.Getenv("GROVE_NVIM_PLUGIN") != "true" {
				opts = append(opts, tea.WithAltScreen())
			}

			host := embed.NewStandaloneHost(model)
			compModel := compositor.NewModel(host)
			p := tea.NewProgram(compModel, opts...)
			finalModel, err := p.Run()
			if err != nil {
				ulog.Error("Error running program").Err(err).Emit()
				return err
			}

			// Free compositor resources and unwrap to recover the
			// StandaloneHost so post-exit assertions succeed.
			if cm, ok := finalModel.(*compositor.Model); ok {
				cm.Free()
				finalModel = cm.Unwrap()
			}
			_ = finalModel // StandaloneHost; model closed via defer

			return nil
		},
	}

	cmd.Flags().BoolVar(&hideCompleted, "active", false, "Show only active sessions (hide completed/failed)")

	return cmd
}
