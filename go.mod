module github.com/mattsolo1/grove-hooks

go 1.22.0

require (
	github.com/mattsolo1/grove-core v0.0.0-00010101000000-000000000000
	github.com/mattsolo1/grove-notifications v0.0.0-00010101000000-000000000000
	github.com/mattsolo1/grove-tmux v0.0.0-00010101000000-000000000000
	github.com/spf13/cobra v1.8.0
)

replace (
	github.com/mattsolo1/grove-core => ../grove-core
	github.com/mattsolo1/grove-notifications => ../grove-notifications
	github.com/mattsolo1/grove-tmux => ../grove-tmux
)