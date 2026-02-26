package storage

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestMockClient_DownloadUpload(t *testing.T) {
	ctx := context.Background()
	m := NewMockClient()

	m.PutObject("bucket", "file.txt", []byte("hello"), &ObjectMetadata{
		ContentType: "text/plain",
	})

	var buf bytes.Buffer
	if err := m.Download(ctx, "bucket", "file.txt", &buf); err != nil {
		t.Fatalf("download: %v", err)
	}
	if buf.String() != "hello" {
		t.Errorf("got %q, want %q", buf.String(), "hello")
	}

	// Upload new content
	if err := m.Upload(ctx, "bucket", "file.txt", strings.NewReader("world"), &ObjectMetadata{
		ContentType: "text/plain",
	}); err != nil {
		t.Fatalf("upload: %v", err)
	}

	buf.Reset()
	if err := m.Download(ctx, "bucket", "file.txt", &buf); err != nil {
		t.Fatalf("download after upload: %v", err)
	}
	if buf.String() != "world" {
		t.Errorf("after upload: got %q, want %q", buf.String(), "world")
	}
}

func TestMockClient_HeadAndUpdateMetadata(t *testing.T) {
	ctx := context.Background()
	m := NewMockClient()

	m.PutObject("bucket", "file.html", []byte("<html>"), &ObjectMetadata{
		ContentType:  "text/html",
		CacheControl: "max-age=3600",
		Metadata:     map[string]string{"version": "1"},
	})

	meta, err := m.HeadObject(ctx, "bucket", "file.html")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if meta.ContentType != "text/html" {
		t.Errorf("ContentType: got %q, want %q", meta.ContentType, "text/html")
	}
	if meta.Metadata["version"] != "1" {
		t.Errorf("version: got %q, want %q", meta.Metadata["version"], "1")
	}

	// Update metadata
	if err := m.UpdateMetadata(ctx, "bucket", "file.html", &ObjectMetadata{
		ContentType:  "text/html",
		CacheControl: "max-age=86400",
		Metadata:     map[string]string{"version": "2"},
	}); err != nil {
		t.Fatalf("update metadata: %v", err)
	}

	meta, err = m.HeadObject(ctx, "bucket", "file.html")
	if err != nil {
		t.Fatalf("head after update: %v", err)
	}
	if meta.CacheControl != "max-age=86400" {
		t.Errorf("CacheControl: got %q, want %q", meta.CacheControl, "max-age=86400")
	}
}

func TestMockClient_List(t *testing.T) {
	ctx := context.Background()
	m := NewMockClient()

	m.PutObject("bucket", "dir/a.txt", []byte("a"), nil)
	m.PutObject("bucket", "dir/b.txt", []byte("b"), nil)
	m.PutObject("bucket", "dir/sub/c.txt", []byte("c"), nil)
	m.PutObject("bucket", "other.txt", []byte("o"), nil)

	// List with delimiter
	entries, err := m.List(ctx, "bucket", "dir/", "/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var files, dirs int
	for _, e := range entries {
		if e.IsDir {
			dirs++
		} else {
			files++
		}
	}

	if files != 2 {
		t.Errorf("files: got %d, want 2", files)
	}
	if dirs != 1 {
		t.Errorf("dirs: got %d, want 1", dirs)
	}
}

func TestMockClient_ListBuckets(t *testing.T) {
	ctx := context.Background()
	m := NewMockClient()
	m.SetBuckets([]string{"alpha", "beta"})

	buckets, err := m.ListBuckets(ctx)
	if err != nil {
		t.Fatalf("list buckets: %v", err)
	}
	if len(buckets) != 2 {
		t.Errorf("got %d buckets, want 2", len(buckets))
	}
}
