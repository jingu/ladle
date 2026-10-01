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

	sm "github.com/jingu/ladle/internal/secretsmanager"
	"github.com/jingu/ladle/internal/uri"
)

// replaceEditor returns a fake editor that rewrites old to new in the file and
// copies what it was shown to seen, so a test can check the editor's input.
func replaceEditor(t *testing.T, old, new string) (ed, seen string) {
	t.Helper()
	seen = filepath.Join(t.TempDir(), "seen")
	script := "cp \"$1\" '" + seen + "'\n" +
		"sed 's/" + old + "/" + new + "/' \"$1\" > \"$1.tmp\" && cat \"$1.tmp\" > \"$1\" && rm \"$1.tmp\"\n"
	return writeFakeEditor(t, script), seen
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunSMEdit_CompactJSONOpensIndentedAndStaysCompact(t *testing.T) {
	c := sm.NewFake()
	c.Set("prod/db", `{"user":"admin","pass":"old"}`)
	ed, seen := replaceEditor(t, `"old"`, `"new"`)

	f := &flags{yes: true, reveal: true, editorCmd: ed}
	if _, err := runSMEdit(context.Background(), c, "prod/db", f); err != nil {
		t.Fatalf("runSMEdit: %v", err)
	}
	if got, want := readFile(t, seen), "{\n  \"user\": \"admin\",\n  \"pass\": \"old\"\n}\n"; got != want {
		t.Errorf("editor was shown %q, want indented %q", got, want)
	}
	if got, want := c.Current("prod/db"), `{"user":"admin","pass":"new"}`; got != want {
		t.Errorf("stored %q, want compact %q", got, want)
	}
}

// Opening a compact secret indented and saving it untouched must not write a
// formatting-only version.
func TestRunSMEdit_UntouchedIsNoOp(t *testing.T) {
	c := sm.NewFake()
	c.Set("prod/db", `{"user":"admin"}`)
	f := &flags{yes: true, reveal: true, editorCmd: writeFakeEditor(t, "exit 0\n")}
	if _, err := runSMEdit(context.Background(), c, "prod/db", f); err != nil {
		t.Fatalf("runSMEdit: %v", err)
	}
	if len(c.Puts) != 0 {
		t.Errorf("Puts = %q, want none", c.Puts)
	}
}

func TestRunSMEdit_RequiresReveal(t *testing.T) {
	c := sm.NewFake()
	c.Set("prod/db", "plain")
	f := &flags{yes: true, editorCmd: writeFakeEditor(t, "exit 0\n")}
	_, err := runSMEdit(context.Background(), c, "prod/db", f)
	if err == nil || !strings.Contains(err.Error(), "--reveal") {
		t.Fatalf("err = %v, want a --reveal refusal", err)
	}
}

// brokenThenFixedEditor writes invalid JSON on its first run and valid JSON on
// the second, recording on each run what it was shown.
func brokenThenFixedEditor(t *testing.T) (ed, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "log")
	count := filepath.Join(dir, "count")
	script := "cat \"$1\" >> '" + log + "'; echo '---' >> '" + log + "'\n" +
		"if [ -f '" + count + "' ]; then printf '{\"a\": \"fixed\"}' > \"$1\"; else touch '" + count + "'; printf '{\"a\": \"typo\",}' > \"$1\"; fi\n"
	return writeFakeEditor(t, script), log
}

// Invalid JSON is not uploaded; "e" reopens the editor on the text the user
// saved, and the fixed text goes through.
func TestRunSMEdit_InvalidJSONReedits(t *testing.T) {
	c := sm.NewFake()
	c.Set("prod/db", `{"a":"1"}`)
	ed, log := brokenThenFixedEditor(t)
	withStdin(t, "e\ny\n") // edit again, then confirm

	f := &flags{reveal: true, editorCmd: ed}
	if _, err := runSMEdit(context.Background(), c, "prod/db", f); err != nil {
		t.Fatalf("runSMEdit: %v", err)
	}
	if got := c.Current("prod/db"); got != `{"a":"fixed"}` {
		t.Errorf("stored %q, want the fixed value", got)
	}
	runs := strings.Split(readFile(t, log), "---\n")
	if len(runs) < 2 || runs[1] != `{"a": "typo",}` {
		t.Errorf("second editor run was shown %q, want the user's broken text", runs)
	}
}

func TestRunSMEdit_InvalidJSONAbort(t *testing.T) {
	c := sm.NewFake()
	c.Set("prod/db", `{"a":"1"}`)
	ed, _ := brokenThenFixedEditor(t)
	withStdin(t, "a\n")

	f := &flags{reveal: true, editorCmd: ed}
	if _, err := runSMEdit(context.Background(), c, "prod/db", f); err != nil {
		t.Fatalf("runSMEdit: %v", err)
	}
	if len(c.Puts) != 0 {
		t.Errorf("Puts = %q, want none after abort", c.Puts)
	}
}

