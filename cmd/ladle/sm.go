package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/jingu/ladle/internal/diff"
	"github.com/jingu/ladle/internal/editor"
	sm "github.com/jingu/ladle/internal/secretsmanager"
	"github.com/jingu/ladle/internal/spinner"
	"github.com/jingu/ladle/internal/uri"
)

// smTarget is what an sm:// URI addresses: a whole secret, or one top-level key
// of a JSON secret when the URI carries a "#key" fragment.
type smTarget struct {
	name   string
	key    string
	hasKey bool
}

func (t smTarget) String() string {
	if t.hasKey {
		return "sm://" + t.name + "#" + t.key
	}
	return "sm://" + t.name
}

// runSM dispatches an sm:// URI. Every Secrets Manager value is a secret, so
// without --reveal no value is ever printed: operations that must show a stored
// value (edit, pipe out, reviewing a diff against it) refuse, and writes that
// need not show one — creating a secret, adding a key, --yes — go ahead with
// the diff hidden.
func runSM(ctx context.Context, u *uri.URI, f *flags) error {
	// Shell completion for sm:// is not wired up; never perform a live read in
	// response to the internal completion flags.
	if f.completeBucket || f.completePath {
		return nil
	}
	client, err := sm.New(ctx, sm.Options{Profile: f.profile, Region: f.region})
	if err != nil {
		return err
	}
	openConfirm := func() (io.ReadCloser, error) { return os.Open("/dev/tty") }
	return smDispatch(ctx, client, u, f, os.Stdin, os.Stdout, !isTerminal(os.Stdin), !isTerminal(os.Stdout), openConfirm)
}

func smDispatch(ctx context.Context, client sm.Client, u *uri.URI, f *flags, in io.Reader, out io.Writer, stdinPiped, stdoutPiped bool, openConfirm func() (io.ReadCloser, error)) error {
	if f.meta {
		return fmt.Errorf("--meta is not supported for sm://")
	}
	if f.append {
		return fmt.Errorf("--append is not supported for sm://")
	}
	t := smTarget{name: u.Bucket, key: u.Key, hasKey: u.HasKey}

	// Listings and history print to stdout whether or not it is a terminal:
	// they show names and version ids, never values.
	// TODO: open the TUI browser instead when stdout is a terminal.
	if f.versions {
		if u.IsDirectory() {
			return fmt.Errorf("--versions requires a secret name (not a prefix)")
		}
		return runSMVersions(ctx, client, t.name, out)
	}
	if u.IsDirectory() {
		return runSMList(ctx, client, t.name, f, out)
	}
	if t.hasKey && t.key == "" {
		return runSMKeys(ctx, client, t.name, out)
	}

	// A name that is not a secret but prefixes others is a namespace, mirroring
	// the S3 and ssm:// directory redirect. Skipped when stdin is piped, where
	// the intent is to create that name.
	notFound := false
	if !t.hasKey && !stdinPiped {
		if _, err := client.Describe(ctx, t.name); err != nil {
			if !sm.IsNotFound(err) {
				return err
			}
			notFound = true
			if names, lerr := client.List(ctx, t.name+"/"); lerr == nil && len(names) > 0 {
				return runSMList(ctx, client, t.name+"/", f, out)
			}
		}
	}

	if stdoutPiped && stdinPiped {
		return fmt.Errorf("both stdin and stdout are redirected; this is not supported")
	}
	if stdoutPiped {
		return runSMPipeOut(ctx, client, t, f, out)
	}
	if stdinPiped {
		return runSMPipeIn(ctx, client, t, f, in, openConfirm)
	}
	var err error
	switch {
	case t.hasKey:
		_, err = runSMKeyEdit(ctx, client, t, f)
	case notFound:
		fmt.Fprintf(os.Stderr, "%s does not exist — creating new secret.\n", t)
		_, err = createSMSecret(ctx, client, t.name, f)
	default:
		_, err = runSMEdit(ctx, client, t.name, f)
	}
	return err
}

