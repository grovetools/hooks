package process

import (
	"fmt"
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
			if os.Getenv("GROVE_DEBUG") != "" {
				fmt.Printf("Using CLAUDE_PID from env: %d\n", pid)
			}
			return pid
		}
	}
	
	// For now, use parent PID as a simple approach
	// In the future, we could use more sophisticated process tree walking
	ppid := os.Getppid()
	if os.Getenv("GROVE_DEBUG") != "" {
		fmt.Printf("Using parent PID: %d (current PID: %d)\n", ppid, os.Getpid())
	}
	return ppid
}