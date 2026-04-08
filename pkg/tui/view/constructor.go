// Package view hosts the embeddable hooks session-browser meta-panel.
//
// Despite the historical name "hooks browse", this is a SESSION BROWSER for
// daemon-tracked agent sessions (Claude Code, flow plan jobs, oneshots,
// chats, etc.) — not a hook-config editor. It mirrors memory/pkg/tui/view
// and cx/pkg/tui/view: a self-contained tea.Model with no package-level
// globals so multiple instances can coexist safely inside a host
// multiplexer (terminal).
//
// Wiring lives on the Model struct. The host constructs a Config, calls
// New(cfg), and either runs the model standalone via embed.RunStandalone
// or embeds it inside a host panel that forwards embed.* messages.
package view

import (
	"context"
	"io"
	"path/filepath"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/sirupsen/logrus"
)

// Config holds the construction parameters for the hooks view meta-panel.
// All fields are optional except DaemonClient — the constructor falls back
// to sensible defaults so embedding callers (terminal panels, the standalone
// CLI shim) only need to plumb in what they care about.
type Config struct {
	// DaemonClient is the daemon connection used to fetch sessions and
	// (in Phase 2.1) subscribe to SSE updates. If nil, New falls back to
	// daemon.NewWithAutoStart().
	DaemonClient daemon.Client

	// Cfg supplies user keybinding overrides. Optional — defaults to the
	// loaded grove config.
	Cfg *config.Config

	// HideCompleted filters out terminal-state sessions on initial load.
	HideCompleted bool

	// FilterPreferences seeds the status/type filter checkboxes. The
	// constructor uses sane defaults if zero.
	FilterPreferences FilterPreferences

	// SaveFilterPreferences is invoked whenever the user toggles a filter
	// in the filter view. Optional — defaults to a no-op.
	SaveFilterPreferences SaveFilterPreferencesFunc

	// GetAllSessions overrides how sessions are fetched. Defaults to a
	// closure that calls client.GetSessions and merges in jobs from the
	// JobRunner — see defaultGetAllSessions. Phase 2.1 will reduce the
	// importance of this knob since the SSE stream becomes the primary
	// source of state.
	GetAllSessions GetAllSessionsFunc

	// DispatchNotifications is called whenever the polling loop produces
	// a fresh sessions slice so the host can fire ntfy/desktop alerts on
	// state transitions. Optional — defaults to a no-op.
	DispatchNotifications DispatchNotificationsFunc

	// InitialFocus is the workspace the host wants the panel to scope to
	// on first render. Optional — when nil, the panel shows all workspaces
	// (mirroring the standalone CLI behavior).
	InitialFocus *workspace.WorkspaceNode
}

// New constructs a hooks session-browser model from a Config. The returned
// model is ready to embed into a Bubble Tea program directly, or to wrap in
// embed.RunStandalone for the CLI path.
func New(cfg Config) Model {
	if cfg.DaemonClient == nil {
		cfg.DaemonClient = daemon.NewWithAutoStart()
	}
	if cfg.Cfg == nil {
		cfg.Cfg, _ = config.LoadDefault()
	}
	if cfg.GetAllSessions == nil {
		cfg.GetAllSessions = defaultGetAllSessions
	}
	if cfg.DispatchNotifications == nil {
		cfg.DispatchNotifications = func(_, _ []*models.Session) {}
	}
	if cfg.SaveFilterPreferences == nil {
		cfg.SaveFilterPreferences = func(FilterPreferences) error { return nil }
	}
	if cfg.FilterPreferences.StatusFilters == nil || cfg.FilterPreferences.TypeFilters == nil {
		cfg.FilterPreferences = DefaultFilterPreferences()
	}

	// Initial session fetch — use the configured loader so the panel
	// renders content on first frame instead of waiting for the first
	// SSE event. Errors are non-fatal: the model just starts empty and
	// the next refresh tick (Phase 2.1: SSE event) populates it.
	sessions, _ := cfg.GetAllSessions(cfg.DaemonClient, cfg.HideCompleted)

	// Workspace discovery for the tree view. Suppress logger noise so
	// stderr stays clean inside the host TUI.
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	workspaces, err := workspace.GetProjects(logger)
	if err != nil {
		workspaces = []*workspace.WorkspaceNode{}
	}

	m := NewModel(
		cfg.Cfg,
		sessions,
		workspaces,
		cfg.DaemonClient,
		cfg.HideCompleted,
		cfg.FilterPreferences,
		cfg.GetAllSessions,
		cfg.DispatchNotifications,
		cfg.SaveFilterPreferences,
	)
	if cfg.InitialFocus != nil {
		m.activeWorkspace = cfg.InitialFocus
		m.localScope = true
		m.hosted = true
		m.updateFilteredAndDisplayNodes()
	}
	return m
}

