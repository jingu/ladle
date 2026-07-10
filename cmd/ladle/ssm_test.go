package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jingu/ladle/internal/ssm"
)

// withStdin replaces os.Stdin with a pipe carrying content for the duration of
// the test, so functions that read os.Stdin directly can be exercised.
func withStdin(t *testing.T, content string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })
	go func() {
		_, _ = w.WriteString(content)
		_ = w.Close()
	}()
}

func TestRunSSMPipeIn_Append(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	c.Set("/app/log", "line1\n", "String", "")
	withStdin(t, "line2\n")

	f := &flags{yes: true, append: true}
	if err := runSSMPipeIn(ctx, c, "/app/log", f); err != nil {
		t.Fatalf("runSSMPipeIn (append): %v", err)
	}
	if got := c.Params["/app/log"].Value; got != "line1\nline2\n" {
		t.Errorf("appended value = %q, want %q", got, "line1\nline2\n")
	}
}

func TestRunSSMPipeIn_AppendSecureWithoutReveal(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	c.Set("/app/secret", "s3cr3t", "SecureString", "alias/key")
	withStdin(t, "more")

	f := &flags{yes: true, append: true} // reveal is false
	err := runSSMPipeIn(ctx, c, "/app/secret", f)
	if err == nil {
		t.Fatal("expected error appending to SecureString without --reveal")
	}
	if !strings.Contains(err.Error(), "--reveal") {
		t.Errorf("error = %q, want it to mention --reveal", err.Error())
	}
}

func TestResolveForEditSecureStringGate(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	c.Set("/app/db-password", "s3cret", "SecureString", "alias/k")
	c.Set("/app/db-url", "postgres://h/db", "String", "")

	t.Run("SecureString without reveal is refused", func(t *testing.T) {
		_, _, err := resolveForEdit(ctx, c, "/app/db-password", false)
		if err == nil {
			t.Fatal("expected refusal for SecureString without --reveal")
		}
		if !strings.Contains(err.Error(), "--reveal") {
			t.Errorf("error should mention --reveal, got: %v", err)
		}
	})

	t.Run("SecureString with reveal returns plaintext and metadata", func(t *testing.T) {
		md, val, err := resolveForEdit(ctx, c, "/app/db-password", true)
		if err != nil {
			t.Fatal(err)
		}
		if val != "s3cret" {
			t.Errorf("value: got %q, want %q", val, "s3cret")
		}
		if md.KeyID != "alias/k" {
			t.Errorf("KeyID should be preserved for re-put, got %q", md.KeyID)
		}
	})

	t.Run("String does not require reveal", func(t *testing.T) {
		_, val, err := resolveForEdit(ctx, c, "/app/db-url", false)
		if err != nil {
			t.Fatal(err)
		}
		if val != "postgres://h/db" {
			t.Errorf("value: got %q", val)
		}
	})

	t.Run("missing parameter reports not found", func(t *testing.T) {
		_, _, err := resolveForEdit(ctx, c, "/app/missing", true)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected not-found error, got: %v", err)
		}
	})
}

