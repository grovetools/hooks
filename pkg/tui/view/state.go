package view

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/grovetools/core/pkg/paths"
)

// tuiState holds persistent TUI settings for the hooks session browser.
type tuiState struct {
	FilterPreferences FilterPreferences `json:"filter_preferences"`
}

// getStateFilePath returns the path to the TUI state file.
func getStateFilePath() (string, error) {
	stateDir := filepath.Join(paths.StateDir(), "hooks")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "tui-state.json"), nil
}

// loadState loads the TUI state from disk or returns defaults.
func loadState() (*tuiState, error) {
	path, err := getStateFilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &tuiState{
				FilterPreferences: DefaultFilterPreferences(),
			}, nil
		}
		return nil, err
	}

	var state tuiState
	if err := json.Unmarshal(data, &state); err != nil {
		return &tuiState{
			FilterPreferences: DefaultFilterPreferences(),
		}, nil
	}

	// Ensure filter maps are not nil
	if state.FilterPreferences.StatusFilters == nil || state.FilterPreferences.TypeFilters == nil {
		state.FilterPreferences = DefaultFilterPreferences()
		return &state, nil
	}

	// Merge with defaults so newly added filters surface on upgrade.
	defaults := DefaultFilterPreferences()
	for k, v := range defaults.StatusFilters {
		if _, ok := state.FilterPreferences.StatusFilters[k]; !ok {
			state.FilterPreferences.StatusFilters[k] = v
		}
	}
	for k, v := range defaults.TypeFilters {
		if _, ok := state.FilterPreferences.TypeFilters[k]; !ok {
			state.FilterPreferences.TypeFilters[k] = v
		}
	}

	return &state, nil
}

// saveFilterState saves the filter preferences to disk.
func saveFilterState(prefs FilterPreferences) error {
	path, err := getStateFilePath()
	if err != nil {
		return err
	}

	state := tuiState{
		FilterPreferences: prefs,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
