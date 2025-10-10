package config

import (
	"log"

	"github.com/mattsolo1/grove-core/config"
)

// NotificationsConfig represents the structure of the 'notifications' section in grove.yml
type NotificationsConfig struct {
	Ntfy   NtfyConfig   `yaml:"ntfy"`
	System SystemConfig `yaml:"system"`
}

// NtfyConfig holds settings for ntfy.sh notifications.
type NtfyConfig struct {
	Enabled bool   `yaml:"enabled"`
	Topic   string `yaml:"topic"`
	URL     string `yaml:"url"`
}

// SystemConfig holds settings for native system notifications.
type SystemConfig struct {
	// Levels specifies which notification levels should trigger a system notification.
	// e.g., ["error", "warning"]
	Levels []string `yaml:"levels"`
}

// Load reads the merged grove configuration and parses the 'notifications' extension.
func Load() *NotificationsConfig {
	// Start with a safe default configuration.
	cfg := defaultConfig()

	// Use grove-core to load the complete, merged configuration from the environment.
	coreCfg, err := config.LoadDefault()
	if err != nil {
		// It's common for no config to exist; this is not a fatal error.
		// We'll proceed with the defaults.
		log.Printf("Debug: No grove config found, using default notification settings: %v", err)
		return cfg
	}

	// Unmarshal the 'notifications' key from the Extensions map into our struct.
	var userCfg NotificationsConfig
	if err := coreCfg.UnmarshalExtension("notifications", &userCfg); err != nil {
		log.Printf("Warning: could not parse 'notifications' config section: %v. Using defaults.", err)
		return cfg
	}

	// Merge user-provided values over the defaults.
	// For bools, we need to check if the user explicitly set them
	// Since Go's zero value for bool is false, we check other fields to determine intent
	if userCfg.Ntfy.Topic != "" || userCfg.Ntfy.URL != "" {
		cfg.Ntfy.Enabled = userCfg.Ntfy.Enabled
	}
	if userCfg.Ntfy.Topic != "" {
		cfg.Ntfy.Topic = userCfg.Ntfy.Topic
	}
	if userCfg.Ntfy.URL != "" {
		cfg.Ntfy.URL = userCfg.Ntfy.URL
	}
	if len(userCfg.System.Levels) > 0 {
		cfg.System.Levels = userCfg.System.Levels
	}

	return cfg
}

func defaultConfig() *NotificationsConfig {
	return &NotificationsConfig{
		Ntfy: NtfyConfig{
			Enabled: false,
			Topic:   "",
			URL:     "https://ntfy.sh",
		},
		System: SystemConfig{
			Levels: []string{"error", "warning"},
		},
	}
}