// defaultGetAllSessions is the in-process session loader used when the
// caller doesn't supply one. It performs the same daemon.GetSessions +
// ListJobs merge that hooks/commands/discovery.go does so embedding hosts
// (terminal) don't need to import commands/.
//
// This is a temporary scaffold — Phase 2.1 replaces the polling-based
// reload with an SSE subscription that pushes session state into the
// model directly, at which point the manual fetch only matters for the
// initial render.
func defaultGetAllSessions(client daemon.Client, hideCompleted bool) ([]*models.Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	all, err := client.GetSessions(ctx)
	if err != nil {
		all = []*models.Session{}
	}

	// Merge in jobs from the JobRunner so idle/pending chat jobs show up.
	seenIDs := make(map[string]struct{}, len(all))
	seenPaths := make(map[string]struct{}, len(all))
	for _, s := range all {
		seenIDs[s.ID] = struct{}{}
		if s.JobFilePath != "" {
			seenPaths[s.JobFilePath] = struct{}{}
		}
	}
	jobs, _ := client.ListJobs(ctx, models.JobFilter{})
	for _, j := range jobs {
		if _, ok := seenIDs[j.ID]; ok {
			continue
		}
		jobPath := filepath.Join(j.PlanDir, j.JobFile)
		if _, ok := seenPaths[jobPath]; ok {
			continue
		}
		s := &models.Session{
			ID:               j.ID,
			Status:           j.Status,
			PlanName:         j.PlanName,
			JobFilePath:      jobPath,
			WorkingDirectory: j.WorkDir,
			Repo:             j.Repo,
			Branch:           j.Branch,
		}
		if s.PlanName == "" {
			s.PlanName = filepath.Base(j.PlanDir)
		}
		s.Type = string(j.Type)
		if s.Type == "" {
			s.Type = "chat"
		}
		s.JobTitle = j.Title
		if !j.SubmittedAt.IsZero() {
			s.StartedAt = j.SubmittedAt
			s.LastActivity = j.SubmittedAt
		} else {
			now := time.Now()
			s.StartedAt = now
			s.LastActivity = now
		}
		all = append(all, s)
	}

	if hideCompleted {
		var filtered []*models.Session
		for _, s := range all {
			if s.Status != "completed" && s.Status != "failed" && s.Status != "error" && s.Status != "interrupted" {
				filtered = append(filtered, s)
			}
		}
		all = filtered
	}
	return all, nil
}

// DefaultFilterPreferences returns the canonical default filter set used by
// both the standalone CLI and the embedded panel.
func DefaultFilterPreferences() FilterPreferences {
	return FilterPreferences{
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
			"abandoned":    false,
		},
		TypeFilters: map[string]bool{
			"claude_code":       true,
			"chat":              true,
			"interactive_agent": true,
			"isolated_agent":    true,
			"oneshot":           true,
			"headless_agent":    true,
			"agent":             true,
			"shell":             true,
			"opencode_session":  true,
		},
	}
}
