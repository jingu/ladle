package browser

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jingu/ladle/internal/storage"
	"github.com/jingu/ladle/internal/uri"
)

func TestRunFileSelection(t *testing.T) {
	mock := storage.NewMockClient()
	mock.PutObject("mybucket", "file.txt", []byte("hello"), nil)

	u, err := uri.Parse("s3://mybucket/")
	if err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader("1\n")
	out := &bytes.Buffer{}

	b := New(mock, u, input, out)
	sel, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Action != ActionEdit {
		t.Fatalf("expected ActionEdit, got %d", sel.Action)
	}
	if sel.URI.Bucket != "mybucket" {
		t.Errorf("expected bucket %q, got %q", "mybucket", sel.URI.Bucket)
	}
	if sel.URI.Key != "file.txt" {
		t.Errorf("expected key %q, got %q", "file.txt", sel.URI.Key)
	}
}

func TestRunQuit(t *testing.T) {
	mock := storage.NewMockClient()
	mock.PutObject("mybucket", "file.txt", []byte("hello"), nil)

	u, err := uri.Parse("s3://mybucket/")
	if err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader("q\n")
	out := &bytes.Buffer{}

	b := New(mock, u, input, out)
	sel, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Action != ActionQuit {
		t.Fatalf("expected ActionQuit, got %d", sel.Action)
	}
}

func TestGoUp(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		expected string
	}{
		{"root stays root", "", ""},
		{"one level up to root", "dir/", ""},
		{"nested to parent", "a/b/c/", "a/b/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Browser{prefix: tt.prefix}
			b.goUp()
			if b.prefix != tt.expected {
				t.Errorf("goUp(%q): got %q, want %q", tt.prefix, b.prefix, tt.expected)
			}
		})
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		size     int64
		expected string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := formatSize(tt.size)
			if got != tt.expected {
				t.Errorf("formatSize(%d): got %q, want %q", tt.size, got, tt.expected)
			}
		})
	}
}
