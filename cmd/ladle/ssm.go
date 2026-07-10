package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/jingu/ladle/internal/browser"
	"github.com/jingu/ladle/internal/diff"
	"github.com/jingu/ladle/internal/editor"
	"github.com/jingu/ladle/internal/spinner"
	"github.com/jingu/ladle/internal/ssm"
	"github.com/jingu/ladle/internal/uri"
)

// ssmDisplay renders the canonical URI for a parameter name (which always
// begins with "/"), e.g. "/myapp/db" -> "ssm:///myapp/db".
func ssmDisplay(name string) string {
	return "ssm://" + name
}

// ssmDirURI builds a directory URI for the browser from a leading-slash path.
func ssmDirURI(dirPath string) *uri.URI {
	return &uri.URI{Scheme: uri.SchemeSSM, Key: dirPath, Raw: ssmDisplay(dirPath)}
}

// runSSM dispatches an ssm:// URI. SecureString values are never exposed unless
// --reveal is given; without it, value-exposing operations refuse rather than
// print masked or ciphertext data.
func runSSM(ctx context.Context, u *uri.URI, f *flags) error {
	// Shell completion for ssm:// is not wired up; never perform a live read in
	// response to the internal completion flags.
	if f.completeBucket || f.completePath {
		return nil
	}

	client, err := ssm.New(ctx, ssm.Options{Profile: f.profile, Region: f.region})
	if err != nil {
		return err
	}

	name := u.Key // normalized, always leading "/"

	stdoutPiped := !isTerminal(os.Stdout)
	stdinPiped := !isTerminal(os.Stdin)

	// --versions and listings are read-only and never consume stdin, so they
	// run regardless of stdin's state (the both-redirect guard below only
	// applies to single-parameter read/write).

	// --versions: history. Piped -> stdout table; interactive -> browser view.
	if f.versions {
		if u.IsDirectory() {
			return fmt.Errorf("--versions requires a parameter name (not a path)")
		}
		if stdoutPiped {
			return runSSMVersions(ctx, client, name)
		}
		parent := path.Dir(name)
		if parent != "/" {
			parent += "/"
		}
		dirURI := ssmDirURI(parent)
		return runSSMBrowser(ctx, client, dirURI, f, browser.WithVersionsKey(strings.TrimPrefix(name, "/")))
	}

	// Explicit path => listing. Piped -> stdout; interactive -> TUI browser.
	if u.IsDirectory() {
		if stdoutPiped {
			return runSSMList(ctx, client, name, f)
		}
		return runSSMBrowser(ctx, client, u, f)
	}

	// A name that is NOT itself a parameter but has children is a namespace:
	// list/browse it, mirroring the S3 prefix redirect. A real parameter (even
	// one that also has children) is edited/read directly. Skipped when stdin
	// is piped, where the intent is to create that name.
	if !stdinPiped {
		if _, derr := client.Describe(ctx, name); derr != nil {
			if !ssm.IsNotFound(derr) {
				return derr
			}
			if entries, lerr := client.List(ctx, name+"/", false); lerr == nil && len(entries) > 0 {
				if stdoutPiped {
					return runSSMList(ctx, client, name+"/", f)
				}
				return runSSMBrowser(ctx, client, ssmDirURI(name+"/"), f)
			}
		}
	}

	// From here we read or write a single parameter's value/metadata;
	// redirecting both stdin and stdout makes the intent ambiguous.
	if stdoutPiped && stdinPiped {
		return fmt.Errorf("both stdin and stdout are redirected; this is not supported")
	}

	if stdoutPiped {
		if f.meta {
			return runSSMMetaPipeOut(ctx, client, name)
		}
		return runSSMPipeOut(ctx, client, name, f)
	}
	if stdinPiped {
		if f.meta {
			return runSSMMetaPipeIn(ctx, client, name, f)
		}
		return runSSMPipeIn(ctx, client, name, f)
	}

	if f.meta {
		_, err = runSSMMetaEdit(ctx, client, name, f)
		return err
	}
	_, err = runSSMEdit(ctx, client, name, f)
	return err
}

