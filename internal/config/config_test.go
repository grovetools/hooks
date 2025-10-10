package config

import (
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()

	if cfg == nil {
		t.Fatal("defaultConfig() returned nil")
	}

	// Verify ntfy defaults
	if cfg.Ntfy.Enabled != false {
		t.Errorf("Expected Ntfy.Enabled to be false, got %v", cfg.Ntfy.Enabled)
	}
	if cfg.Ntfy.Topic != "" {
		t.Errorf("Expected Ntfy.Topic to be empty, got %q", cfg.Ntfy.Topic)
	}
	if cfg.Ntfy.URL != "https://ntfy.sh" {
		t.Errorf("Expected Ntfy.URL to be https://ntfy.sh, got %q", cfg.Ntfy.URL)
	}

	// Verify system defaults
	if len(cfg.System.Levels) != 2 {
		t.Errorf("Expected System.Levels to have 2 items, got %d", len(cfg.System.Levels))
	}
	expectedLevels := []string{"error", "warning"}
	for i, level := range expectedLevels {
		if cfg.System.Levels[i] != level {
			t.Errorf("Expected System.Levels[%d] to be %q, got %q", i, level, cfg.System.Levels[i])
		}
	}
}

func TestLoad(t *testing.T) {
	// Test that Load() doesn't panic and returns a valid config
	cfg := Load()

	if cfg == nil {
		t.Fatal("Load() returned nil")
	}

	// At minimum, should have default values
	if cfg.Ntfy.URL == "" {
		t.Error("Expected Ntfy.URL to be set to at least the default value")
	}

	if len(cfg.System.Levels) == 0 {
		t.Error("Expected System.Levels to have at least default values")
	}
}
