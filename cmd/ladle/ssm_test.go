package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	t.Cleanup(func() {
		os.Stdin = orig
		_ = r.Close()
	})
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

// An invalid explicit type (e.g. a future/changed choice list) is rejected in
// runSSMNewFile itself, not accepted and failing later in Put.
func TestRunSSMNewFile_RejectsInvalidChoice(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()

	f := &flags{yes: true}
	if _, err := runSSMNewFile(ctx, c, "/app/x", f, "Bogus"); err == nil || !strings.Contains(err.Error(), "invalid --type") {
		t.Fatalf("expected an invalid type error, got %v", err)
	}
	if len(c.Puts) != 0 {
		t.Errorf("nothing should have been created, got %d puts", len(c.Puts))
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

func TestPromptParamType(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "first choice", input: "1\n", want: "String"},
		{name: "second choice", input: "2\n", want: "StringList"},
		{name: "third choice", input: "3\n", want: "SecureString"},
		{name: "empty takes the default", input: "\n", want: "String"},
		{name: "surrounding space", input: "  3  \n", want: "SecureString"},
		{name: "out of range", input: "4\n", wantErr: true},
		{name: "not a number", input: "SecureString\n", wantErr: true},
		{name: "no input", input: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := bufio.NewScanner(strings.NewReader(tt.input))
			got, err := promptParamType(sc, io.Discard)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("promptParamType: %v", err)
			}
			if got != tt.want {
				t.Errorf("type = %q, want %q", got, tt.want)
			}
		})
	}
}

// With neither a popup choice nor --type, the type is asked for after the editor
// closes, and the confirmation that follows still sees its own answer.
func TestRunSSMNewFile_PromptsForTypeWhenUnset(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	ed := writeFakeEditor(t, "printf 'topsecret' > \"$1\"\nexit 0\n")
	withStdin(t, "3\n\ny\n") // 3) SecureString, no description, then confirm

	f := &flags{editorCmd: ed}
	if _, err := runSSMNewFile(ctx, c, "/app/token", f, ""); err != nil {
		t.Fatalf("runSSMNewFile: %v", err)
	}
	p := c.Params["/app/token"]
	if p == nil {
		t.Fatal("parameter was not created")
	}
	if p.Type != "SecureString" {
		t.Errorf("created type = %q, want SecureString (picked at the prompt)", p.Type)
	}
	if p.Value != "topsecret" {
		t.Errorf("created value = %q, want %q", p.Value, "topsecret")
	}
}

// --yes means "ask nothing", so an unset type takes the String default instead
// of blocking on a prompt.
func TestRunSSMNewFile_YesSkipsTypePrompt(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	ed := writeFakeEditor(t, "printf 'v' > \"$1\"\nexit 0\n")

	f := &flags{yes: true, editorCmd: ed}
	if _, err := runSSMNewFile(ctx, c, "/app/plain", f, ""); err != nil {
		t.Fatalf("runSSMNewFile: %v", err)
	}
	if p := c.Params["/app/plain"]; p == nil || p.Type != "String" {
		t.Errorf("--yes with no --type should create a String, got %+v", p)
	}
}

// An invalid --type must be rejected before the editor opens, so the user does
// not type a value only to have the write fail afterwards.
func TestRunSSMNewFile_RejectsInvalidFlagTypeBeforeEditor(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	ed := writeFakeEditor(t, "printf 'v' > \"$1\"\nexit 1\n") // would fail if reached

	f := &flags{yes: true, editorCmd: ed, paramType: "Bogus"}
	_, err := runSSMNewFile(ctx, c, "/app/x", f, "")
	if err == nil || !strings.Contains(err.Error(), "invalid --type") {
		t.Fatalf("expected an invalid type error, got %v", err)
	}
	if len(c.Puts) != 0 {
		t.Errorf("nothing should have been created, got %d puts", len(c.Puts))
	}
}

