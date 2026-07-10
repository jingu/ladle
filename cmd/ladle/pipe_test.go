package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jingu/ladle/internal/storage"
	"github.com/jingu/ladle/internal/uri"
)

// mustParse parses a URI or fails the test.
func mustParse(t *testing.T, raw string) *uri.URI {
	t.Helper()
	u, err := uri.Parse(raw)
	if err != nil {
		t.Fatalf("uri.Parse(%q): %v", raw, err)
	}
	return u
}

// confirmYes returns an openConfirm that feeds "y" to the prompt.
func confirmYes() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("y\n")), nil
}

// confirmNo returns an openConfirm that feeds "n" to the prompt.
func confirmNo() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("n\n")), nil
}

// confirmFail returns an openConfirm that fails to open a confirmation reader,
// simulating an unavailable /dev/tty.
func confirmFail() (io.ReadCloser, error) {
	return nil, errors.New("no tty")
}

func TestIsTerminal_Pipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()

	if isTerminal(r) {
		t.Error("pipe read end should not be detected as a terminal")
	}
	if isTerminal(w) {
		t.Error("pipe write end should not be detected as a terminal")
	}
}

func TestIsTerminal_RegularFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "ladle")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer func() { _ = f.Close() }()

	if isTerminal(f) {
		t.Error("regular file should not be detected as a terminal")
	}
}

func TestRunPipeOut(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.PutObject("bucket", "file.txt", []byte("hello world"), nil)

	var out bytes.Buffer
	if err := runPipeOut(ctx, m, mustParse(t, "s3://bucket/file.txt"), &out); err != nil {
		t.Fatalf("runPipeOut: %v", err)
	}
	if out.String() != "hello world" {
		t.Errorf("output = %q, want %q", out.String(), "hello world")
	}
}

func TestRunPipeOut_NotFound(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()

	var out bytes.Buffer
	if err := runPipeOut(ctx, m, mustParse(t, "s3://bucket/missing.txt"), &out); err == nil {
		t.Fatal("expected error for missing object, got nil")
	}
}

func TestRunPipeIn_Upload(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.PutObject("bucket", "file.txt", []byte("old content"), nil)

	in := strings.NewReader("new content")
	f := &flags{yes: true}
	if err := runPipeIn(ctx, m, mustParse(t, "s3://bucket/file.txt"), f, in, confirmFail); err != nil {
		t.Fatalf("runPipeIn: %v", err)
	}

	var got bytes.Buffer
	if err := m.Download(ctx, "bucket", "file.txt", &got); err != nil {
		t.Fatalf("download: %v", err)
	}
	if got.String() != "new content" {
		t.Errorf("uploaded content = %q, want %q", got.String(), "new content")
	}
}

func TestRunPipeIn_NewObject(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()

	in := strings.NewReader("brand new")
	f := &flags{yes: true}
	if err := runPipeIn(ctx, m, mustParse(t, "s3://bucket/new.txt"), f, in, confirmFail); err != nil {
		t.Fatalf("runPipeIn (new object): %v", err)
	}

	var got bytes.Buffer
	if err := m.Download(ctx, "bucket", "new.txt", &got); err != nil {
		t.Fatalf("download: %v", err)
	}
	if got.String() != "brand new" {
		t.Errorf("uploaded content = %q, want %q", got.String(), "brand new")
	}
}

func TestRunPipeIn_NoChanges(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.PutObject("bucket", "file.txt", []byte("same"), nil)

	in := strings.NewReader("same")
	f := &flags{}
	// confirmFail would error if a prompt were reached; identical content must skip it.
	if err := runPipeIn(ctx, m, mustParse(t, "s3://bucket/file.txt"), f, in, confirmFail); err != nil {
		t.Fatalf("runPipeIn (no changes): %v", err)
	}
}

func TestRunPipeIn_DryRun(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.PutObject("bucket", "file.txt", []byte("original"), nil)

	in := strings.NewReader("modified")
	f := &flags{dryRun: true}
	if err := runPipeIn(ctx, m, mustParse(t, "s3://bucket/file.txt"), f, in, confirmFail); err != nil {
		t.Fatalf("runPipeIn (dry-run): %v", err)
	}

	var got bytes.Buffer
	if err := m.Download(ctx, "bucket", "file.txt", &got); err != nil {
		t.Fatalf("download: %v", err)
	}
	if got.String() != "original" {
		t.Errorf("dry-run must not upload; content = %q, want %q", got.String(), "original")
	}
}

func TestRunPipeIn_ConfirmYes(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.PutObject("bucket", "file.txt", []byte("old"), nil)

	in := strings.NewReader("new")
	f := &flags{}
	if err := runPipeIn(ctx, m, mustParse(t, "s3://bucket/file.txt"), f, in, confirmYes); err != nil {
		t.Fatalf("runPipeIn: %v", err)
	}

	var got bytes.Buffer
	_ = m.Download(ctx, "bucket", "file.txt", &got)
	if got.String() != "new" {
		t.Errorf("content = %q, want %q", got.String(), "new")
	}
}

