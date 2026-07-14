package localpath

import (
	"path/filepath"
	"testing"
)

func TestExpandTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)        // POSIX
	t.Setenv("USERPROFILE", home) // Windows

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"bare tilde", "~", home},
		{"tilde slash", "~/", home},
		{"tilde subdir", "~/Downloads", filepath.Join(home, "Downloads")},
		{"tilde nested", "~/Downloads/sub/", filepath.Join(home, "Downloads/sub")},
		{"absolute unchanged", "/tmp/x", "/tmp/x"},
		{"relative unchanged", "./x", "./x"},
		{"tilde user unchanged", "~other/x", "~other/x"},
		{"tilde midpath unchanged", "a/~/b", "a/~/b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExpandTilde(tt.input); got != tt.want {
				t.Errorf("ExpandTilde(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
