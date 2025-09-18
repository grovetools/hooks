package git

import (
	"os/exec"
	"path/filepath"
	"strings"
)

type Info struct {
	Repository string
	Branch     string
}

func GetInfo(workingDir string) *Info {
	info := &Info{
		Repository: filepath.Base(workingDir),
		Branch:     "unknown",
	}

	// Check if we're in a git repository. If not, return the working directory's base name.
	cmd := exec.Command("git", "-C", workingDir, "rev-parse", "--is-inside-work-tree")
	if output, err := cmd.Output(); err != nil || strings.TrimSpace(string(output)) != "true" {
		return info
	}

	// Get the repository root directory path. This is the most reliable method for both
	// standard repositories and worktrees.
	cmd = exec.Command("git", "-C", workingDir, "rev-parse", "--show-toplevel")
	if output, err := cmd.Output(); err == nil {
		topLevel := strings.TrimSpace(string(output))
		info.Repository = filepath.Base(topLevel)
	}

	// Get the current branch name
	cmd = exec.Command("git", "-C", workingDir, "rev-parse", "--abbrev-ref", "HEAD")
	if output, err := cmd.Output(); err == nil {
		branch := strings.TrimSpace(string(output))
		if branch != "" {
			info.Branch = branch
		}
	}

	return info
}