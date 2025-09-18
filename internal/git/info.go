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

	// Check if we're in a git repository
	cmd := exec.Command("git", "-C", workingDir, "rev-parse", "--is-inside-work-tree")
	if output, err := cmd.Output(); err != nil || strings.TrimSpace(string(output)) != "true" {
		return info
	}

	// Get the repository name - handle worktrees properly
	// First try to get the git common directory (works for worktrees)
	cmd = exec.Command("git", "-C", workingDir, "rev-parse", "--git-common-dir")
	if output, err := cmd.Output(); err == nil {
		gitDir := strings.TrimSpace(string(output))
		// The repository name is the parent directory of .git
		repoPath := filepath.Dir(gitDir)
		info.Repository = filepath.Base(repoPath)
	} else {
		// Fallback to show-toplevel for regular repos
		cmd = exec.Command("git", "-C", workingDir, "rev-parse", "--show-toplevel")
		if output, err := cmd.Output(); err == nil {
			topLevel := strings.TrimSpace(string(output))
			info.Repository = filepath.Base(topLevel)
		}
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