// --yes never prompts, so invalid JSON is an error rather than a re-edit.
func TestRunSMEdit_InvalidJSONWithYesFails(t *testing.T) {
	c := sm.NewFake()
	c.Set("prod/db", `{"a":"1"}`)
	ed, _ := brokenThenFixedEditor(t)

	f := &flags{yes: true, reveal: true, editorCmd: ed}
	_, err := runSMEdit(context.Background(), c, "prod/db", f)
	var se *sm.SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *SyntaxError", err)
	}
	if len(c.Puts) != 0 {
		t.Errorf("Puts = %q, want none", c.Puts)
	}
}

func TestRunSMKeyEdit_StringValue(t *testing.T) {
	c := sm.NewFake()
	c.Set("prod/db", `{"user":"admin","pass":"old"}`)
	ed, seen := replaceEditor(t, "old", "n<+>w")

	f := &flags{yes: true, reveal: true, editorCmd: ed}
	target := smTarget{name: "prod/db", key: "pass", hasKey: true}
	if _, err := runSMKeyEdit(context.Background(), c, target, f); err != nil {
		t.Fatalf("runSMKeyEdit: %v", err)
	}
	if got := readFile(t, seen); got != "old" {
		t.Errorf("editor was shown %q, want the bare string", got)
	}
	if got, want := c.Current("prod/db"), `{"user":"admin","pass":"n<+>w"}`; got != want {
		t.Errorf("stored %q, want %q", got, want)
	}
}

// Adding a key shows no existing value, so it needs no --reveal.
func TestRunSMKeyEdit_AddKeyWithoutReveal(t *testing.T) {
	c := sm.NewFake()
	c.Set("prod/db", `{"user":"admin"}`)
	ed := writeFakeEditor(t, "printf 'db1\\n' > \"$1\"\n")

	f := &flags{yes: true, editorCmd: ed}
	target := smTarget{name: "prod/db", key: "name", hasKey: true}
	if _, err := runSMKeyEdit(context.Background(), c, target, f); err != nil {
		t.Fatalf("runSMKeyEdit: %v", err)
	}
	if got, want := c.Current("prod/db"), `{"user":"admin","name":"db1"}`; got != want {
		t.Errorf("stored %q, want %q", got, want)
	}
}

func TestRunSMKeyEdit_ExistingKeyRequiresReveal(t *testing.T) {
	c := sm.NewFake()
	c.Set("prod/db", `{"pass":"x"}`)
	f := &flags{yes: true, editorCmd: writeFakeEditor(t, "exit 0\n")}
	target := smTarget{name: "prod/db", key: "pass", hasKey: true}
	if _, err := runSMKeyEdit(context.Background(), c, target, f); err == nil || !strings.Contains(err.Error(), "--reveal") {
		t.Fatalf("err = %v, want a --reveal refusal", err)
	}
}

func TestRunSMKeyEdit_NonJSONSecret(t *testing.T) {
	c := sm.NewFake()
	c.Set("prod/token", "plain")
	f := &flags{yes: true, reveal: true, editorCmd: writeFakeEditor(t, "exit 0\n")}
	target := smTarget{name: "prod/token", key: "x", hasKey: true}
	if _, err := runSMKeyEdit(context.Background(), c, target, f); err == nil || !strings.Contains(err.Error(), "not a JSON secret") {
		t.Fatalf("err = %v, want a not-JSON refusal", err)
	}
}

// changingClient reports a different current version than the one read, as if
// someone else wrote the secret during the edit.
type changingClient struct {
	*sm.FakeClient
}

func (c *changingClient) Describe(ctx context.Context, name string) (*sm.Metadata, error) {
	md, err := c.FakeClient.Describe(ctx, name)
	if err == nil {
		md.CurrentVersionID = "someone-else"
	}
	return md, err
}

func TestRunSMKeyEdit_RefusesWhenSecretChanged(t *testing.T) {
	fake := sm.NewFake()
	fake.Set("prod/db", `{"a":"1","b":"2"}`)
	c := &changingClient{FakeClient: fake}
	ed, _ := replaceEditor(t, "1", "9")

	f := &flags{yes: true, reveal: true, editorCmd: ed}
	target := smTarget{name: "prod/db", key: "a", hasKey: true}
	if _, err := runSMKeyEdit(context.Background(), c, target, f); !errors.Is(err, errSecretChanged) {
		t.Fatalf("err = %v, want errSecretChanged", err)
	}
	if len(fake.Puts) != 0 {
		t.Errorf("Puts = %q, want none", fake.Puts)
	}
}