// With --description unset the type prompt is followed by a description prompt,
// and the answer lands on the created parameter.
func TestRunSSMNewFile_PromptsForDescription(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	ed := writeFakeEditor(t, "printf 'k' > \"$1\"\nexit 0\n")
	withStdin(t, "3\nFastly API key\ny\n") // type, description, confirm

	f := &flags{editorCmd: ed}
	if _, err := runSSMNewFile(ctx, c, "/app/token", f, ""); err != nil {
		t.Fatalf("runSSMNewFile: %v", err)
	}
	p := c.Params["/app/token"]
	if p == nil {
		t.Fatal("parameter was not created")
	}
	if p.Type != "SecureString" {
		t.Errorf("created type = %q, want SecureString", p.Type)
	}
	if p.Metadata.Description != "Fastly API key" {
		t.Errorf("description = %q, want %q", p.Metadata.Description, "Fastly API key")
	}
}

// An empty answer at the description prompt means "no description", not an error.
func TestRunSSMNewFile_EmptyDescriptionSkipped(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	ed := writeFakeEditor(t, "printf 'k' > \"$1\"\nexit 0\n")
	withStdin(t, "1\n\ny\n") // String, no description, confirm

	f := &flags{editorCmd: ed}
	if _, err := runSSMNewFile(ctx, c, "/app/plain", f, ""); err != nil {
		t.Fatalf("runSSMNewFile: %v", err)
	}
	if p := c.Params["/app/plain"]; p == nil || p.Metadata.Description != "" {
		t.Errorf("expected no description, got %+v", p)
	}
}

// --description supplies the value, so the prompt is skipped entirely.
func TestRunSSMNewFile_DescriptionFlagSkipsPrompt(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	ed := writeFakeEditor(t, "printf 'k' > \"$1\"\nexit 0\n")
	withStdin(t, "y\n") // only the confirmation is read

	f := &flags{editorCmd: ed, paramType: "String", description: "from the flag"}
	if _, err := runSSMNewFile(ctx, c, "/app/flagged", f, ""); err != nil {
		t.Fatalf("runSSMNewFile: %v", err)
	}
	if p := c.Params["/app/flagged"]; p == nil || p.Metadata.Description != "from the flag" {
		t.Errorf("description = %+v, want %q", p, "from the flag")
	}
}

// --yes asks nothing, so an unset description stays empty.
func TestRunSSMNewFile_YesSkipsDescriptionPrompt(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	ed := writeFakeEditor(t, "printf 'k' > \"$1\"\nexit 0\n")

	f := &flags{yes: true, editorCmd: ed}
	if _, err := runSSMNewFile(ctx, c, "/app/quiet", f, ""); err != nil {
		t.Fatalf("runSSMNewFile: %v", err)
	}
	if p := c.Params["/app/quiet"]; p == nil || p.Metadata.Description != "" {
		t.Errorf("expected no description under --yes, got %+v", p)
	}
}

// Unlike --type, --description applies to an existing parameter: a value edit
// with the flag rewrites the description.
func TestRunSSMEdit_DescriptionFlagOverwrites(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	c.Set("/app/db-url", "postgres://old/db", "String", "")
	c.Params["/app/db-url"].Metadata.Description = "stale"
	ed := writeFakeEditor(t, "printf 'postgres://new/db' > \"$1\"\nexit 0\n")

	f := &flags{yes: true, editorCmd: ed, description: "primary database URL"}
	if _, err := runSSMEdit(ctx, c, "/app/db-url", f); err != nil {
		t.Fatalf("runSSMEdit: %v", err)
	}
	if got := c.Params["/app/db-url"].Metadata.Description; got != "primary database URL" {
		t.Errorf("description = %q, want %q", got, "primary database URL")
	}
}

// Without the flag a value edit must keep the existing description rather than
// dropping it: SSM has no metadata-only write, so every edit re-Puts.
func TestRunSSMEdit_PreservesDescriptionWithoutFlag(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	c.Set("/app/db-url", "postgres://old/db", "String", "")
	c.Params["/app/db-url"].Metadata.Description = "keep me"
	ed := writeFakeEditor(t, "printf 'postgres://new/db' > \"$1\"\nexit 0\n")

	f := &flags{yes: true, editorCmd: ed}
	if _, err := runSSMEdit(ctx, c, "/app/db-url", f); err != nil {
		t.Fatalf("runSSMEdit: %v", err)
	}
	if got := c.Params["/app/db-url"].Metadata.Description; got != "keep me" {
		t.Errorf("description = %q, want it preserved as %q", got, "keep me")
	}
}

