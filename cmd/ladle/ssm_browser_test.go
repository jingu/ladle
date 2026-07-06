package main

import (
	"context"
	"strings"
	"testing"

	"github.com/jingu/ladle/internal/ssm"
)

func newAdapterWith(reveal bool) (*ssmStorageAdapter, *ssm.FakeClient) {
	c := ssm.NewFake()
	c.Set("/myapp/db-url", "postgres://h/db", "String", "")
	c.Set("/myapp/db-password", "s3cret", "SecureString", "alias/k")
	c.Set("/myapp/prod/host", "example.com", "String", "")
	return &ssmStorageAdapter{client: c, reveal: reveal}, c
}

func TestAdapterMasksSecureStringByDefault(t *testing.T) {
	ctx := context.Background()

	t.Run("SecureString masked without reveal", func(t *testing.T) {
		a, _ := newAdapterWith(false)
		var buf strings.Builder
		if err := a.Download(ctx, "", "myapp/db-password", &buf); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), "s3cret") {
			t.Fatalf("secret leaked into preview: %q", buf.String())
		}
		if !strings.Contains(buf.String(), "SecureString") {
			t.Errorf("expected mask placeholder, got %q", buf.String())
		}
	})

	t.Run("SecureString revealed with --reveal", func(t *testing.T) {
		a, _ := newAdapterWith(true)
		var buf strings.Builder
		if err := a.Download(ctx, "", "myapp/db-password", &buf); err != nil {
			t.Fatal(err)
		}
		if buf.String() != "s3cret" {
			t.Errorf("got %q, want %q", buf.String(), "s3cret")
		}
	})

	t.Run("String is never masked", func(t *testing.T) {
		a, _ := newAdapterWith(false)
		var buf strings.Builder
		if err := a.Download(ctx, "", "myapp/db-url", &buf); err != nil {
			t.Fatal(err)
		}
		if buf.String() != "postgres://h/db" {
			t.Errorf("got %q", buf.String())
		}
	})
}

func TestAdapterListKeyTranslation(t *testing.T) {
	ctx := context.Background()
	a, _ := newAdapterWith(false)

	// Browser-world keys have no leading slash; a namespace folds to a dir.
	entries, err := a.List(ctx, "", "myapp/", "/")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		if strings.HasPrefix(e.Key, "/") {
			t.Errorf("browser key should not have a leading slash: %q", e.Key)
		}
		got[e.Key] = e.IsDir
	}
	if _, ok := got["myapp/db-url"]; !ok {
		t.Errorf("expected leaf myapp/db-url, got %v", got)
	}
	if isDir, ok := got["myapp/prod/"]; !ok || !isDir {
		t.Errorf("expected directory myapp/prod/, got %v", got)
	}
}

func TestAdapterCopyPreservesTypeAndValue(t *testing.T) {
	ctx := context.Background()
	a, c := newAdapterWith(true)

	if err := a.Copy(ctx, "", "myapp/db-password", "myapp/db-password-copy"); err != nil {
		t.Fatal(err)
	}
	cp, ok := c.Params["/myapp/db-password-copy"]
	if !ok {
		t.Fatal("copy did not create the destination parameter")
	}
	if cp.Value != "s3cret" || cp.Type != "SecureString" {
		t.Errorf("copy lost value/type: %#v", cp)
	}
}

func TestAdapterCopyRejectsSameNormalizedName(t *testing.T) {
	ctx := context.Background()
	a, c := newAdapterWith(true)

	// Destination differs only by a leading slash — normalizes to the same SSM
	// name. Must error so a following Move never deletes the source.
	err := a.Copy(ctx, "", "myapp/db-url", "/myapp/db-url")
	if err == nil {
		t.Fatal("expected Copy to reject a same-normalized-name destination")
	}
	if _, ok := c.Params["/myapp/db-url"]; !ok {
		t.Error("source parameter must still exist after a rejected copy")
	}
}

func TestAdapterCopySecureRequiresReveal(t *testing.T) {
	ctx := context.Background()
	a, _ := newAdapterWith(false) // no --reveal

	err := a.Copy(ctx, "", "myapp/db-password", "myapp/db-password-copy")
	if err == nil || !strings.Contains(err.Error(), "--reveal") {
		t.Fatalf("expected SecureString copy to require --reveal, got: %v", err)
	}
}
