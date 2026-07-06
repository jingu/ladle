package ssm

import (
	"context"
	"testing"
)

func TestFakeListFiltersByPath(t *testing.T) {
	ctx := context.Background()
	c := NewFake()
	c.Set("/a/x", "1", "String", "")
	c.Set("/a/sub/y", "2", "String", "")
	c.Set("/b/z", "3", "String", "")

	got, err := c.List(ctx, "/a", false)
	if err != nil {
		t.Fatal(err)
	}
	// Only /a descendants, folded to the immediate level: leaf /a/x and dir /a/sub/
	want := map[string]bool{"/a/x": true, "/a/sub/": true}
	if len(got) != len(want) {
		t.Fatalf("got %d entries %v, want %v", len(got), got, want)
	}
	for _, e := range got {
		if !want[e.Name] {
			t.Errorf("unexpected entry %q (path /a should not include /b/*)", e.Name)
		}
	}
}

func TestFakeSetPopulatesAllMetadata(t *testing.T) {
	c := NewFake()
	c.Set("/a/x", "1", "SecureString", "alias/k")
	m := c.Params["/a/x"].Metadata
	if m.Tier == "" || m.Description == "" || m.DataType == "" {
		t.Errorf("Set should populate all metadata fields so drops are detectable, got %#v", m)
	}
}