// resolveForEdit fetches a parameter's metadata and, gated by --reveal for
// SecureString, its current value. It returns a clear error when a SecureString
// would be exposed without --reveal.
func resolveForEdit(ctx context.Context, client ssm.Client, name string, reveal bool) (*ssm.Metadata, string, error) {
	md, err := client.Describe(ctx, name)
	if err != nil {
		if ssm.IsNotFound(err) {
			return nil, "", fmt.Errorf("parameter %s not found (pipe a value in to create it)", ssmDisplay(name))
		}
		return nil, "", err
	}
	secure := md.IsSecure()
	if secure && !reveal {
		return nil, "", fmt.Errorf("%s is a SecureString; re-run with --reveal to decrypt and edit", ssmDisplay(name))
	}
	param, err := client.Get(ctx, name, secure)
	if err != nil {
		return nil, "", err
	}
	return md, param.Value, nil
}

func runSSMEdit(ctx context.Context, client ssm.Client, name string, f *flags) (string, error) {
	display := ssmDisplay(name)

	sp := spinner.New(os.Stderr, fmt.Sprintf("Fetching %s ...", display))
	sp.Start()
	md, original, err := resolveForEdit(ctx, client, name, f.reveal)
	if err != nil {
		sp.Stop()
		return "", err
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Fetched %s", display))

	filename := path.Base(name)
	tmpPath, err := editor.TempFile(filename, []byte(original))
	if err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "Temp file: %s\n", tmpPath)

	editorCmd := editor.ResolveEditor(f.editorCmd)
	if err := editor.Open(editorCmd, tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "Recovery: your edits are saved at %s\n", tmpPath)
		return "", err
	}

	modifiedBytes, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("reading modified file: %w", err)
	}
	modified := trimEditorNewline(string(modifiedBytes))

	// Only remove the temp file once we have the edits in hand — an editor
	// failure above leaves it in place for recovery.
	defer editor.Cleanup(tmpPath)

	diffText, tooLarge := diff.Generate(original, modified, "original", "modified")
	if diffText == "" && !tooLarge {
		msg := "No changes detected. Skipping update."
		fmt.Fprintln(os.Stderr, msg)
		return msg, nil
	}

	fmt.Fprintf(os.Stderr, "\nParameter: %s\n\n", display)
	if tooLarge {
		fmt.Fprintln(os.Stderr, "Value is too large to display a diff; skipping diff.")
	} else {
		diff.Print(os.Stderr, diffText)
	}

	if f.dryRun {
		msg := "(dry-run: update skipped)"
		fmt.Fprintln(os.Stderr, "\n"+msg)
		return msg, nil
	}

	if !f.yes {
		if !confirm(os.Stdin, os.Stderr, "Update parameter?") {
			msg := "Update cancelled."
			fmt.Fprintln(os.Stderr, msg)
			return msg, nil
		}
	}

	return ssmPut(ctx, client, name, modified, md)
}

// ensureParamAbsent keeps new-parameter creation create-only: it returns an
// error if the parameter exists, or if its existence cannot be determined (any
// non-NotFound error), so a permission/network failure never reads as "absent".
func ensureParamAbsent(ctx context.Context, client ssm.Client, name, display string) error {
	if _, err := client.Describe(ctx, name); err == nil {
		return fmt.Errorf("%s already exists; use Edit instead", display)
	} else if !ssm.IsNotFound(err) {
		return err
	}
	return nil
}

