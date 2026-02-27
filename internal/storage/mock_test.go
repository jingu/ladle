package storage

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
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

func TestMockClient_ListVersions(t *testing.T) {
	ctx := context.Background()
	m := NewMockClient()

	now := time.Now()
	m.PutObjectVersioned("bucket", "file.txt", "v2", []byte("version2"), nil, now, false)
	m.PutObjectVersioned("bucket", "file.txt", "v1", []byte("version1"), nil, now.Add(-time.Hour), false)

	versions, err := m.ListVersions(ctx, "bucket", "file.txt")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("got %d versions, want 2", len(versions))
	}
	if !versions[0].IsLatest {
		t.Error("first version should be latest")
	}
	if versions[0].VersionID != "v2" {
		t.Errorf("first version ID: got %q, want %q", versions[0].VersionID, "v2")
	}
	if versions[1].IsLatest {
		t.Error("second version should not be latest")
	}
}

func TestMockClient_ListVersions_NoVersioning(t *testing.T) {
	ctx := context.Background()
	m := NewMockClient()
	m.PutObject("bucket", "file.txt", []byte("hello"), nil)

	versions, err := m.ListVersions(ctx, "bucket", "file.txt")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("got %d versions, want 1", len(versions))
	}
	if versions[0].VersionID != "null" {
		t.Errorf("version ID: got %q, want %q", versions[0].VersionID, "null")
	}
	if !versions[0].IsLatest {
		t.Error("should be latest")
	}
}

func TestMockClient_DownloadVersion(t *testing.T) {
	ctx := context.Background()
	m := NewMockClient()

	now := time.Now()
	m.PutObjectVersioned("bucket", "file.txt", "v2", []byte("new content"), nil, now, false)
	m.PutObjectVersioned("bucket", "file.txt", "v1", []byte("old content"), nil, now.Add(-time.Hour), false)

	var buf bytes.Buffer
	if err := m.DownloadVersion(ctx, "bucket", "file.txt", "v1", &buf); err != nil {
		t.Fatalf("download version: %v", err)
	}
	if buf.String() != "old content" {
		t.Errorf("got %q, want %q", buf.String(), "old content")
	}
}

func TestMockClient_DownloadVersion_DeleteMarker(t *testing.T) {
	ctx := context.Background()
	m := NewMockClient()

	now := time.Now()
	m.PutObjectVersioned("bucket", "file.txt", "dm1", nil, nil, now, true)

	var buf bytes.Buffer
	err := m.DownloadVersion(ctx, "bucket", "file.txt", "dm1", &buf)
	if err == nil {
		t.Fatal("expected error for delete marker, got nil")
	}
	if !strings.Contains(err.Error(), "delete marker") {
		t.Errorf("error should mention delete marker, got: %v", err)
	}
}