func TestNewParamType(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "String", false},
		{"String", "String", false},
		{"StringList", "StringList", false},
		{"SecureString", "SecureString", false},
		{"securestring", "", true}, // AWS types are case-sensitive
		{"Secret", "", true},
	}
	for _, tt := range tests {
		got, err := newParamType(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("newParamType(%q): expected error", tt.in)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("newParamType(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestRunSSMNewFile_RefusesExisting(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	c.Set("/app/exists", "v", "String", "")

	f := &flags{yes: true}
	_, err := runSSMNewFile(ctx, c, "/app/exists", f, "String")
	if err == nil {
		t.Fatal("expected runSSMNewFile to refuse overwriting an existing parameter")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want it to mention 'already exists'", err.Error())
	}
}

// The type picked in the browser popup is threaded through as the ptype arg and
// wins over the launch --type default.
func TestRunSSMNewFile_UsesChosenType(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	ed := writeFakeEditor(t, "printf 'topsecret' > \"$1\"\nexit 0\n")

	f := &flags{yes: true, editorCmd: ed} // no --type on launch; default would be String
	if _, err := runSSMNewFile(ctx, c, "/app/token", f, "SecureString"); err != nil {
		t.Fatalf("runSSMNewFile: %v", err)
	}
	p := c.Params["/app/token"]
	if p == nil {
		t.Fatal("parameter was not created")
	}
	if p.Type != "SecureString" {
		t.Errorf("created type = %q, want SecureString (chosen in popup despite String default)", p.Type)
	}
	if p.Value != "topsecret" {
		t.Errorf("created value = %q, want %q", p.Value, "topsecret")
	}
}

// An empty choice falls back to the launch --type default.
func TestRunSSMNewFile_EmptyChoiceFallsBackToType(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	ed := writeFakeEditor(t, "printf 'v' > \"$1\"\nexit 0\n")

	f := &flags{yes: true, editorCmd: ed, paramType: "StringList"}
	if _, err := runSSMNewFile(ctx, c, "/app/list", f, ""); err != nil {
		t.Fatalf("runSSMNewFile: %v", err)
	}
	if p := c.Params["/app/list"]; p == nil || p.Type != "StringList" {
		t.Errorf("empty choice should fall back to --type StringList, got %+v", p)
	}
}

// describeErrClient wraps a FakeClient but forces Describe to fail with a
// non-NotFound error.
type describeErrClient struct {
	*ssm.FakeClient
	err error
}

func (c describeErrClient) Describe(context.Context, string) (*ssm.Metadata, error) {
	return nil, c.err
}

// A non-NotFound Describe failure must abort create-only creation.
func TestRunSSMNewFile_DescribeErrorNotSwallowed(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("AccessDeniedException")
	c := describeErrClient{FakeClient: ssm.NewFake(), err: boom}

	f := &flags{yes: true}
	if _, err := runSSMNewFile(ctx, c, "/app/x", f, "String"); !errors.Is(err, boom) {
		t.Fatalf("expected the Describe error to surface, got %v", err)
	}
}

// An invalid --type must fail fast when launching the browser, matching the
// pipe-in / edit flows, instead of being silently ignored.
func TestRunSSMBrowser_RejectsInvalidType(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()

	f := &flags{paramType: "Nope"}
	err := runSSMBrowser(ctx, c, mustParse(t, "ssm:///app/"), f)
	if err == nil || !strings.Contains(err.Error(), "invalid --type") {
		t.Fatalf("expected an invalid --type error, got %v", err)
	}
}

func TestTrimEditorNewline(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"s3cr3t\n", "s3cr3t"},
		{"s3cr3t\r\n", "s3cr3t"},
		{"s3cr3t\n\n", "s3cr3t"},
		{"s3cr3t", "s3cr3t"},         // no newline: unchanged
		{"a\nb\n", "a\nb"},           // keeps interior newline, trims trailing
		{"  spaced  ", "  spaced  "}, // spaces are not trimmed
		{"\n", ""},                   // newline-only -> empty
	}
	for _, tt := range tests {
		if got := trimEditorNewline(tt.in); got != tt.want {
			t.Errorf("trimEditorNewline(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A SecureString created in the editor must not keep vim's trailing newline.
func TestRunSSMNewFile_StripsTrailingNewline(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	ed := writeFakeEditor(t, "printf 'topsecret\\n' > \"$1\"\nexit 0\n") // editor appends \n

	f := &flags{yes: true, editorCmd: ed}
	if _, err := runSSMNewFile(ctx, c, "/app/token", f, "SecureString"); err != nil {
		t.Fatalf("runSSMNewFile: %v", err)
	}
	if got := c.Params["/app/token"].Value; got != "topsecret" {
		t.Errorf("stored value = %q, want %q (trailing newline must be stripped)", got, "topsecret")
	}
}

// Editing an existing parameter likewise strips the editor's trailing newline.
func TestRunSSMEdit_StripsTrailingNewline(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	c.Set("/app/db-url", "old", "String", "")
	ed := writeFakeEditor(t, "printf 'postgres://new\\n' > \"$1\"\nexit 0\n")

	f := &flags{yes: true, editorCmd: ed}
	if _, err := runSSMEdit(ctx, c, "/app/db-url", f); err != nil {
		t.Fatalf("runSSMEdit: %v", err)
	}
	if got := c.Params["/app/db-url"].Value; got != "postgres://new" {
		t.Errorf("stored value = %q, want %q (trailing newline must be stripped)", got, "postgres://new")
	}
}