// runSSMNewFile creates a new parameter by opening the editor on an empty
// buffer. It refuses to overwrite an existing parameter (create-only) and
// defaults the type to String unless --type is given.
func runSSMNewFile(ctx context.Context, client ssm.Client, name string, f *flags, ptype string) (string, error) {
	display := ssmDisplay(name)

	// Refuse to clobber an existing parameter; "new file" is create-only.
	if err := ensureParamAbsent(ctx, client, name, display); err != nil {
		return "", err
	}

	// ptype is the type the user picked in the browser's choice popup; fall back
	// to the launch --type when it's empty. Validate it either way so a bad value
	// is rejected here rather than accepted and failing later in Put.
	if ptype == "" {
		ptype = f.paramType
	}
	ptype, err := newParamType(ptype)
	if err != nil {
		return "", err
	}
	md := &ssm.Metadata{Type: ptype}

	filename := path.Base(name)
	tmpPath, err := editor.TempFile(filename, nil)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "Temp file: %s\n", tmpPath)

	editorCmd := editor.ResolveEditor(f.editorCmd)
	if err := editor.Open(editorCmd, tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "Recovery: your edits are saved at %s\n", tmpPath)
		return "", err
	}

	modifiedBytes, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("reading new file: %w", err)
	}
	defer editor.Cleanup(tmpPath)
	modified := trimEditorNewline(string(modifiedBytes))

	if modified == "" {
		msg := "Empty value — nothing created."
		fmt.Fprintln(os.Stderr, msg)
		return msg, nil
	}

	diffText, tooLarge := diff.Generate("", modified, "empty", "new")
	fmt.Fprintf(os.Stderr, "\nParameter: %s (%s)\n\n", display, ptype)
	if tooLarge {
		fmt.Fprintln(os.Stderr, "Value is too large to display a diff; skipping diff.")
	} else {
		diff.Print(os.Stderr, diffText)
	}

	if f.dryRun {
		msg := "(dry-run: creation skipped)"
		fmt.Fprintln(os.Stderr, "\n"+msg)
		return msg, nil
	}

	if !f.yes {
		if !confirm(os.Stdin, os.Stderr, "Create parameter?") {
			msg := "Creation cancelled."
			fmt.Fprintln(os.Stderr, msg)
			return msg, nil
		}
	}

	// Re-check just before writing: the editor session is long, so the parameter
	// may have appeared meanwhile. Narrows (does not fully close) the race.
	if err := ensureParamAbsent(ctx, client, name, display); err != nil {
		return "", err
	}

	return ssmPut(ctx, client, name, modified, md)
}

func runSSMPipeOut(ctx context.Context, client ssm.Client, name string, f *flags) error {
	display := ssmDisplay(name)
	md, err := client.Describe(ctx, name)
	if err != nil {
		return err
	}
	secure := md.IsSecure()
	if secure && !f.reveal {
		return fmt.Errorf("%s is a SecureString; re-run with --reveal to output the decrypted value", display)
	}
	param, err := client.Get(ctx, name, secure)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "✓ Fetched %s\n", display)
	_, err = io.WriteString(os.Stdout, param.Value)
	return err
}

func runSSMPipeIn(ctx context.Context, client ssm.Client, name string, f *flags) error {
	display := ssmDisplay(name)

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}
	modified := string(data)

	// Metadata (no value) is safe to fetch and gives us the Type to preserve.
	md, err := client.Describe(ctx, name)
	newParam := false
	if err != nil {
		if !ssm.IsNotFound(err) {
			return err
		}
		newParam = true
		ptype, err := newParamType(f.paramType)
		if err != nil {
			return err
		}
		md = &ssm.Metadata{Type: ptype}
		fmt.Fprintf(os.Stderr, "Parameter %s does not exist — will create as %s.\n", display, ptype)
	} else if f.paramType != "" && !strings.EqualFold(f.paramType, md.Type) {
		fmt.Fprintf(os.Stderr, "Note: --type applies only when creating; %s already exists as %s.\n", display, md.Type)
	}

	// Fetch the current value for a diff when we can. For an existing
	// SecureString this needs --reveal; without it we can still update under
	// --yes (no diff), but not review interactively.
	var original string
	haveOriginal := true
	switch {
	case newParam:
		// nothing to diff against
	case md.IsSecure() && !f.reveal:
		haveOriginal = false
		if !f.yes {
			return fmt.Errorf("%s is a SecureString; re-run with --reveal to review the diff, or --yes to update without one", display)
		}
	default:
		param, err := client.Get(ctx, name, md.IsSecure())
		if err != nil {
			return err
		}
		original = param.Value
	}

	// Append mode needs the current value to prepend it. For an existing
	// SecureString without --reveal we never fetched it, so append is impossible.
	if f.append {
		if !haveOriginal {
			return fmt.Errorf("%s is a SecureString; --append needs the current value, re-run with --reveal", display)
		}
		modified = original + modified
	}

	// No-op detection runs whenever the current value is known, even with --yes.
	if haveOriginal {
		diffText, tooLarge := diff.Generate(original, modified, "remote", "stdin")
		if diffText == "" && !tooLarge {
			fmt.Fprintln(os.Stderr, "No changes detected. Skipping update.")
			return nil
		}
		fmt.Fprintf(os.Stderr, "\nParameter: %s\n\n", display)
		if tooLarge {
			fmt.Fprintln(os.Stderr, "Value is too large to display a diff; skipping diff.")
		} else {
			diff.Print(os.Stderr, diffText)
		}
	}

	if f.dryRun {
		fmt.Fprintln(os.Stderr, "\n(dry-run: update skipped)")
		return nil
	}

	if !f.yes {
		tty, err := os.Open("/dev/tty")
		if err != nil {
			return fmt.Errorf("cannot open terminal for confirmation (use --yes to skip): %w", err)
		}
		defer func() { _ = tty.Close() }()
		if !confirm(tty, os.Stderr, "Update parameter?") {
			fmt.Fprintln(os.Stderr, "Update cancelled.")
			return nil
		}
	}

	_, err = ssmPut(ctx, client, name, modified, md)
	return err
}