func TestRunPipeIn_ConfirmNo(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.PutObject("bucket", "file.txt", []byte("old"), nil)

	in := strings.NewReader("new")
	f := &flags{}
	if err := runPipeIn(ctx, m, mustParse(t, "s3://bucket/file.txt"), f, in, confirmNo); err != nil {
		t.Fatalf("runPipeIn: %v", err)
	}

	var got bytes.Buffer
	_ = m.Download(ctx, "bucket", "file.txt", &got)
	if got.String() != "old" {
		t.Errorf("declined upload must keep original; content = %q, want %q", got.String(), "old")
	}
}

func TestRunPipeIn_Append(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.PutObject("bucket", "log.txt", []byte("line1\n"), nil)

	in := strings.NewReader("line2\n")
	f := &flags{yes: true, append: true}
	if err := runPipeIn(ctx, m, mustParse(t, "s3://bucket/log.txt"), f, in, confirmFail); err != nil {
		t.Fatalf("runPipeIn (append): %v", err)
	}

	var got bytes.Buffer
	if err := m.Download(ctx, "bucket", "log.txt", &got); err != nil {
		t.Fatalf("download: %v", err)
	}
	if got.String() != "line1\nline2\n" {
		t.Errorf("appended content = %q, want %q", got.String(), "line1\nline2\n")
	}
}

func TestRunPipeIn_AppendToNewObject(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()

	in := strings.NewReader("first\n")
	f := &flags{yes: true, append: true}
	if err := runPipeIn(ctx, m, mustParse(t, "s3://bucket/new.txt"), f, in, confirmFail); err != nil {
		t.Fatalf("runPipeIn (append to new): %v", err)
	}

	var got bytes.Buffer
	if err := m.Download(ctx, "bucket", "new.txt", &got); err != nil {
		t.Fatalf("download: %v", err)
	}
	if got.String() != "first\n" {
		t.Errorf("append to missing object should create it; content = %q, want %q", got.String(), "first\n")
	}
}

func TestRunPipeIn_ConfirmOpenFails(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.PutObject("bucket", "file.txt", []byte("old"), nil)

	in := strings.NewReader("new")
	f := &flags{}
	err := runPipeIn(ctx, m, mustParse(t, "s3://bucket/file.txt"), f, in, confirmFail)
	if err == nil {
		t.Fatal("expected error when confirmation reader cannot be opened")
	}
	if !strings.Contains(err.Error(), "cannot open terminal") {
		t.Errorf("error = %q, want it to mention 'cannot open terminal'", err.Error())
	}
}

func TestRunPipeIn_BinaryRejected(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()

	in := bytes.NewReader([]byte{0x00, 0x01, 0x02, 0x00})
	f := &flags{yes: true}
	err := runPipeIn(ctx, m, mustParse(t, "s3://bucket/blob.bin"), f, in, confirmFail)
	if err == nil {
		t.Fatal("expected error for binary stdin without --force")
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("error = %q, want it to mention 'binary'", err.Error())
	}
}

