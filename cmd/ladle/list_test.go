package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jingu/ladle/internal/storage"
)

func TestRunListOut_Directory(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.PutObject("bucket", "dir/a.txt", []byte("a"), nil)
	m.PutObject("bucket", "dir/b.txt", []byte("bb"), nil)
	m.PutObject("bucket", "dir/sub/c.txt", []byte("ccc"), nil)

	var out bytes.Buffer
	if err := runListOut(ctx, m, mustParse(t, "s3://bucket/dir/"), &out); err != nil {
		t.Fatalf("runListOut: %v", err)
	}

	want := "s3://bucket/dir/a.txt\ns3://bucket/dir/b.txt\ns3://bucket/dir/sub/\n"
	if out.String() != want {
		t.Errorf("output =\n%q\nwant\n%q", out.String(), want)
	}
}

func TestRunListOut_Buckets(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.SetBuckets([]string{"zeta", "alpha"})

	var out bytes.Buffer
	if err := runListOut(ctx, m, mustParse(t, "s3://"), &out); err != nil {
		t.Fatalf("runListOut: %v", err)
	}

	want := "s3://alpha/\ns3://zeta/\n"
	if out.String() != want {
		t.Errorf("output =\n%q\nwant\n%q", out.String(), want)
	}
}

func TestRunVersionsOut(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	t1 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC)
	// Newest first, mirroring backend order.
	m.PutObjectVersioned("bucket", "file.txt", "v2", []byte("newer"), nil, t1, false)
	m.PutObjectVersioned("bucket", "file.txt", "v1", []byte("old"), nil, t2, false)

	var out bytes.Buffer
	if err := runVersionsOut(ctx, m, mustParse(t, "s3://bucket/file.txt"), &out); err != nil {
		t.Fatalf("runVersionsOut: %v", err)
	}

	want := "v2\t2026-06-01T12:00:00Z\t5\tLATEST\t-\n" +
		"v1\t2026-05-01T09:30:00Z\t3\t-\t-\n"
	if out.String() != want {
		t.Errorf("output =\n%q\nwant\n%q", out.String(), want)
	}
}

func TestRunVersionsOut_DeleteMarker(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	ts := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	m.PutObjectVersioned("bucket", "file.txt", "v1", nil, nil, ts, true)

	var out bytes.Buffer
	if err := runVersionsOut(ctx, m, mustParse(t, "s3://bucket/file.txt"), &out); err != nil {
		t.Fatalf("runVersionsOut: %v", err)
	}

	if !strings.Contains(out.String(), "DELETE_MARKER") {
		t.Errorf("expected DELETE_MARKER flag, got %q", out.String())
	}
}

func TestRunVersionsOut_NotFound(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()

	var out bytes.Buffer
	if err := runVersionsOut(ctx, m, mustParse(t, "s3://bucket/missing.txt"), &out); err == nil {
		t.Fatal("expected error for missing object, got nil")
	}
}