// getSecretString fetches the current value, refusing binary secrets: ladle
// edits SecretString only.
func getSecretString(ctx context.Context, client sm.Client, name string) (*sm.Secret, error) {
	sec, err := client.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if sec.IsBinary {
		return nil, fmt.Errorf("sm://%s holds a binary secret (SecretBinary); ladle edits text secrets only", name)
	}
	return sec, nil
}

// getJSONKey fetches a secret and the raw JSON value of one of its keys.
func getJSONKey(ctx context.Context, client sm.Client, t smTarget) (sec *sm.Secret, raw string, found bool, err error) {
	sec, err = getSecretString(ctx, client, t.name)
	if err != nil {
		if sm.IsNotFound(err) {
			return nil, "", false, fmt.Errorf("secret sm://%s not found (create it with ladle sm://%s)", t.name, t.name)
		}
		return nil, "", false, err
	}
	if !sm.IsJSON(sec.Value) {
		return nil, "", false, fmt.Errorf("sm://%s is not a JSON secret; edit it whole with ladle sm://%s", t.name, t.name)
	}
	raw, found, err = sm.GetKey(sec.Value, t.key)
	return sec, raw, found, err
}

func revealError(t smTarget, action string) error {
	return fmt.Errorf("%s holds a secret value; re-run with --reveal to %s", t, action)
}