func TestCreateSMSecret_JSONStoredCompact(t *testing.T) {
	c := sm.NewFake()
	ed := writeFakeEditor(t, "printf '{\\n  \"user\": \"admin\"\\n}\\n' > \"$1\"\n")
	f := &flags{yes: true, editorCmd: ed, description: "db creds"}
	if _, err := createSMSecret(context.Background(), c, "prod/db", f); err != nil {
		t.Fatalf("createSMSecret: %v", err)
	}
	if got := c.Current("prod/db"); got != `{"user":"admin"}` {
		t.Errorf("stored %q, want compact JSON", got)
	}
	if got := c.Meta["prod/db"].Description; got != "db creds" {
		t.Errorf("description = %q, want %q", got, "db creds")
	}
}

func TestCreateSMSecret_PlainTextTrimsEditorNewline(t *testing.T) {
	c := sm.NewFake()
	ed := writeFakeEditor(t, "printf 'tok3n\\n' > \"$1\"\n")
	f := &flags{yes: true, editorCmd: ed}
	if _, err := createSMSecret(context.Background(), c, "prod/token", f); err != nil {
		t.Fatalf("createSMSecret: %v", err)
	}
	if got := c.Current("prod/token"); got != "tok3n" {
		t.Errorf("stored %q, want %q", got, "tok3n")
	}
}

