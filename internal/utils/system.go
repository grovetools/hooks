package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mattsolo1/grove-core/pkg/models"
)

// CopyToClipboard copies text to the system clipboard
func CopyToClipboard(text string) {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("pbcopy")
	} else {
		// Try xclip first, then xsel
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else {
			// No clipboard utility found
			return
		}
	}

	if cmd != nil {
		cmd.Stdin = strings.NewReader(text)
		cmd.Run()
	}
}

// OpenInFileManager opens a path in the system file manager
func OpenInFileManager(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "linux":
		cmd = exec.Command("xdg-open", path)
	case "windows":
		cmd = exec.Command("explorer", path)
	}

	if cmd != nil {
		cmd.Start()
	}
}

// ExportSessionToJSON exports a session to a JSON file
func ExportSessionToJSON(session *models.Session) {
	data, _ := json.MarshalIndent(session, "", "  ")
	filename := fmt.Sprintf("session_%s.json", session.ID[:8])
	os.WriteFile(filename, data, 0644)
}

// ExpandPath expands ~ to home directory, respecting XDG_DATA_HOME for .grove paths
func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		expandedPath := path[2:]

		// If the path is for .grove, respect XDG_DATA_HOME
		if strings.HasPrefix(expandedPath, ".grove/") {
			if xdgDataHome := os.Getenv("XDG_DATA_HOME"); xdgDataHome != "" {
				// Use XDG_DATA_HOME/... (strip .grove/ prefix since XDG_DATA_HOME already points to .grove)
				return filepath.Join(xdgDataHome, expandedPath[7:]) // Strip ".grove/"
			}
		}

		homeDir, _ := os.UserHomeDir() // Intentionally ignoring error to match existing behavior
		return filepath.Join(homeDir, expandedPath)
	}
	return path
}
