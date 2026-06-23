package azblobclient

import (
	"testing"
	"time"

	"github.com/jingu/ladle/internal/storage"
)

func TestSortVersionsNewestFirst(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Input mimics Azure's oldest-first ordering with the current version last.
	versions := []storage.ObjectVersion{
		{VersionID: "v1", LastModified: base},
		{VersionID: "v2", LastModified: base.Add(1 * time.Hour)},
		{VersionID: "v3", IsLatest: true, LastModified: base.Add(2 * time.Hour)},
	}

	sortVersionsNewestFirst(versions)

	want := []string{"v3", "v2", "v1"}
	for i, w := range want {
		if versions[i].VersionID != w {
			t.Errorf("position %d: got %q, want %q", i, versions[i].VersionID, w)
		}
	}
	if !versions[0].IsLatest {
		t.Error("latest version should be first")
	}
}

func TestMetaRoundTrip(t *testing.T) {
	in := map[string]string{"author": "alice", "env": "prod"}
	out := fromAzureMeta(toAzureMeta(in))

	if len(out) != len(in) {
		t.Fatalf("len = %d, want %d", len(out), len(in))
	}
	for k, v := range in {
		if out[k] != v {
			t.Errorf("key %q: got %q, want %q", k, out[k], v)
		}
	}
}

func TestToAzureMetaEmpty(t *testing.T) {
	if got := toAzureMeta(nil); got != nil {
		t.Errorf("toAzureMeta(nil) = %v, want nil", got)
	}
	if got := toAzureMeta(map[string]string{}); got != nil {
		t.Errorf("toAzureMeta(empty) = %v, want nil", got)
	}
}
