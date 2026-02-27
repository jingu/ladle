package browser

import (
	"context"
	"strings"
	"testing"

	"github.com/jingu/ladle/internal/storage"
	"github.com/jingu/ladle/internal/uri"
)

func TestComputeUp(t *testing.T) {
	tests := []struct {
		name              string
		bucket            string
		prefix            string
		bucketListEnabled bool
		expectedBucket    string
		expectedPrefix    string
	}{
		{"root stays root", "b", "", false, "b", ""},
		{"one level up to root", "b", "dir/", false, "b", ""},
		{"nested to parent", "b", "a/b/c/", false, "b", "a/b/"},
		{"root to bucket list", "b", "", true, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Browser{bucketListEnabled: tt.bucketListEnabled}
			gotBucket, gotPrefix := b.computeUp(tt.bucket, tt.prefix)
			if gotBucket != tt.expectedBucket {
				t.Errorf("computeUp bucket: got %q, want %q", gotBucket, tt.expectedBucket)
			}
			if gotPrefix != tt.expectedPrefix {
				t.Errorf("computeUp prefix: got %q, want %q", gotPrefix, tt.expectedPrefix)
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

func TestNonExistentDirectoryError(t *testing.T) {
	mock := storage.NewMockClient()
	// Bucket exists but no objects under "nonexistent/"
	mock.PutObject("mybucket", "file.txt", []byte("hello"), nil)

	u, err := uri.Parse("s3://mybucket/nonexistent/")
	if err != nil {
		t.Fatal(err)
	}

	b := New(mock, u, strings.NewReader(""), nil, "test")
	err = b.Run(context.Background(), func(u *uri.URI) (string, error) { return "", nil })
	if err == nil {
		t.Fatal("expected error for non-existent directory")
	}
	if !strings.Contains(err.Error(), "directory not found") {
		t.Errorf("expected 'directory not found' error, got: %v", err)
	}
}

func TestIconForEntry(t *testing.T) {
	tests := []struct {
		name     string
		isDir    bool
		isBucket bool
		expected string
	}{
		{"dir/", true, false, iconDir},
		{"mybucket", false, true, iconBucket},
		{"file.txt", false, false, iconText},
		{"file.md", false, false, iconText},
		{"photo.png", false, false, iconImage},
		{"photo.jpg", false, false, iconImage},
		{"archive.zip", false, false, iconArchive},
		{"app.exe", false, false, iconArchive},
		{"data.json", false, false, iconText},
		{"index.html", false, false, iconText},
		{"style.css", false, false, iconText},
		{"app.js", false, false, iconText},
		{"app.ts", false, false, iconText},
		{"component.jsx", false, false, iconText},
		{"component.tsx", false, false, iconText},
		{"config.yaml", false, false, iconText},
		{"unknown", false, false, iconFile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := iconForEntry(tt.name, tt.isDir, tt.isBucket)
			if got != tt.expected {
				t.Errorf("iconForEntry(%q): got %q, want %q", tt.name, got, tt.expected)
			}
		})
	}
}
