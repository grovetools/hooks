package process

import (
	"os"
	"strconv"
)

// GetParentPID returns the parent process ID of the current process
func GetParentPID() int {
	ppid := os.Getppid()
	return ppid
}

// GetClaudePID attempts to find the Claude process PID from the environment
// For now, we'll use the parent PID as a simple approach
func GetClaudePID() int {
	// First check if CLAUDE_PID is set in environment
	if pidStr := os.Getenv("CLAUDE_PID"); pidStr != "" {
		if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
			return pid
		}
	}
	
	// For now, use parent PID as a simple approach
	// In the future, we could use more sophisticated process tree walking
	return os.Getppid()
}