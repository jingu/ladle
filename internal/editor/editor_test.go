package editor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveEditor(t *testing.T) {
	// Clear all env vars first
	origLadle := os.Getenv("LADLE_EDITOR")
	origEditor := os.Getenv("EDITOR")
	origVisual := os.Getenv("VISUAL")
	defer func() {
		os.Setenv("LADLE_EDITOR", origLadle)
		os.Setenv("EDITOR", origEditor)
		os.Setenv("VISUAL", origVisual)
	}()

	t.Run("flag takes priority", func(t *testing.T) {
		os.Setenv("LADLE_EDITOR", "nano")
		result := ResolveEditor("code")
		if result != "code" {
			t.Errorf("got %q, want %q", result, "code")
		}
	})

	t.Run("LADLE_EDITOR second priority", func(t *testing.T) {
		os.Setenv("LADLE_EDITOR", "emacs")
		os.Setenv("EDITOR", "vim")
		result := ResolveEditor("")
		if result != "emacs" {
			t.Errorf("got %q, want %q", result, "emacs")
		}
	})

	t.Run("EDITOR third priority", func(t *testing.T) {
		os.Unsetenv("LADLE_EDITOR")
		os.Setenv("EDITOR", "vim")
		os.Setenv("VISUAL", "code")
		result := ResolveEditor("")
		if result != "vim" {
			t.Errorf("got %q, want %q", result, "vim")
		}
	})

	t.Run("VISUAL fourth priority", func(t *testing.T) {
		os.Unsetenv("LADLE_EDITOR")
		os.Unsetenv("EDITOR")
		os.Setenv("VISUAL", "code")
		result := ResolveEditor("")
		if result != "code" {
			t.Errorf("got %q, want %q", result, "code")
		}
	})

	t.Run("fallback to vi", func(t *testing.T) {
		os.Unsetenv("LADLE_EDITOR")
		os.Unsetenv("EDITOR")
		os.Unsetenv("VISUAL")
		result := ResolveEditor("")
		if result != "vi" {
			t.Errorf("got %q, want %q", result, "vi")
		}
	})
}

func TestTempFile(t *testing.T) {
	content := []byte("hello world")
	path, err := TempFile("test.html", content)
	if err != nil {
		t.Fatalf("TempFile: %v", err)
	}
	defer Cleanup(path)

	if filepath.Base(path) != "test.html" {
		t.Errorf("filename: got %q, want %q", filepath.Base(path), "test.html")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading temp file: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content: got %q, want %q", string(data), string(content))
	}
}

func TestCleanup(t *testing.T) {
	path, err := TempFile("cleanup.txt", []byte("data"))
	if err != nil {
		t.Fatalf("TempFile: %v", err)
	}

	dir := filepath.Dir(path)
	Cleanup(path)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("expected dir to be removed, but it still exists")
	}
}

func TestIsBinary(t *testing.T) {
	tests := []struct {
		name   string
		data   []byte
		binary bool
	}{
		{"text file", []byte("hello world\nfoo bar\n"), false},
		{"binary with null", []byte("hello\x00world"), true},
		{"empty", []byte{}, false},
		{"utf8", []byte("こんにちは"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsBinary(tt.data)
			if got != tt.binary {
				t.Errorf("IsBinary(%q): got %v, want %v", tt.name, got, tt.binary)
			}
		})
	}
}