// newParamType validates the --type flag for a to-be-created parameter,
// defaulting to String when unset.
// trimEditorNewline removes trailing newline(s) that an editor (e.g. vim, via
// fixeol) appends on save. SSM stores values verbatim, so a stray "\n" silently
// corrupts secrets like passwords and tokens. It prints a note to stderr when it
// changes the value, keeping the mutation visible before the diff/confirm. Only
// the editor-based flows call this — pipe-in keeps stdin's exact bytes.
func trimEditorNewline(value string) string {
	trimmed := strings.TrimRight(value, "\r\n")
	if trimmed != value {
		fmt.Fprintln(os.Stderr, "Note: removed trailing newline(s) — SSM values are stored verbatim (use pipe-in to keep them).")
	}
	return trimmed
}

func newParamType(flagVal string) (string, error) {
	switch flagVal {
	case "":
		return "String", nil
	case "String", "StringList", "SecureString":
		return flagVal, nil
	default:
		return "", fmt.Errorf("invalid --type %q (want String, StringList, or SecureString)", flagVal)
	}
}

func runSSMList(ctx context.Context, client ssm.Client, listPath string, f *flags) error {
	entries, err := client.List(ctx, listPath, f.recursive)
	if err != nil {
		return err
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, ssmDisplay(e.Name))
	}
	return writeLines(os.Stdout, lines)
}

func runSSMVersions(ctx context.Context, client ssm.Client, name string) error {
	hist, err := client.History(ctx, name)
	if err != nil {
		return err
	}
	w := os.Stdout
	for i, h := range hist {
		latest := "-"
		if i == 0 {
			latest = "LATEST"
		}
		if _, err := fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
			h.Version,
			h.LastModified.UTC().Format(time.RFC3339),
			h.Type,
			h.ModifiedUser,
			latest,
		); err != nil {
			return err
		}
	}
	return nil
}

func runSSMMetaPipeOut(ctx context.Context, client ssm.Client, name string) error {
	md, err := client.Describe(ctx, name)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "✓ Fetched metadata for %s\n", ssmDisplay(name))
	y, err := ssm.MarshalMeta(ssmDisplay(name), md)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(y)
	return err
}

