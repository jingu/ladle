// Package localpath contains helpers for handling local filesystem paths
// typed by the user (e.g. the download destination in the browser).
package localpath

import (
	"os"
	"path/filepath"
	"strings"
)

// ExpandTilde expands a leading "~" or "~/..." in path to the user's home
// directory: a bare "~" becomes the home directory and "~/x" becomes
// "$HOME/x". Any other path (including "~user") is returned unchanged. If the
// home directory cannot be resolved, path is returned unchanged.
func ExpandTilde(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[len("~/"):])
}
