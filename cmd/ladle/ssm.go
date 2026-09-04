package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
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
	notFound := false
	if !stdinPiped {
		if _, derr := client.Describe(ctx, name); derr != nil {
			if !ssm.IsNotFound(derr) {
				return derr
			}
			notFound = true
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
	// A name with no parameter and no children is a create target: open the
	// editor on an empty buffer instead of erroring out, mirroring the browser's
	// "n" key and the pipe-in flow. The probe above already established absence,
	// so this goes straight to createSSMParam rather than paying for a second
	// Describe; the re-check just before writing still runs.
	if notFound {
		fmt.Fprintf(os.Stderr, "%s does not exist — creating new parameter.\n", ssmDisplay(name))
		_, err = createSSMParam(ctx, client, name, f, "")
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
	originalDesc := md.Description
	applyDescription(md, f)

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

	// --description is a change in its own right, so an untouched value is only a
	// no-op when the description is untouched too.
	diffText, tooLarge := diff.Generate(original, modified, "original", "modified")
	if diffText == "" && !tooLarge && md.Description == originalDesc {
		msg := "No changes detected. Skipping update."
		fmt.Fprintln(os.Stderr, msg)
		return msg, nil
	}

	fmt.Fprintf(os.Stderr, "\nParameter: %s\n\n", display)
	switch {
	case tooLarge:
		fmt.Fprintln(os.Stderr, "Value is too large to display a diff; skipping diff.")
	case diffText == "":
		fmt.Fprintln(os.Stderr, "Value unchanged.")
	default:
		diff.Print(os.Stderr, diffText)
	}
	printDescriptionChange(os.Stderr, originalDesc, md.Description)

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

	return ssmPut(ctx, client, name, modified, md, false)
}

// ensureParamAbsent keeps new-parameter creation create-only: it returns an
// error if the parameter exists, or if its existence cannot be determined (any
// non-NotFound error), so a permission/network failure never reads as "absent".
func ensureParamAbsent(ctx context.Context, client ssm.Client, name, display string) error {
	if _, err := client.Describe(ctx, name); err == nil {
		return fmt.Errorf("%s already exists (select it and press Enter to edit)", display)
	} else if !ssm.IsNotFound(err) {
		return err
	}
	return nil
}

// runSSMNewFile creates a new parameter by opening the editor on an empty
// buffer. It refuses to overwrite an existing parameter (create-only). The type
// comes from the browser's choice popup (ptype) or the launch --type; when
// neither is given it is asked for after the editor closes, once there is a
// value in hand to classify.
func runSSMNewFile(ctx context.Context, client ssm.Client, name string, f *flags, ptype string) (string, error) {
	// Refuse to clobber an existing parameter; "new file" is create-only.
	if err := ensureParamAbsent(ctx, client, name, ssmDisplay(name)); err != nil {
		return "", err
	}
	return createSSMParam(ctx, client, name, f, ptype)
}

// createSSMParam is runSSMNewFile for a caller that has just proved the
// parameter absent, so the up-front Describe would be a second answer to a
// question already asked. The re-check just before writing still runs.
func createSSMParam(ctx context.Context, client ssm.Client, name string, f *flags, ptype string) (string, error) {
	display := ssmDisplay(name)

	// ptype is the type the user picked in the browser's choice popup; fall back
	// to the launch --type when it's empty. Validate whichever we got before the
	// editor opens, so a bad value is rejected up front rather than after the
	// user has typed a value. Still empty means neither was given: ask below.
	if ptype == "" {
		ptype = f.paramType
	}
	if ptype != "" {
		var terr error
		if ptype, terr = newParamType(ptype); terr != nil {
			return "", terr
		}
	}

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
	// Keep the temp file on the paths that give up with the typed value still in
	// hand, the way an editor failure does. Losing a value the user just typed —
	// a pasted secret, say — to an unanswerable prompt would be worse than
	// leaving a stray file behind.
	keepTmp := false
	defer func() {
		if !keepTmp {
			editor.Cleanup(tmpPath)
		}
	}()
	modified := trimEditorNewline(string(modifiedBytes))

	if modified == "" {
		msg := "Empty value — nothing created."
		fmt.Fprintln(os.Stderr, msg)
		return msg, nil
	}

	// One scanner for every question asked below, shared so the type prompt does
	// not swallow the confirmation's line along with its own.
	sc := bufio.NewScanner(os.Stdin)

	// No type from the popup or --type: ask now. --yes means "don't prompt", so
	// it takes the String default instead.
	if ptype == "" {
		if f.yes {
			ptype = paramTypes[0]
		} else if ptype, err = promptParamType(sc, os.Stderr); err != nil {
			keepTmp = true
			fmt.Fprintf(os.Stderr, "Recovery: your value is saved at %s\n", tmpPath)
			return "", err
		}
	}
	md := &ssm.Metadata{Type: ptype, Description: f.description}
	if md.Description == "" && !f.yes {
		md.Description = promptDescription(sc, os.Stderr)
	}

	header := fmt.Sprintf("%s (%s)", display, ptype)
	if md.Description != "" {
		header += " — " + md.Description
	}
	diffText, tooLarge := diff.Generate("", modified, "empty", "new")
	fmt.Fprintf(os.Stderr, "\nParameter: %s\n\n", header)
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
		if !confirmScan(sc, os.Stderr, "Create parameter?") {
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

	return ssmPut(ctx, client, name, modified, md, true)
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
	originalDesc := md.Description
	applyDescription(md, f)

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
	// A --description change counts as a change even if the value is identical.
	if haveOriginal {
		diffText, tooLarge := diff.Generate(original, modified, "remote", "stdin")
		if diffText == "" && !tooLarge && md.Description == originalDesc {
			fmt.Fprintln(os.Stderr, "No changes detected. Skipping update.")
			return nil
		}
		fmt.Fprintf(os.Stderr, "\nParameter: %s\n\n", display)
		switch {
		case tooLarge:
			fmt.Fprintln(os.Stderr, "Value is too large to display a diff; skipping diff.")
		case diffText == "":
			fmt.Fprintln(os.Stderr, "Value unchanged.")
		default:
			diff.Print(os.Stderr, diffText)
		}
	}
	printDescriptionChange(os.Stderr, originalDesc, md.Description)

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

	_, err = ssmPut(ctx, client, name, modified, md, newParam)
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

// paramTypes lists the SSM parameter types a new parameter can be created as,
// in the order they are offered.
var paramTypes = []string{"String", "StringList", "SecureString"}

// promptParamType asks which type a new parameter should be created as. It is
// the direct-URI counterpart of the browser's choice popup, used when --type
// was not given; an empty answer takes the default (String).
func promptParamType(sc *bufio.Scanner, out io.Writer) (string, error) {
	_, _ = fmt.Fprintln(out, "\nParameter type:")
	for i, t := range paramTypes {
		_, _ = fmt.Fprintf(out, "  %d) %s\n", i+1, t)
	}
	// Re-ask on a bad answer rather than returning: by this point the caller
	// holds a value the user has already typed, and a slip on a menu is no
	// reason to make them type it again. Only unreadable input (EOF) gives up.
	for {
		_, _ = fmt.Fprintf(out, "Select [1-%d, default 1]: ", len(paramTypes))
		if !sc.Scan() {
			return "", fmt.Errorf("no parameter type selected")
		}
		answer := strings.TrimSpace(sc.Text())
		if answer == "" {
			return paramTypes[0], nil
		}
		n, err := strconv.Atoi(answer)
		if err != nil || n < 1 || n > len(paramTypes) {
			_, _ = fmt.Fprintf(out, "Invalid selection %q — enter a number from 1 to %d.\n", answer, len(paramTypes))
			continue
		}
		return paramTypes[n-1], nil
	}
}

// promptDescription asks for the optional description of a new parameter. An
// empty answer means "no description", so unlike the type there is nothing to
// fail on and EOF is not an error.
func promptDescription(sc *bufio.Scanner, out io.Writer) string {
	_, _ = fmt.Fprint(out, "Description (optional, press enter to skip): ")
	if !sc.Scan() {
		return ""
	}
	return strings.TrimSpace(sc.Text())
}

// printDescriptionChange renders a pending description change. The diff covers
// the value alone, so without this a metadata rewrite would go through the
// confirmation prompt unseen.
func printDescriptionChange(out io.Writer, before, after string) {
	if before == after {
		return
	}
	_, _ = fmt.Fprintf(out, "Description: %s -> %s\n", quoteOrNone(before), quoteOrNone(after))
}

func quoteOrNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return strconv.Quote(s)
}

// applyDescription overrides md's description with --description. Unlike
// --type, a description can be changed after creation, so the flag applies to
// every value write rather than creation only. Without the flag the description
// fetched via Describe stands, so an edit never silently drops it; clearing one
// goes through --meta, where the YAML is the authority on every attribute.
func applyDescription(md *ssm.Metadata, f *flags) {
	if f.description != "" {
		md.Description = f.description
	}
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
	// --meta edits every attribute through the YAML document, so a --description
	// alongside it would be a second, conflicting source of truth for one field.
	if f.description != "" {
		fmt.Fprintln(os.Stderr, "Note: --description is ignored with --meta; edit the description in the YAML instead.")
	}

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

	return ssmPut(ctx, client, name, value, newMeta, false)
}

func runSSMMetaPipeIn(ctx context.Context, client ssm.Client, name string, f *flags) error {
	display := ssmDisplay(name)
	// --meta edits every attribute through the YAML document, so a --description
	// alongside it would be a second, conflicting source of truth for one field.
	if f.description != "" {
		fmt.Fprintln(os.Stderr, "Note: --description is ignored with --meta; edit the description in the YAML instead.")
	}

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

	_, err = ssmPut(ctx, client, name, value, newMeta, false)
	return err
}

// ssmPut writes a parameter and reports success on stderr. SSM has a single
// write API for both cases, so `create` only selects the wording: a caller that
// knows the parameter did not exist says so rather than reporting an update.
func ssmPut(ctx context.Context, client ssm.Client, name, value string, md *ssm.Metadata, create bool) (string, error) {
	display := ssmDisplay(name)
	progress, done := "Updating %s ...", "✓ Updated %s"
	if create {
		progress, done = "Creating %s ...", "✓ Created %s"
	}
	sp := spinner.New(os.Stderr, fmt.Sprintf(progress, display))
	sp.Start()
	if err := client.Put(ctx, ssm.PutInput{Name: name, Value: value, Meta: *md}); err != nil {
		sp.Stop()
		return "", err
	}
	msg := fmt.Sprintf(done, display)
	sp.StopWithMessage(msg)
	return msg, nil
}