func TestRunMetaPipeOut(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.PutObject("bucket", "file.html", []byte("<html>"), &storage.ObjectMetadata{
		ContentType:  "text/html",
		CacheControl: "max-age=3600",
		Metadata:     map[string]string{"author": "yoshitaka"},
	})

	var out bytes.Buffer
	if err := runMetaPipeOut(ctx, m, mustParse(t, "s3://bucket/file.html"), &out); err != nil {
		t.Fatalf("runMetaPipeOut: %v", err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "# s3://bucket/file.html\n") {
		t.Errorf("expected comment header, got:\n%s", got)
	}
	for _, want := range []string{"text/html", "max-age=3600", "author", "yoshitaka"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestRunMetaPipeOut_NotFound(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()

	var out bytes.Buffer
	if err := runMetaPipeOut(ctx, m, mustParse(t, "s3://bucket/missing.html"), &out); err == nil {
		t.Fatal("expected error for missing object, got nil")
	}
}

func TestRunMetaPipeIn_Update(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.PutObject("bucket", "file.html", []byte("<html>"), &storage.ObjectMetadata{
		ContentType: "text/html",
	})

	newYAML := `ContentType: application/json
CacheControl: max-age=60
ContentEncoding: ""
ContentDisposition: ""
Metadata:
  team: platform
`
	f := &flags{yes: true}
	if err := runMetaPipeIn(ctx, m, mustParse(t, "s3://bucket/file.html"), f, strings.NewReader(newYAML), confirmFail); err != nil {
		t.Fatalf("runMetaPipeIn: %v", err)
	}

	got, err := m.HeadObject(ctx, "bucket", "file.html")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if got.ContentType != "application/json" {
		t.Errorf("ContentType = %q, want %q", got.ContentType, "application/json")
	}
	if got.CacheControl != "max-age=60" {
		t.Errorf("CacheControl = %q, want %q", got.CacheControl, "max-age=60")
	}
	if got.Metadata["team"] != "platform" {
		t.Errorf("Metadata[team] = %q, want %q", got.Metadata["team"], "platform")
	}
}

func TestRunMetaPipeIn_NoChanges(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.PutObject("bucket", "file.html", []byte("<html>"), &storage.ObjectMetadata{
		ContentType: "text/html",
	})

	// Re-feed the exact current metadata YAML — must detect no changes and skip the prompt.
	var current bytes.Buffer
	if err := runMetaPipeOut(ctx, m, mustParse(t, "s3://bucket/file.html"), &current); err != nil {
		t.Fatalf("runMetaPipeOut: %v", err)
	}

	f := &flags{}
	if err := runMetaPipeIn(ctx, m, mustParse(t, "s3://bucket/file.html"), f, strings.NewReader(current.String()), confirmFail); err != nil {
		t.Fatalf("runMetaPipeIn (no changes): %v", err)
	}
}

func TestRunMetaPipeIn_InvalidYAML(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.PutObject("bucket", "file.html", []byte("<html>"), &storage.ObjectMetadata{ContentType: "text/html"})

	f := &flags{yes: true}
	err := runMetaPipeIn(ctx, m, mustParse(t, "s3://bucket/file.html"), f, strings.NewReader("\tnot: valid: yaml:"), confirmFail)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestRunMetaPipeIn_ConfirmNo(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.PutObject("bucket", "file.html", []byte("<html>"), &storage.ObjectMetadata{ContentType: "text/html"})

	newYAML := "ContentType: application/json\nCacheControl: \"\"\nContentEncoding: \"\"\nContentDisposition: \"\"\nMetadata: {}\n"
	f := &flags{}
	if err := runMetaPipeIn(ctx, m, mustParse(t, "s3://bucket/file.html"), f, strings.NewReader(newYAML), confirmNo); err != nil {
		t.Fatalf("runMetaPipeIn: %v", err)
	}

	got, _ := m.HeadObject(ctx, "bucket", "file.html")
	if got.ContentType != "text/html" {
		t.Errorf("declined update must keep original; ContentType = %q, want %q", got.ContentType, "text/html")
	}
}

func TestRunNewFile_RefusesExisting(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	m.PutObject("bucket", "exists.txt", []byte("data"), nil)

	f := &flags{yes: true}
	_, err := runNewFile(ctx, m, mustParse(t, "s3://bucket/exists.txt"), f)
	if err == nil {
		t.Fatal("expected runNewFile to refuse overwriting an existing object")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want it to mention 'already exists'", err.Error())
	}
}

// writeFakeEditor writes an executable shell script that stands in for $EDITOR.
func writeFakeEditor(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake-editor.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatalf("writing fake editor: %v", err)
	}
	return p
}

// Simulates vim `:q!` on the empty new-file buffer: the editor exits 0 without
// ever writing the temp file. Nothing must be created.
func TestRunNewFile_QuitWithoutSaving(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	ed := writeFakeEditor(t, "exit 0\n") // never touches "$1"

	f := &flags{yes: true, editorCmd: ed}
	msg, err := runNewFile(ctx, m, mustParse(t, "s3://bucket/ghost.txt"), f)
	if err != nil {
		t.Fatalf("runNewFile: %v", err)
	}
	if !strings.Contains(msg, "nothing created") {
		t.Errorf("message = %q, want it to report nothing created", msg)
	}
	if _, err := m.HeadObject(ctx, "bucket", "ghost.txt"); err == nil {
		t.Error("no object should have been created after quit-without-saving")
	}
}

// Simulates saving content in the editor: the temp file ends up non-empty and
// the object is created.
func TestRunNewFile_SavesContent(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	ed := writeFakeEditor(t, "printf 'hello world' > \"$1\"\nexit 0\n")

	f := &flags{yes: true, editorCmd: ed}
	if _, err := runNewFile(ctx, m, mustParse(t, "s3://bucket/created.txt"), f); err != nil {
		t.Fatalf("runNewFile: %v", err)
	}
	var got bytes.Buffer
	if err := m.Download(ctx, "bucket", "created.txt", &got); err != nil {
		t.Fatalf("download: %v", err)
	}
	if got.String() != "hello world" {
		t.Errorf("created content = %q, want %q", got.String(), "hello world")
	}
}

// Simulates vim `:cq` / an editor crash: non-zero exit surfaces an error and
// creates nothing.
func TestRunNewFile_EditorFails(t *testing.T) {
	ctx := context.Background()
	m := storage.NewMockClient()
	ed := writeFakeEditor(t, "exit 1\n")

	f := &flags{yes: true, editorCmd: ed}
	_, err := runNewFile(ctx, m, mustParse(t, "s3://bucket/nope.txt"), f)
	if err == nil {
		t.Fatal("expected an error when the editor exits non-zero")
	}
	if _, err := m.HeadObject(ctx, "bucket", "nope.txt"); err == nil {
		t.Error("no object should have been created after an editor failure")
	}
}
