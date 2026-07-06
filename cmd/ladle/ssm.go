package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

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

// runSSM dispatches an ssm:// URI. SecureString values are never exposed unless
// --reveal is given; without it, value-exposing operations refuse rather than
// print masked or ciphertext data.
func runSSM(ctx context.Context, u *uri.URI, f *flags) error {
	client, err := ssm.New(ctx, ssm.Options{Profile: f.profile, Region: f.region})
	if err != nil {
		return err
	}

	name := u.Key // normalized, always leading "/"

	stdoutPiped := !isTerminal(os.Stdout)
	stdinPiped := !isTerminal(os.Stdin)
	if stdoutPiped && stdinPiped {
		return fmt.Errorf("both stdin and stdout are redirected; this is not supported")
	}

	// --versions: print history (no TUI for ssm)
	if f.versions {
		if u.IsDirectory() {
			return fmt.Errorf("--versions requires a parameter name (not a path)")
		}
		return runSSMVersions(ctx, client, name)
	}

	// Directory/path => listing (printed to stdout; ssm has no TUI browser)
	if u.IsDirectory() {
		return runSSMList(ctx, client, name, f)
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
	secure := md.Type == "SecureString"
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

	filename := name[strings.LastIndexByte(name, '/')+1:]
	tmpPath, err := editor.TempFile(filename, []byte(original))
	if err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "Temp file: %s\n", tmpPath)
	defer editor.Cleanup(tmpPath)

	editorCmd := editor.ResolveEditor(f.editorCmd)
	if err := editor.Open(editorCmd, tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "Recovery: your edits are saved at %s\n", tmpPath)
		return "", err
	}

	modifiedBytes, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("reading modified file: %w", err)
	}
	modified := string(modifiedBytes)

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

func runSSMPipeOut(ctx context.Context, client ssm.Client, name string, f *flags) error {
	display := ssmDisplay(name)
	md, err := client.Describe(ctx, name)
	if err != nil {
		return err
	}
	secure := md.Type == "SecureString"
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

	// Metadata (no value) is always safe to fetch and gives us the Type to
	// preserve on write.
	md, err := client.Describe(ctx, name)
	newParam := false
	if err != nil {
		if !ssm.IsNotFound(err) {
			return err
		}
		newParam = true
		md = &ssm.Metadata{Type: "String"}
		fmt.Fprintf(os.Stderr, "Parameter %s does not exist — will create as String.\n", display)
	}

	// Diff requires the current value. Skip it entirely with --yes so that
	// non-interactive writes (incl. SecureString) don't need --reveal.
	if !f.yes {
		var original string
		if !newParam {
			secure := md.Type == "SecureString"
			if secure && !f.reveal {
				return fmt.Errorf("%s is a SecureString; re-run with --reveal to review the diff, or --yes to update without one", display)
			}
			param, err := client.Get(ctx, name, secure)
			if err != nil {
				return err
			}
			original = param.Value
		}

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

		if f.dryRun {
			fmt.Fprintln(os.Stderr, "\n(dry-run: update skipped)")
			return nil
		}

		tty, err := os.Open("/dev/tty")
		if err != nil {
			return fmt.Errorf("cannot open terminal for confirmation (use --yes to skip): %w", err)
		}
		defer func() { _ = tty.Close() }()
		if !confirm(tty, os.Stderr, "Update parameter?") {
			fmt.Fprintln(os.Stderr, "Update cancelled.")
			return nil
		}
	} else if f.dryRun {
		fmt.Fprintln(os.Stderr, "(dry-run: update skipped)")
		return nil
	}

	_, err = ssmPut(ctx, client, name, modified, md)
	return err
}

func runSSMList(ctx context.Context, client ssm.Client, path string, f *flags) error {
	entries, err := client.List(ctx, path, f.recursive)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	w := os.Stdout
	for _, e := range entries {
		fmt.Fprintln(w, ssmDisplay(e.Name))
	}
	return nil
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
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
			h.Version,
			h.LastModified.UTC().Format(time.RFC3339),
			h.Type,
			h.ModifiedUser,
			latest,
		)
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
	defer editor.Cleanup(tmpPath)

	editorCmd := editor.ResolveEditor(f.editorCmd)
	if err := editor.Open(editorCmd, tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "Recovery: your edits are saved at %s\n", tmpPath)
		return "", err
	}

	modifiedBytes, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("reading modified file: %w", err)
	}

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

	originalYAML, err := ssm.MarshalMeta(display, md)
	if err != nil {
		return err
	}

	diffText, tooLarge := diff.Generate(string(originalYAML), string(newYAML), "remote", "stdin")
	if diffText == "" && !tooLarge {
		fmt.Fprintln(os.Stderr, "No changes detected. Skipping update.")
		return nil
	}
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