// runSMEdit edits a whole existing secret. A compact JSON secret is opened
// indented and written back compact, so a save without real changes is a no-op
// rather than a formatting-only version.
func runSMEdit(ctx context.Context, client sm.Client, name string, f *flags) (string, error) {
	t := smTarget{name: name}
	if !f.reveal {
		return "", revealError(t, "edit it")
	}
	sp := spinner.New(os.Stderr, fmt.Sprintf("Fetching %s ...", t))
	sp.Start()
	sec, err := getSecretString(ctx, client, name)
	if err != nil {
		sp.Stop()
		return "", err
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Fetched %s", t))

	isJSON := sm.IsJSON(sec.Value)
	compact := isJSON && sm.IsCompact(sec.Value)
	before := sec.Value
	if compact {
		if before, err = sm.Pretty(sec.Value); err != nil {
			return "", err
		}
	}

	sc := bufio.NewScanner(os.Stdin)
	mode := textPlain
	if isJSON {
		mode = textJSON
	}
	after, ok, err := editSecretText(sc, f, smTempName(name, isJSON), before, mode)
	if err != nil {
		return "", err
	}
	if !ok {
		return editAborted(), nil
	}

	value := after
	if compact {
		if value, err = sm.Compact(after); err != nil {
			return "", err
		}
	}
	if value == sec.Value || (isJSON && sameJSON(sec.Value, value)) {
		return noChanges(), nil
	}
	if isJSON {
		// after lost its final newline in editSecretText; match it so the diff
		// does not end on a newline-only change.
		before = strings.TrimRight(before, "\r\n")
	}
	return smConfirmAndPut(ctx, client, t, sec, before, after, value, f, sc)
}

// runSMKeyEdit edits one top-level key of a JSON secret, adding the key when it
// is absent. A string value is edited without its quotes and escapes.
func runSMKeyEdit(ctx context.Context, client sm.Client, t smTarget, f *flags) (string, error) {
	sp := spinner.New(os.Stderr, fmt.Sprintf("Fetching %s ...", t))
	sp.Start()
	sec, raw, found, err := getJSONKey(ctx, client, t)
	if err != nil {
		sp.Stop()
		return "", err
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Fetched %s", t))

	before, isString := "", true
	if found {
		if !f.reveal {
			return "", revealError(t, "edit it")
		}
		before, isString = sm.KeyEditText(raw)
	} else {
		fmt.Fprintf(os.Stderr, "Key %q does not exist in sm://%s — adding it.\n", t.key, t.name)
	}

	sc := bufio.NewScanner(os.Stdin)
	mode := textPlain
	if !isString {
		mode = textJSON
	}
	after, ok, err := editSecretText(sc, f, smTempName(t.key, !isString), before, mode)
	if err != nil {
		return "", err
	}
	if !ok {
		return editAborted(), nil
	}
	if !found && after == "" {
		msg := "Empty value — nothing added."
		fmt.Fprintln(os.Stderr, msg)
		return msg, nil
	}
	newRaw, err := sm.KeyRawFromEdit(after, isString)
	if err != nil {
		return "", err
	}
	if found && (after == before || (!isString && sameJSON(raw, newRaw))) {
		return noChanges(), nil
	}
	value, err := sm.SetKey(sec.Value, t.key, newRaw)
	if err != nil {
		return "", err
	}
	return smConfirmAndPut(ctx, client, t, sec, before, after, value, f, sc)
}

// createSMSecret creates a secret from an empty editor buffer. Input that
// starts like JSON must parse, and is stored compact, the form the AWS console
// writes.
func createSMSecret(ctx context.Context, client sm.Client, name string, f *flags) (string, error) {
	t := smTarget{name: name}
	sc := bufio.NewScanner(os.Stdin)
	after, ok, err := editSecretText(sc, f, smTempName(name, false), "", textDetectJSON)
	if err != nil {
		return "", err
	}
	if !ok {
		return editAborted(), nil
	}
	if after == "" {
		msg := "Empty value — nothing created."
		fmt.Fprintln(os.Stderr, msg)
		return msg, nil
	}
	value := after
	if sm.LooksLikeJSON(after) {
		if value, err = sm.Compact(after); err != nil {
			return "", err
		}
	}
	return smConfirmAndPut(ctx, client, t, nil, "", after, value, f, sc)
}

// textMode says whether edited text must be JSON.
type textMode int

const (
	textPlain      textMode = iota // kept as typed, minus the editor's final newline
	textJSON                       // must parse
	textDetectJSON                 // must parse if it starts like JSON (a new secret)
)

// editSecretText runs the editor on before and returns what was saved. Text
// that must be JSON (see textMode) is validated; on a parse error the user
// picks between editing again — with their text intact — and aborting
// (ok=false).
//
// The temp file holds a secret in plaintext, so it is removed on every path,
// including failures: unlike other backends there is no recovery copy.
func editSecretText(sc *bufio.Scanner, f *flags, filename, before string, mode textMode) (after string, ok bool, err error) {
	tmpPath, err := editor.TempFile(filename, []byte(before))
	if err != nil {
		return "", false, err
	}
	defer editor.Cleanup(tmpPath)

	editorCmd := editor.ResolveEditor(f.editorCmd)
	for {
		if err := editor.Open(editorCmd, tmpPath); err != nil {
			return "", false, err
		}
		data, err := os.ReadFile(tmpPath)
		if err != nil {
			return "", false, fmt.Errorf("reading edited file: %w", err)
		}
		text := string(data)
		if mode == textPlain || (mode == textDetectJSON && !sm.LooksLikeJSON(text)) {
			// Only strip a newline the editor can have added: a value that
			// already ended in one (a PEM block, say) keeps it, so an untouched
			// save stays a no-op.
			if strings.HasSuffix(before, "\n") {
				return text, true, nil
			}
			return trimEditorNewline(text), true, nil
		}
		verr := sm.Validate(text)
		if verr == nil {
			// Whitespace around a JSON document is insignificant; drop the
			// editor's final newline without a note.
			return strings.TrimRight(text, "\r\n"), true, nil
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", verr)
		// --yes promises no prompts, so there is no one to ask: fail.
		if f.yes {
			return "", false, verr
		}
		if !promptEditAgain(sc, os.Stderr) {
			return "", false, nil
		}
	}
}

// promptEditAgain asks whether to reopen the editor after invalid JSON. An
// empty answer re-edits; EOF aborts.
func promptEditAgain(sc *bufio.Scanner, out io.Writer) bool {
	for {
		_, _ = fmt.Fprint(out, "(e)dit again / (a)bort [e]: ")
		if !sc.Scan() {
			return false
		}
		switch strings.ToLower(strings.TrimSpace(sc.Text())) {
		case "", "e", "edit":
			return true
		case "a", "abort":
			return false
		}
	}
}

func editAborted() string {
	msg := "Edit aborted."
	fmt.Fprintln(os.Stderr, msg)
	return msg
}

func noChanges() string {
	msg := "No changes detected. Skipping update."
	fmt.Fprintln(os.Stderr, msg)
	return msg
}

// smTempName names the temp file after the secret or key, with a .json suffix
// for JSON content so the editor highlights it.
func smTempName(name string, isJSON bool) string {
	base := path.Base(name)
	if base == "." || base == ".." || base == "/" {
		base = "value" // not a usable file name
	}
	if isJSON && !strings.HasSuffix(base, ".json") {
		base += ".json"
	}
	return base
}

// sameJSON reports whether two JSON documents differ only in whitespace.
func sameJSON(a, b string) bool {
	ca, errA := sm.Compact(a)
	cb, errB := sm.Compact(b)
	return errA == nil && errB == nil && ca == cb
}

// smConfirmAndPut shows the diff, asks for confirmation on sc and writes value.
// cur is the version the edit was based on, nil for a new secret.
func smConfirmAndPut(ctx context.Context, client sm.Client, t smTarget, cur *sm.Secret, before, after, value string, f *flags, sc *bufio.Scanner) (string, error) {
	printSMDiff(t, before, after, f.reveal)
	warnRotation(ctx, client, t.name, cur != nil)
	if f.dryRun {
		msg := "(dry-run: update skipped)"
		fmt.Fprintln(os.Stderr, "\n"+msg)
		return msg, nil
	}
	if !f.yes {
		prompt := "Update secret?"
		if cur == nil {
			prompt = "Create secret?"
		}
		if !confirmScan(sc, os.Stderr, prompt) {
			msg := "Update cancelled."
			fmt.Fprintln(os.Stderr, msg)
			return msg, nil
		}
	}
	return smPut(ctx, client, t.name, cur, value, f.description)
}

// printSMDiff shows the pending change, or only that there is one when the
// value may not be shown (no --reveal).
func printSMDiff(t smTarget, before, after string, reveal bool) {
	fmt.Fprintf(os.Stderr, "\nSecret: %s\n\n", t)
	if !reveal {
		fmt.Fprintln(os.Stderr, "Value hidden (pass --reveal to show the diff).")
		return
	}
	diffText, tooLarge := diff.Generate(before, after, "current", "new")
	if tooLarge {
		fmt.Fprintln(os.Stderr, "Value is too large to display a diff; skipping diff.")
		return
	}
	diff.Print(os.Stderr, diffText)
}

// warnRotation flags a secret that a rotation function also writes: a manual
// value can break that function or be replaced by its next run.
func warnRotation(ctx context.Context, client sm.Client, name string, exists bool) {
	if !exists {
		return
	}
	if md, err := client.Describe(ctx, name); err == nil && md.RotationEnabled {
		fmt.Fprintf(os.Stderr, "Warning: rotation is enabled for sm://%s; a manual change may break or be overwritten by the next rotation.\n", name)
	}
}

// errSecretChanged reports that the secret gained a new version while it was
// being edited.
var errSecretChanged = errors.New("the secret changed while you were editing it (a new version is current); re-run to edit the latest value")

// smPut writes value as a new version of an existing secret (cur non-nil) or
// creates the secret (cur nil). Just before writing it checks which version is
// current: a key edit rewrites the whole document, so writing over a version
// someone else put during the edit would silently drop their change. The check
// and the write are two calls, so this narrows the race rather than closing it.
func smPut(ctx context.Context, client sm.Client, name string, cur *sm.Secret, value, description string) (string, error) {
	t := smTarget{name: name}
	if cur != nil && description != "" {
		fmt.Fprintf(os.Stderr, "Note: --description applies only when creating; %s already exists.\n", t)
	}
	progress, done := "Updating %s ...", "✓ Updated %s"
	if cur == nil {
		progress, done = "Creating %s ...", "✓ Created %s"
	}
	sp := spinner.New(os.Stderr, fmt.Sprintf(progress, t))
	sp.Start()
	var err error
	if cur == nil {
		err = client.Create(ctx, name, value, description)
	} else {
		var md *sm.Metadata
		if md, err = client.Describe(ctx, name); err == nil {
			if md.CurrentVersionID != cur.VersionID {
				err = errSecretChanged
			} else {
				err = client.Put(ctx, name, value)
			}
		}
	}
	if err != nil {
		sp.Stop()
		return "", err
	}
	msg := fmt.Sprintf(done, t)
	sp.StopWithMessage(msg)
	return msg, nil
}

func runSMPipeOut(ctx context.Context, client sm.Client, t smTarget, f *flags, out io.Writer) error {
	if !f.reveal {
		return revealError(t, "output it")
	}
	var value string
	if t.hasKey {
		_, raw, found, err := getJSONKey(ctx, client, t)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("key %q not found in sm://%s", t.key, t.name)
		}
		value, _ = sm.KeyEditText(raw)
	} else {
		sec, err := getSecretString(ctx, client, t.name)
		if err != nil {
			return err
		}
		value = sec.Value
	}
	fmt.Fprintf(os.Stderr, "✓ Fetched %s\n", t)
	_, err := io.WriteString(out, value)
	return err
}

// runSMPipeIn writes stdin as the secret (or key) value. Non-JSON input is kept
// byte for byte; JSON input must parse and follows the stored document's
// compactness, since its whitespace carries no meaning.
func runSMPipeIn(ctx context.Context, client sm.Client, t smTarget, f *flags, in io.Reader, openConfirm func() (io.ReadCloser, error)) error {
	data, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}
	input := string(data)

	var (
		cur                  *sm.Secret
		before, after, value string // after is the new value as the diff shows it
		existed              bool   // a value existed, so showing a diff would reveal it
	)
	if t.hasKey {
		var raw string
		cur, raw, existed, err = getJSONKey(ctx, client, t)
		if err != nil {
			return err
		}
		isString := true
		if existed {
			before, isString = sm.KeyEditText(raw)
		}
		newRaw, err := sm.KeyRawFromEdit(input, isString)
		if err != nil {
			return err
		}
		if existed && (input == before || (!isString && sameJSON(raw, newRaw))) {
			noChanges()
			return nil
		}
		if value, err = sm.SetKey(cur.Value, t.key, newRaw); err != nil {
			return err
		}
		after = input
	} else {
		cur, err = getSecretString(ctx, client, t.name)
		switch {
		case sm.IsNotFound(err):
			cur, value = nil, input
			if sm.LooksLikeJSON(input) {
				if value, err = compactValid(input); err != nil {
					return err
				}
			}
			after = prettyIfJSON(value)
			fmt.Fprintf(os.Stderr, "Secret %s does not exist — will create it.\n", t)
		case err != nil:
			return err
		default:
			existed = true
			wasJSON := sm.IsJSON(cur.Value)
			value = input
			// JSON input must parse whether the secret already was JSON or is
			// becoming JSON, the same rule as creating one. It is stored compact
			// unless it replaces an indented document.
			if wasJSON || sm.LooksLikeJSON(input) {
				if err := sm.Validate(input); err != nil {
					return err
				}
				if !wasJSON || sm.IsCompact(cur.Value) {
					if value, err = sm.Compact(input); err != nil {
						return err
					}
				}
			}
			if value == cur.Value || (wasJSON && sameJSON(cur.Value, value)) {
				noChanges()
				return nil
			}
			// Diff JSON indented on both sides, as the editor flow does; a
			// compact document would otherwise diff as one changed line.
			before, after = prettyIfJSON(cur.Value), prettyIfJSON(value)
		}
	}

	// Reviewing a change to an existing value means showing it; without
	// --reveal only an unreviewed --yes write can proceed.
	if existed && !f.reveal && !f.yes {
		return fmt.Errorf("%s holds a secret value; re-run with --reveal to review the diff, or --yes to update without one", t)
	}
	printSMDiff(t, before, after, f.reveal)
	warnRotation(ctx, client, t.name, cur != nil)

	if f.dryRun {
		fmt.Fprintln(os.Stderr, "\n(dry-run: update skipped)")
		return nil
	}
	if !f.yes {
		tty, err := openConfirm()
		if err != nil {
			return fmt.Errorf("cannot open terminal for confirmation (use --yes to skip): %w", err)
		}
		defer func() { _ = tty.Close() }()
		prompt := "Update secret?"
		if cur == nil {
			prompt = "Create secret?"
		}
		if !confirm(tty, os.Stderr, prompt) {
			fmt.Fprintln(os.Stderr, "Update cancelled.")
			return nil
		}
	}
	_, err = smPut(ctx, client, t.name, cur, value, f.description)
	return err
}

// prettyIfJSON returns a JSON document indented for display, anything else
// unchanged.
func prettyIfJSON(s string) string {
	if !sm.IsJSON(s) {
		return s
	}
	p, err := sm.Pretty(s)
	if err != nil {
		return s
	}
	return p
}

func compactValid(s string) (string, error) {
	if err := sm.Validate(s); err != nil {
		return "", err
	}
	return sm.Compact(s)
}

// runSMList prints the secrets under a name prefix, one URI per line. Secret
// names are flat, but "/" is the conventional separator, so unless --recursive
// is set deeper names fold into one "prefix/" entry per level, like ssm://.
func runSMList(ctx context.Context, client sm.Client, prefix string, f *flags, out io.Writer) error {
	names, err := client.List(ctx, prefix)
	if err != nil {
		return err
	}
	var lines []string
	seen := map[string]bool{}
	for _, n := range names {
		if !f.recursive {
			if i := strings.IndexByte(n[len(prefix):], '/'); i >= 0 {
				n = n[:len(prefix)+i+1]
			}
		}
		if !seen[n] {
			seen[n] = true
			lines = append(lines, "sm://"+n)
		}
	}
	return writeLines(out, lines)
}

// runSMKeys prints the top-level keys of a JSON secret as sm://name#key lines.
// Key names are not secret, so no --reveal is needed.
func runSMKeys(ctx context.Context, client sm.Client, name string, out io.Writer) error {
	sec, err := getSecretString(ctx, client, name)
	if err != nil {
		return err
	}
	if !sm.IsJSON(sec.Value) {
		return fmt.Errorf("sm://%s is not a JSON secret; it has no keys", name)
	}
	keys, err := sm.Keys(sec.Value)
	if err != nil {
		return err
	}
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, "sm://"+name+"#"+k)
	}
	return writeLines(out, lines)
}

// runSMVersions prints the secret's versions, newest first, as tab-separated
// lines: version id, RFC3339 UTC creation time, staging labels ("-" if none).
func runSMVersions(ctx context.Context, client sm.Client, name string, out io.Writer) error {
	versions, err := client.History(ctx, name)
	if err != nil {
		return err
	}
	for _, v := range versions {
		stages := "-"
		if len(v.Stages) > 0 {
			stages = strings.Join(v.Stages, ",")
		}
		if _, err := fmt.Fprintf(out, "%s\t%s\t%s\n", v.VersionID, v.CreatedDate.UTC().Format(time.RFC3339), stages); err != nil {
			return err
		}
	}
	return nil
}
