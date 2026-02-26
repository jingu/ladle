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

func TestBucketListSelection(t *testing.T) {
	mock := storage.NewMockClient()
	mock.SetBuckets([]string{"alpha", "bravo", "charlie"})
	mock.PutObject("bravo", "file.txt", []byte("hello"), nil)

	u, err := uri.Parse("s3://")
	if err != nil {
		t.Fatal(err)
	}
	// Select bucket 2 (bravo), then select file 1
	input := strings.NewReader("2\n1\n")
	out := &bytes.Buffer{}

	b := New(mock, u, input, out)
	sel, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Action != ActionEdit {
		t.Fatalf("expected ActionEdit, got %d", sel.Action)
	}
	if sel.URI.Bucket != "bravo" {
		t.Errorf("expected bucket %q, got %q", "bravo", sel.URI.Bucket)
	}
	if sel.URI.Key != "file.txt" {
		t.Errorf("expected key %q, got %q", "file.txt", sel.URI.Key)
	}
}

func TestBucketListGoUpFromBucketRoot(t *testing.T) {
	mock := storage.NewMockClient()
	mock.SetBuckets([]string{"mybucket"})
	mock.PutObject("mybucket", "file.txt", []byte("hello"), nil)

	u, err := uri.Parse("s3://")
	if err != nil {
		t.Fatal(err)
	}
	// Select bucket 1, then go up (..) to bucket list, then quit
	input := strings.NewReader("1\n..\nq\n")
	out := &bytes.Buffer{}

	b := New(mock, u, input, out)
	sel, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Action != ActionQuit {
		t.Fatalf("expected ActionQuit, got %d", sel.Action)
	}
	// Verify the bucket list header appeared after going up
	if !strings.Contains(out.String(), "s3://\n") {
		t.Error("expected bucket list header after going up")
	}
}

func TestBucketListQuit(t *testing.T) {
	mock := storage.NewMockClient()
	mock.SetBuckets([]string{"mybucket"})

	u, err := uri.Parse("s3://")
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
			b := &Browser{bucket: tt.bucket, prefix: tt.prefix, bucketListEnabled: tt.bucketListEnabled}
			b.goUp()
			if b.bucket != tt.expectedBucket {
				t.Errorf("goUp bucket: got %q, want %q", b.bucket, tt.expectedBucket)
			}
			if b.prefix != tt.expectedPrefix {
				t.Errorf("goUp prefix: got %q, want %q", b.prefix, tt.expectedPrefix)
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