// A create must not be reported as an update, matching the S3 side's
// "✓ Created". The message is also what the browser shows after `n`.
func TestRunSSMNewFile_ReportsCreated(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	ed := writeFakeEditor(t, "printf 'v' > \"$1\"\nexit 0\n")

	f := &flags{yes: true, editorCmd: ed}
	msg, err := runSSMNewFile(ctx, c, "/app/fresh", f, "String")
	if err != nil {
		t.Fatalf("runSSMNewFile: %v", err)
	}
	if !strings.Contains(msg, "Created") {
		t.Errorf("message = %q, want it to report a creation", msg)
	}
}

// Editing an existing parameter still reports an update.
func TestRunSSMEdit_ReportsUpdated(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	c.Set("/app/db-url", "old", "String", "")
	ed := writeFakeEditor(t, "printf 'new' > \"$1\"\nexit 0\n")

	f := &flags{yes: true, editorCmd: ed}
	msg, err := runSSMEdit(ctx, c, "/app/db-url", f)
	if err != nil {
		t.Fatalf("runSSMEdit: %v", err)
	}
	if !strings.Contains(msg, "Updated") {
		t.Errorf("message = %q, want it to report an update", msg)
	}
}

// captureStderr redirects os.Stderr to a pipe for the duration of the test and
// returns a reader for what was written. Needed where the code under test
// writes to os.Stderr directly (spinners, prompts, recovery hints).
func captureStderr(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()

	var out string
	var once sync.Once
	read := func() string {
		once.Do(func() {
			os.Stderr = orig
			_ = w.Close()
			out = <-done
			_ = r.Close()
		})
		return out
	}
	t.Cleanup(func() { read() })
	return read
}

// A --description change with an untouched value must still be written: the
// no-op guard looks at the value alone, which used to drop the change silently.
func TestRunSSMEdit_DescriptionOnlyChangeIsWritten(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	c.Set("/app/db-url", "postgres://same", "String", "")
	c.Params["/app/db-url"].Metadata.Description = "stale"
	ed := writeFakeEditor(t, "exit 0\n") // leaves the value exactly as fetched

	f := &flags{yes: true, editorCmd: ed, description: "primary database URL"}
	if _, err := runSSMEdit(ctx, c, "/app/db-url", f); err != nil {
		t.Fatalf("runSSMEdit: %v", err)
	}
	if len(c.Puts) != 1 {
		t.Fatalf("expected 1 write, got %d", len(c.Puts))
	}
	p := c.Params["/app/db-url"]
	if p.Metadata.Description != "primary database URL" {
		t.Errorf("description = %q, want it updated", p.Metadata.Description)
	}
	if p.Value != "postgres://same" {
		t.Errorf("value = %q, want it untouched", p.Value)
	}
}

// The same via pipe-in: identical stdin plus a new description is a change.
func TestRunSSMPipeIn_DescriptionOnlyChangeIsWritten(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	c.Set("/app/db-url", "postgres://same", "String", "")
	c.Params["/app/db-url"].Metadata.Description = "stale"
	withStdin(t, "postgres://same")

	f := &flags{yes: true, description: "primary database URL"}
	if err := runSSMPipeIn(ctx, c, "/app/db-url", f); err != nil {
		t.Fatalf("runSSMPipeIn: %v", err)
	}
	if len(c.Puts) != 1 {
		t.Fatalf("expected 1 write, got %d", len(c.Puts))
	}
	if got := c.Params["/app/db-url"].Metadata.Description; got != "primary database URL" {
		t.Errorf("description = %q, want it updated", got)
	}
}

