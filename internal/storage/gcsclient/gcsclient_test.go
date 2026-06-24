package gcsclient

import (
	"testing"
	"time"

	"github.com/jingu/ladle/internal/storage"
)

func TestSortVersionsNewestFirst(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Input mimics GCS's oldest-first ordering with the live generation last.
	versions := []storage.ObjectVersion{
		{VersionID: "1", LastModified: base},
		{VersionID: "2", LastModified: base.Add(1 * time.Hour)},
		{VersionID: "3", IsLatest: true, LastModified: base.Add(2 * time.Hour)},
	}

	sortVersionsNewestFirst(versions)

	want := []string{"3", "2", "1"}
	for i, w := range want {
		if versions[i].VersionID != w {
			t.Errorf("position %d: got %q, want %q", i, versions[i].VersionID, w)
		}
	}
	if !versions[0].IsLatest {
		t.Error("latest version should be first")
	}
}

func TestParseGeneration(t *testing.T) {
	got, err := parseGeneration("1700000000000001")
	if err != nil {
		t.Fatalf("parseGeneration error: %v", err)
	}
	if got != 1700000000000001 {
		t.Errorf("got %d, want 1700000000000001", got)
	}

	if _, err := parseGeneration("not-a-number"); err == nil {
		t.Error("expected error for non-numeric version ID")
	}
}

func TestCopyMeta(t *testing.T) {
	if got := copyMeta(nil); got != nil {
		t.Errorf("copyMeta(nil) = %v, want nil", got)
	}
	if got := copyMeta(map[string]string{}); got != nil {
		t.Errorf("copyMeta(empty) = %v, want nil", got)
	}

	in := map[string]string{"author": "alice", "env": "prod"}
	out := copyMeta(in)
	if len(out) != len(in) {
		t.Fatalf("len = %d, want %d", len(out), len(in))
	}
	for k, v := range in {
		if out[k] != v {
			t.Errorf("key %q: got %q, want %q", k, out[k], v)
		}
	}
	// Verify it's a copy, not the same map.
	out["author"] = "bob"
	if in["author"] != "alice" {
		t.Error("copyMeta did not return an independent copy")
	}
}
