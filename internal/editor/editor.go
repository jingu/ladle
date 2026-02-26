// Package editor handles launching the user's editor and managing temp files.
package editor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ResolveEditor determines which editor to use based on the priority:
// 1. explicit flag, 2. LADLE_EDITOR, 3. EDITOR, 4. VISUAL, 5. "vi"
func ResolveEditor(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	for _, env := range []string{"LADLE_EDITOR", "EDITOR", "VISUAL"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	return "vi"
}

// TempFile creates a temporary file with the given filename (preserving extension)
// and writes content to it. Returns the file path.
func TempFile(filename string, content []byte) (string, error) {
	dir, err := os.MkdirTemp("", "ladle-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}

	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, content, 0600); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("writing temp file: %w", err)
	}
	return path, nil
}

// Open launches the editor for the given file path and waits for it to exit.
func Open(editor, filePath string) error {
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		return fmt.Errorf("empty editor command")
	}

	args := append(parts[1:], filePath)
	cmd := exec.Command(parts[0], args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor exited with error: %w", err)
	}
	return nil
}

// Cleanup removes the temp file and its parent directory.
func Cleanup(path string) {
	dir := filepath.Dir(path)
	os.RemoveAll(dir)
}

// IsBinary checks if the content appears to be binary by looking for null bytes
// in the first 8KB.
func IsBinary(data []byte) bool {
	limit := 8192
	if len(data) < limit {
		limit = len(data)
	}
	for i := 0; i < limit; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}