func runSSMMetaEdit(ctx context.Context, client ssm.Client, name string, f *flags) (string, error) {
	display := ssmDisplay(name)

	// Editing metadata re-writes the parameter (SSM has no metadata-only API),
	// so we need the current value — gated by --reveal for SecureString.
	sp := spinner.New(os.Stderr, fmt.Sprintf("Fetching metadata for %s ...", display))
	sp.Start()
	md, value, err := resolveForEdit(ctx, client, name, f.reveal)
	if err != nil {
		sp.Stop()
		return "", err
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Fetched metadata for %s", display))

	originalYAML, err := ssm.MarshalMeta(display, md)
	if err != nil {
		return "", err
	}

	tmpPath, err := editor.TempFile("metadata.yaml", originalYAML)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "Temp file: %s\n", tmpPath)

	editorCmd := editor.ResolveEditor(f.editorCmd)
	if err := editor.Open(editorCmd, tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "Recovery: your edits are saved at %s\n", tmpPath)
		return "", err
	}

	modifiedBytes, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("reading modified file: %w", err)
	}

	// Only remove the temp file once we have the edits in hand — an editor
	// failure above leaves it in place for recovery.
	defer editor.Cleanup(tmpPath)

	diffText, tooLarge := diff.Generate(string(originalYAML), string(modifiedBytes), "original", "modified")
	if diffText == "" && !tooLarge {
		msg := "No changes detected. Skipping update."
		fmt.Fprintln(os.Stderr, msg)
		return msg, nil
	}

	fmt.Fprintf(os.Stderr, "\nMetadata: %s\n\n", display)
	if tooLarge {
		fmt.Fprintln(os.Stderr, "Metadata is too large to display a diff; skipping diff.")
	} else {
		diff.Print(os.Stderr, diffText)
	}

	newMeta, err := ssm.UnmarshalMeta(modifiedBytes)
	if err != nil {
		return "", err
	}

	if f.dryRun {
		msg := "(dry-run: update skipped)"
		fmt.Fprintln(os.Stderr, "\n"+msg)
		return msg, nil
	}

	if !f.yes {
		if !confirm(os.Stdin, os.Stderr, "Update metadata?") {
			msg := "Update cancelled."
			fmt.Fprintln(os.Stderr, msg)
			return msg, nil
		}
	}

	return ssmPut(ctx, client, name, value, newMeta)
}

func runSSMMetaPipeIn(ctx context.Context, client ssm.Client, name string, f *flags) error {
	display := ssmDisplay(name)

	newYAML, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}
	newMeta, err := ssm.UnmarshalMeta(newYAML)
	if err != nil {
		return err
	}

	md, value, err := resolveForEdit(ctx, client, name, f.reveal)
	if err != nil {
		return err
	}

	// Detect a no-op by comparing parsed metadata, not the raw stdin bytes
	// against canonical YAML — the "# uri" comment and field ordering would
	// otherwise make semantically-identical input look like a change.
	if *newMeta == *md {
		fmt.Fprintln(os.Stderr, "No changes detected. Skipping update.")
		return nil
	}

	originalYAML, err := ssm.MarshalMeta(display, md)
	if err != nil {
		return err
	}

	diffText, tooLarge := diff.Generate(string(originalYAML), string(newYAML), "remote", "stdin")
	fmt.Fprintf(os.Stderr, "\nMetadata: %s\n\n", display)
	if tooLarge {
		fmt.Fprintln(os.Stderr, "Metadata is too large to display a diff; skipping diff.")
	} else {
		diff.Print(os.Stderr, diffText)
	}

	if f.dryRun {
		fmt.Fprintln(os.Stderr, "\n(dry-run: update skipped)")
		return nil
	}

	if !f.yes {
		tty, err := os.Open("/dev/tty")
		if err != nil {
			return fmt.Errorf("cannot open terminal for confirmation (use --yes to skip): %w", err)
		}
		defer func() { _ = tty.Close() }()
		if !confirm(tty, os.Stderr, "Update metadata?") {
			fmt.Fprintln(os.Stderr, "Update cancelled.")
			return nil
		}
	}

	_, err = ssmPut(ctx, client, name, value, newMeta)
	return err
}

// ssmPut writes a parameter and reports success on stderr.
func ssmPut(ctx context.Context, client ssm.Client, name, value string, md *ssm.Metadata) (string, error) {
	display := ssmDisplay(name)
	sp := spinner.New(os.Stderr, fmt.Sprintf("Updating %s ...", display))
	sp.Start()
	if err := client.Put(ctx, ssm.PutInput{Name: name, Value: value, Meta: *md}); err != nil {
		sp.Stop()
		return "", err
	}
	msg := fmt.Sprintf("✓ Updated %s", display)
	sp.StopWithMessage(msg)
	return msg, nil
}
