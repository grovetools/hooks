package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/grovetools/core/config"
	corehooks "github.com/grovetools/hooks/internal/hooks"
	"github.com/spf13/cobra"
)

// resolveRepoRoot finds the directory containing grove.{toml,yml,yaml} by
// walking up from startDir. Unlike config.FindConfigFile this never falls
// back to the XDG config — disable markers must be tied to a real repo.
func resolveRepoRoot(startDir string) (string, error) {
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	candidates := []string{
		"grove.toml", "grove.yml", "grove.yaml",
		".grove.toml", ".grove.yml", ".grove.yaml",
	}
	dir := abs
	for {
		for _, name := range candidates {
			if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no grove.toml found at or above %s", startDir)
		}
		dir = parent
	}
}

// loadOnStopHooks resolves the repo root and returns its [[hooks.on_stop]]
// entries plus the resolved root path.
func loadOnStopHooks(repoFlag string) (string, []config.HookCommand, error) {
	start := repoFlag
	if start == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", nil, fmt.Errorf("get cwd: %w", err)
		}
		start = wd
	}
	root, err := resolveRepoRoot(start)
	if err != nil {
		return "", nil, err
	}
	cfg, err := config.LoadFrom(root)
	if err != nil {
		return root, nil, fmt.Errorf("load grove config: %w", err)
	}
	var hooksCfg config.HooksConfig
	if err := cfg.UnmarshalExtension("hooks", &hooksCfg); err != nil {
		return root, nil, fmt.Errorf("unmarshal hooks config: %w", err)
	}
	return root, hooksCfg.OnStop, nil
}

func newHookNameError(hookName string, available []config.HookCommand) error {
	names := make([]string, 0, len(available))
	for _, h := range available {
		names = append(names, h.Name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return fmt.Errorf("hook %q not found: no [[hooks.on_stop]] entries in grove.toml", hookName)
	}
	return fmt.Errorf("hook %q not found in [[hooks.on_stop]]; available: %s",
		hookName, strings.Join(names, ", "))
}

func newDisableHookCmd() *cobra.Command {
	var repoFlag, reason string
	cmd := &cobra.Command{
		Use:   "disable <hook-name>",
		Short: "Disable an on_stop hook for the current repo (creates a marker file)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hookName := args[0]
			root, onStop, err := loadOnStopHooks(repoFlag)
			if err != nil {
				return err
			}
			found := false
			for _, h := range onStop {
				if h.Name == hookName {
					found = true
					break
				}
			}
			if !found {
				return newHookNameError(hookName, onStop)
			}
			if err := corehooks.DisableHook(root, hookName, reason); err != nil {
				return fmt.Errorf("write marker: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Disabled hook %q for %s\n", hookName, filepath.Base(root))
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "Repo directory (defaults to cwd)")
	cmd.Flags().StringVar(&reason, "reason", "", "Optional reason recorded in the marker file")
	return cmd
}

func newEnableHookCmd() *cobra.Command {
	var repoFlag string
	cmd := &cobra.Command{
		Use:   "enable <hook-name>",
		Short: "Re-enable an on_stop hook for the current repo (removes the marker file)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hookName := args[0]
			root, onStop, err := loadOnStopHooks(repoFlag)
			if err != nil {
				return err
			}
			found := false
			for _, h := range onStop {
				if h.Name == hookName {
					found = true
					break
				}
			}
			if !found {
				return newHookNameError(hookName, onStop)
			}
			if err := corehooks.EnableHook(root, hookName); err != nil {
				return fmt.Errorf("remove marker: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Enabled hook %q for %s\n", hookName, filepath.Base(root))
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "Repo directory (defaults to cwd)")
	return cmd
}

// hookListEntry is the JSON shape emitted by `grove hooks list --json`.
type hookListEntry struct {
	Name             string `json:"name"`
	Command          string `json:"command"`
	Disabled         bool   `json:"disabled"`
	DisableReason    string `json:"disable_reason,omitempty"`
	MarkerPath       string `json:"marker_path,omitempty"`
	DisableEnv       string `json:"disable_env,omitempty"`
	DisableEnvActive bool   `json:"disable_env_active,omitempty"`
	EnableEnv        string `json:"enable_env,omitempty"`
	EnableEnvActive  bool   `json:"enable_env_active,omitempty"`
}

func newListHooksCmd() *cobra.Command {
	var (
		repoFlag   string
		jsonOutput bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List on_stop hooks and their enabled/disabled state",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, onStop, err := loadOnStopHooks(repoFlag)
			if err != nil {
				return err
			}
			entries := make([]hookListEntry, 0, len(onStop))
			for _, h := range onStop {
				e := hookListEntry{
					Name:       h.Name,
					Command:    h.Command,
					Disabled:   corehooks.IsHookDisabledByMarker(root, h.Name),
					DisableEnv: h.DisableEnv,
					EnableEnv:  h.EnableEnv,
				}
				if e.Disabled {
					e.DisableReason = corehooks.HookDisableReason(root, h.Name)
					e.MarkerPath = corehooks.HookMarkerPath(root, h.Name)
				}
				if h.DisableEnv != "" && os.Getenv(h.DisableEnv) != "" {
					e.DisableEnvActive = true
				}
				if h.EnableEnv != "" && os.Getenv(h.EnableEnv) != "" {
					e.EnableEnvActive = true
				}
				entries = append(entries, e)
			}

			if jsonOutput {
				out := struct {
					Repo  string          `json:"repo"`
					Hooks []hookListEntry `json:"hooks"`
				}{Repo: root, Hooks: entries}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			if len(entries) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No [[hooks.on_stop]] entries in %s\n", root)
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tSTATE\tCOMMAND\tNOTE")
			for _, e := range entries {
				state := "enabled"
				note := ""
				if e.Disabled {
					state = "disabled"
					if e.DisableReason != "" {
						note = "reason: " + e.DisableReason
					}
				}
				if e.DisableEnv != "" {
					gate := fmt.Sprintf("disable_env=%s(%s)", e.DisableEnv, envState(e.DisableEnvActive))
					note = appendNote(note, gate)
				}
				if e.EnableEnv != "" {
					gate := fmt.Sprintf("enable_env=%s(%s)", e.EnableEnv, envState(e.EnableEnvActive))
					note = appendNote(note, gate)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Name, state, truncateHookCmd(e.Command, 40), note)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "Repo directory (defaults to cwd)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit JSON")
	return cmd
}

func envState(active bool) string {
	if active {
		return "set"
	}
	return "unset"
}

func appendNote(existing, addition string) string {
	if existing == "" {
		return addition
	}
	return existing + "; " + addition
}

func truncateHookCmd(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