func TestRunSMPipeIn(t *testing.T) {
	tests := []struct {
		name    string
		initial string // "" means the secret does not exist
		target  smTarget
		f       flags
		stdin   string
		want    string
		wantErr string
	}{
		{
			name: "key without reveal under --yes", initial: `{"a":"1","b":"2"}`,
			target: smTarget{name: "s", key: "b", hasKey: true}, f: flags{yes: true},
			stdin: "x", want: `{"a":"1","b":"x"}`,
		},
		{
			name: "key keeps stdin bytes", initial: `{"a":"1"}`,
			target: smTarget{name: "s", key: "a", hasKey: true}, f: flags{yes: true},
			stdin: "x\n", want: `{"a":"x\n"}`,
		},
		{
			name: "whole JSON follows compact original", initial: `{"a":"1"}`,
			target: smTarget{name: "s"}, f: flags{yes: true},
			stdin: "{\n  \"a\": \"2\"\n}\n", want: `{"a":"2"}`,
		},
		{
			name: "whole JSON must parse", initial: `{"a":"1"}`,
			target: smTarget{name: "s"}, f: flags{yes: true},
			stdin: `{"a":`, want: `{"a":"1"}`, wantErr: "invalid JSON",
		},
		{
			name: "create compacts JSON", target: smTarget{name: "s"}, f: flags{yes: true},
			stdin: "{ \"a\": 1 }\n", want: `{"a":1}`,
		},
		{
			name: "create keeps plain bytes", target: smTarget{name: "s"}, f: flags{yes: true},
			stdin: "tok\n", want: "tok\n",
		},
		{
			name: "existing value needs --reveal or --yes", initial: "old",
			target: smTarget{name: "s"}, stdin: "new", want: "old", wantErr: "--reveal",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := sm.NewFake()
			if tt.initial != "" {
				c.Set("s", tt.initial)
			}
			noTTY := func() (io.ReadCloser, error) { return nil, errors.New("no tty in tests") }
			err := runSMPipeIn(context.Background(), c, tt.target, &tt.f, strings.NewReader(tt.stdin), noTTY)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("runSMPipeIn: %v", err)
			}
			if got := c.Current("s"); got != tt.want {
				t.Errorf("stored %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunSMPipeOut(t *testing.T) {
	c := sm.NewFake()
	c.Set("prod/db", `{"pass":"p\"w","port":5432}`)
	tests := []struct {
		target smTarget
		reveal bool
		want   string
		errSub string
	}{
		{smTarget{name: "prod/db"}, true, `{"pass":"p\"w","port":5432}`, ""},
		{smTarget{name: "prod/db", key: "pass", hasKey: true}, true, `p"w`, ""},
		{smTarget{name: "prod/db", key: "port", hasKey: true}, true, `5432`, ""},
		{smTarget{name: "prod/db", key: "nope", hasKey: true}, true, "", "not found"},
		{smTarget{name: "prod/db"}, false, "", "--reveal"},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		err := runSMPipeOut(context.Background(), c, tt.target, &flags{reveal: tt.reveal}, &out)
		if tt.errSub != "" {
			if err == nil || !strings.Contains(err.Error(), tt.errSub) {
				t.Errorf("%s: err = %v, want %q", tt.target, err, tt.errSub)
			}
			continue
		}
		if err != nil || out.String() != tt.want {
			t.Errorf("%s: got %q, %v; want %q", tt.target, out.String(), err, tt.want)
		}
	}
}

func TestSMDispatchListings(t *testing.T) {
	c := sm.NewFake()
	c.Set("prod/app/db", `{"user":"u","pass":"p"}`)
	c.Set("prod/app/api", "k")
	c.Set("prod/web", "w")
	c.Set("staging/db", "s")

	tests := []struct {
		raw       string
		recursive bool
		want      string
	}{
		{"sm://", false, "sm://prod/\nsm://staging/\n"},
		{"sm://prod/", false, "sm://prod/app/\nsm://prod/web\n"},
		{"sm://prod/", true, "sm://prod/app/api\nsm://prod/app/db\nsm://prod/web\n"},
		{"sm://prod/app", false, "sm://prod/app/api\nsm://prod/app/db\n"}, // namespace redirect
		{"sm://prod/app/db#", false, "sm://prod/app/db#pass\nsm://prod/app/db#user\n"},
	}
	for _, tt := range tests {
		u, err := uri.Parse(tt.raw)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		f := &flags{recursive: tt.recursive}
		if err := smDispatch(context.Background(), c, u, f, strings.NewReader(""), &out, false, true, nil); err != nil {
			t.Fatalf("%s: %v", tt.raw, err)
		}
		if out.String() != tt.want {
			t.Errorf("%s: got %q, want %q", tt.raw, out.String(), tt.want)
		}
	}
}

func TestRunSMVersions(t *testing.T) {
	c := sm.NewFake()
	c.Set("s", "1")
	c.Set("s", "2")
	var out bytes.Buffer
	if err := runSMVersions(context.Background(), c, "s", &out); err != nil {
		t.Fatal(err)
	}
	want := "v2\t0001-01-01T00:00:00Z\tAWSCURRENT\nv1\t0001-01-01T00:00:00Z\tAWSPREVIOUS\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

// Opening a value that already ends in a newline (a PEM block) and saving it
// untouched must not strip the newline and write a new version.
func TestRunSMKeyEdit_TrailingNewlineUntouchedIsNoOp(t *testing.T) {
	c := sm.NewFake()
	c.Set("prod/tls", `{"cert":"-----END-----\n"}`)
	f := &flags{yes: true, reveal: true, editorCmd: writeFakeEditor(t, "exit 0\n")}
	target := smTarget{name: "prod/tls", key: "cert", hasKey: true}
	if _, err := runSMKeyEdit(context.Background(), c, target, f); err != nil {
		t.Fatalf("runSMKeyEdit: %v", err)
	}
	if len(c.Puts) != 0 {
		t.Errorf("Puts = %q, want none", c.Puts)
	}
}

// Without --reveal no value reaches stderr, not even a new one from stdin.
func TestRunSMPipeIn_HidesNewValueWithoutReveal(t *testing.T) {
	c := sm.NewFake()
	c.Set("s", `{"a":"1"}`)
	read := captureStderr(t)
	target := smTarget{name: "s", key: "token", hasKey: true}
	err := runSMPipeIn(context.Background(), c, target, &flags{yes: true}, strings.NewReader("hunter2"), nil)
	out := read()
	if err != nil {
		t.Fatalf("runSMPipeIn: %v", err)
	}
	if strings.Contains(out, "hunter2") {
		t.Errorf("stderr shows the value without --reveal:\n%s", out)
	}
	if got := c.Current("s"); got != `{"a":"1","token":"hunter2"}` {
		t.Errorf("stored %q", got)
	}
}

// Replacing a compact JSON secret diffs both sides indented, so the diff shows
// the changed key rather than one changed line.
func TestRunSMPipeIn_JSONDiffIsIndented(t *testing.T) {
	c := sm.NewFake()
	c.Set("s", `{"a":"1","b":"2"}`)
	read := captureStderr(t)
	err := runSMPipeIn(context.Background(), c, smTarget{name: "s"}, &flags{yes: true, reveal: true}, strings.NewReader(`{"a":"1","b":"3"}`), nil)
	out := read()
	if err != nil {
		t.Fatalf("runSMPipeIn: %v", err)
	}
	if !strings.Contains(out, `"b": "3"`) || strings.Contains(out, `+{"a"`) {
		t.Errorf("diff is not indented:\n%s", out)
	}
}

// A plain secret replaced by JSON-looking input follows the create rule: it
// must parse.
func TestRunSMPipeIn_PlainToInvalidJSON(t *testing.T) {
	c := sm.NewFake()
	c.Set("s", "plain")
	err := runSMPipeIn(context.Background(), c, smTarget{name: "s"}, &flags{yes: true}, strings.NewReader(`{"a":`), nil)
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("err = %v, want invalid JSON", err)
	}
}

func TestSMTempName(t *testing.T) {
	tests := []struct {
		name   string
		isJSON bool
		want   string
	}{
		{"prod/db", true, "db.json"},
		{"PASS", false, "PASS"},
		{"..", false, "value"},
		{".", true, "value.json"},
	}
	for _, tt := range tests {
		if got := smTempName(tt.name, tt.isJSON); got != tt.want {
			t.Errorf("smTempName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