// With neither the value nor the description changed it is still a no-op.
func TestRunSSMEdit_NoChangeStillSkips(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	c.Set("/app/db-url", "postgres://same", "String", "")
	ed := writeFakeEditor(t, "exit 0\n")

	f := &flags{yes: true, editorCmd: ed}
	msg, err := runSSMEdit(ctx, c, "/app/db-url", f)
	if err != nil {
		t.Fatalf("runSSMEdit: %v", err)
	}
	if !strings.Contains(msg, "No changes detected") {
		t.Errorf("message = %q, want a no-op", msg)
	}
	if len(c.Puts) != 0 {
		t.Errorf("expected no write, got %d", len(c.Puts))
	}
}

// The pending description change must be visible before the confirmation, since
// the diff shows the value alone.
func TestRunSSMEdit_ShowsDescriptionChange(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	c.Set("/app/db-url", "old", "String", "")
	c.Params["/app/db-url"].Metadata.Description = "stale"
	ed := writeFakeEditor(t, "printf 'new' > \"$1\"\nexit 0\n")
	stderr := captureStderr(t)

	f := &flags{yes: true, editorCmd: ed, description: "fresh"}
	if _, err := runSSMEdit(ctx, c, "/app/db-url", f); err != nil {
		t.Fatalf("runSSMEdit: %v", err)
	}
	if out := stderr(); !strings.Contains(out, `Description: "stale" -> "fresh"`) {
		t.Errorf("stderr did not report the description change:\n%s", out)
	}
}

// A value the user has already typed must survive a prompt that cannot be
// answered: the temp file stays put and its path is reported, the way an editor
// failure is handled.
func TestCreateSSMParam_KeepsValueWhenTypePromptFails(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	ed := writeFakeEditor(t, "printf 'topsecret' > \"$1\"\nexit 0\n")
	withStdin(t, "") // EOF at the type prompt
	stderr := captureStderr(t)

	f := &flags{editorCmd: ed} // no --type, no --yes: the prompt runs
	_, err := createSSMParam(ctx, c, "/app/token", f, "")
	if err == nil {
		t.Fatal("expected an error when the type prompt cannot be answered")
	}
	out := stderr()
	if !strings.Contains(out, "Recovery:") {
		t.Fatalf("no recovery hint in stderr:\n%s", out)
	}
	var saved string
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "Recovery: your value is saved at "); i >= 0 {
			saved = strings.TrimSpace(line[i+len("Recovery: your value is saved at "):])
		}
	}
	if saved == "" {
		t.Fatalf("could not find the saved path in stderr:\n%s", out)
	}
	body, readErr := os.ReadFile(saved)
	if readErr != nil {
		t.Fatalf("temp file was removed despite the recovery hint: %v", readErr)
	}
	if string(body) != "topsecret" {
		t.Errorf("recovered value = %q, want %q", body, "topsecret")
	}
	_ = os.RemoveAll(filepath.Dir(saved))
}

// A mistyped menu answer re-asks instead of throwing the typed value away.
func TestPromptParamType_RetriesAfterInvalidAnswer(t *testing.T) {
	sc := bufio.NewScanner(strings.NewReader("9\nnope\n3\n"))
	got, err := promptParamType(sc, io.Discard)
	if err != nil {
		t.Fatalf("promptParamType: %v", err)
	}
	if got != "SecureString" {
		t.Errorf("type = %q, want SecureString", got)
	}
}

// The browser acts on whatever the cursor is on, so it must not inherit a
// --description meant for a parameter named on the command line.
func TestBrowserFlags_DropsDescription(t *testing.T) {
	f := &flags{description: "for one parameter", reveal: true, paramType: "String"}
	bf := browserFlags(f)

	if bf.description != "" {
		t.Errorf("browser flags kept description %q", bf.description)
	}
	if f.description != "for one parameter" {
		t.Errorf("the caller's flags were mutated: %q", f.description)
	}
	if !bf.reveal || bf.paramType != "String" {
		t.Errorf("unrelated flags were lost: %+v", bf)
	}
	if same := browserFlags(&flags{reveal: true}); same.description != "" || !same.reveal {
		t.Errorf("nothing to strip should pass the flags through, got %+v", same)
	}
}
