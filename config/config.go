package config

// Config defines the structure for 'hooks' configuration in grove.yml.
type Config struct {
	// TUI defines settings for the interactive browser.
	TUI TUIConfig `yaml:"tui"`
}

// TUIConfig defines settings for the TUI.
type TUIConfig struct {
	// CacheEnabled controls whether the TUI caches session data.
	// Set to false during development to see real-time changes. Defaults to true.
	CacheEnabled *bool `yaml:"cache_enabled"`

	// CacheTTLSeconds controls how long the cache is valid in seconds.
	// Lower values give more real-time updates. Defaults to 60 seconds.
	// Set to 2-5 for development to get near-realtime updates with good performance.
	CacheTTLSeconds *int `yaml:"cache_ttl_seconds"`
}
