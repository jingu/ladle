package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jingu/ladle/internal/browser"
	"github.com/jingu/ladle/internal/diff"
	"github.com/jingu/ladle/internal/localpath"
	"github.com/jingu/ladle/internal/spinner"
	"github.com/jingu/ladle/internal/ssm"
	"github.com/jingu/ladle/internal/storage"
	"github.com/jingu/ladle/internal/uri"
)

// secureMask is shown in place of a SecureString value when --reveal is not set.
const secureMask = "[SecureString — pass --reveal to view]\n"

// ssmStorageAdapter adapts an ssm.Client to the storage.Client interface so the
// existing TUI browser can drive SSM Parameter Store. It only implements what
// the browser actually uses (List, ListVersions, DownloadVersion, Delete, Copy);
// the remaining methods are best-effort or unused.
//
// The browser works in "S3-style" keys (no leading slash); the adapter prepends
// "/" for SSM API calls and strips it from names it returns. SecureString values
// are masked unless reveal is set.
type ssmStorageAdapter struct {
	client ssm.Client
	reveal bool
}

// toName converts a browser-world key (no leading slash) to an SSM parameter
// name/path (single leading slash). An empty key maps to the root "/".
func toName(key string) string {
	return "/" + strings.TrimLeft(key, "/")
}

// fromName converts an SSM name to a browser-world key (no leading slash).
func fromName(name string) string {
	return strings.TrimPrefix(name, "/")
}

func (a *ssmStorageAdapter) Download(ctx context.Context, _ string, key string, w io.Writer) error {
	return a.writeValue(ctx, w, func(decrypt bool) (*ssm.Parameter, error) {
		return a.client.Get(ctx, toName(key), decrypt)
	})
}

func (a *ssmStorageAdapter) DownloadVersion(ctx context.Context, _ string, key, versionID string, w io.Writer) error {
	v, err := strconv.ParseInt(versionID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid version %q: %w", versionID, err)
	}
	return a.writeValue(ctx, w, func(decrypt bool) (*ssm.Parameter, error) {
		return a.client.GetVersion(ctx, toName(key), v, decrypt)
	})
}

// writeValue fetches a parameter and writes its value, masking SecureString
// content unless reveal is set. With reveal it decrypts directly; without it,
// it fetches undecrypted (so SecureString ciphertext is never even retrieved as
// plaintext) and writes a placeholder for secure values.
func (a *ssmStorageAdapter) writeValue(_ context.Context, w io.Writer, get func(decrypt bool) (*ssm.Parameter, error)) error {
	if a.reveal {
		p, err := get(true)
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, p.Value)
		return err
	}
	p, err := get(false)
	if err != nil {
		return err
	}
	if p.Metadata.IsSecure() {
		_, err = io.WriteString(w, secureMask)
		return err
	}
	_, err = io.WriteString(w, p.Value)
	return err
}

func (a *ssmStorageAdapter) List(ctx context.Context, _ string, prefix, delimiter string) ([]storage.ListEntry, error) {
	entries, err := a.client.List(ctx, toName(prefix), delimiter == "")
	if err != nil {
		return nil, err
	}
	out := make([]storage.ListEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, storage.ListEntry{Key: fromName(e.Name), IsDir: e.IsDir})
	}
	return out, nil
}

func (a *ssmStorageAdapter) ListVersions(ctx context.Context, _ string, key string) ([]storage.ObjectVersion, error) {
	hist, err := a.client.History(ctx, toName(key))
	if err != nil {
		return nil, err
	}
	out := make([]storage.ObjectVersion, 0, len(hist))
	for i, h := range hist {
		out = append(out, storage.ObjectVersion{
			VersionID:    strconv.FormatInt(h.Version, 10),
			IsLatest:     i == 0, // History returns newest-first
			LastModified: h.LastModified,
		})
	}
	return out, nil
}

func (a *ssmStorageAdapter) Delete(ctx context.Context, _ string, key string) error {
	return a.client.Delete(ctx, toName(key))
}

func (a *ssmStorageAdapter) Copy(ctx context.Context, _ string, srcKey, dstKey string) error {
	src, dst := toName(srcKey), toName(dstKey)
	// Guard against a destination that only differs by slash normalization: a
	// self-copy here would let a subsequent Move delete the (only) parameter.
	if src == dst {
		return fmt.Errorf("source and destination are the same parameter (%s)", src)
	}
	md, err := a.client.Describe(ctx, src)
	if err != nil {
		return err
	}
	// Copying a SecureString requires decrypting it to re-write under the new
	// name; keep the same --reveal gate the rest of the tool uses for secrets.
	if md.IsSecure() && !a.reveal {
		return fmt.Errorf("%s is a SecureString; re-run with --reveal to copy or move it", src)
	}
	p, err := a.client.Get(ctx, src, md.IsSecure())
	if err != nil {
		return err
	}
	return a.client.Put(ctx, ssm.PutInput{Name: dst, Value: p.Value, Meta: *md})
}

func (a *ssmStorageAdapter) Upload(ctx context.Context, _ string, key string, r io.Reader, _ *storage.ObjectMetadata) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	name := toName(key)
	md, err := a.client.Describe(ctx, name)
	if err != nil {
		if !ssm.IsNotFound(err) {
			return err
		}
		md = &ssm.Metadata{Type: "String"}
	}
	return a.client.Put(ctx, ssm.PutInput{Name: name, Value: string(data), Meta: *md})
}

func (a *ssmStorageAdapter) HeadObject(context.Context, string, string) (*storage.ObjectMetadata, error) {
	return &storage.ObjectMetadata{}, nil
}

func (a *ssmStorageAdapter) UpdateMetadata(context.Context, string, string, *storage.ObjectMetadata) error {
	return fmt.Errorf("metadata editing is handled by the ssm meta editor, not the browser adapter")
}

// ListBuckets is never called: the SSM browser disables bucket-list mode.
func (a *ssmStorageAdapter) ListBuckets(context.Context) ([]string, error) {
	return nil, nil
}

// runSSMBrowser opens the TUI browser over SSM Parameter Store.
func runSSMBrowser(ctx context.Context, client ssm.Client, u *uri.URI, f *flags, extraOpts ...browser.RunOption) error {
	adapter := &ssmStorageAdapter{client: client, reveal: f.reveal}

	// The browser addresses parameters with S3-style keys (no leading slash).
	bu := &uri.URI{Scheme: uri.SchemeSSM, Bucket: "", Key: strings.TrimPrefix(u.Key, "/"), Raw: u.Raw}
	b := browser.New(adapter, bu, os.Stdin, os.Stderr, version)
	b.SetBucketListEnabled(false)

	editFn := func(sel *uri.URI) (string, error) { return runSSMEdit(ctx, client, sel.Key, f) }
	editMetaFn := func(sel *uri.URI) (string, error) { return runSSMMetaEdit(ctx, client, sel.Key, f) }
	downloadFn := func(sel *uri.URI, dir string) (string, error) { return runSSMDownload(ctx, client, sel.Key, dir, f) }
	restoreFn := func(sel *uri.URI, versionID string) (string, error) {
		return runSSMRestoreVersion(ctx, client, sel.Key, versionID, f)
	}
	newFileFn := func(sel *uri.URI, choice string) (string, error) {
		return runSSMNewFile(ctx, client, sel.Key, f, choice)
	}

	// New parameters pick a type in the browser; default the highlight to the
	// launch --type (String when unset). An invalid --type fails fast here, the
	// same as the pipe-in / edit flows, rather than being silently ignored.
	paramTypes := []string{"String", "StringList", "SecureString"}
	defType, err := newParamType(f.paramType)
	if err != nil {
		return err
	}
	defIndex := 0
	for i, t := range paramTypes {
		if t == defType {
			defIndex = i
			break
		}
	}

	opts := []browser.RunOption{
		browser.WithEditMeta(editMetaFn),
		browser.WithDownload(downloadFn),
		browser.WithRestoreVersion(restoreFn),
		browser.WithNewFile(newFileFn),
		browser.WithNewFileChoices("New parameter type", paramTypes, defIndex),
	}
	opts = append(opts, extraOpts...)
	return b.Run(ctx, editFn, opts...)
}

// runSSMDownload writes a parameter's value to a local file (SecureString gated
// by --reveal).
func runSSMDownload(ctx context.Context, client ssm.Client, name, dir string, f *flags) (string, error) {
	_, value, err := resolveForEdit(ctx, client, name, f.reveal)
	if err != nil {
		return "", err
	}
	dir = localpath.ExpandTilde(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating directory %s: %w", dir, err)
	}
	destPath := filepath.Join(dir, path.Base(name))
	if err := os.WriteFile(destPath, []byte(value), 0600); err != nil {
		return "", fmt.Errorf("writing %s: %w", destPath, err)
	}
	msg := fmt.Sprintf("✓ Downloaded to %s", destPath)
	fmt.Fprintln(os.Stderr, msg)
	return msg, nil
}

// runSSMRestoreVersion restores a previous version's value as the current value.
func runSSMRestoreVersion(ctx context.Context, client ssm.Client, name, versionID string, f *flags) (string, error) {
	display := ssmDisplay(name)
	v, err := strconv.ParseInt(versionID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid version %q: %w", versionID, err)
	}

	md, err := client.Describe(ctx, name)
	if err != nil {
		return "", err
	}
	if md.IsSecure() && !f.reveal {
		return "", fmt.Errorf("%s is a SecureString; re-run with --reveal to restore a version", display)
	}

	sp := spinner.New(os.Stderr, fmt.Sprintf("Fetching version %d of %s ...", v, display))
	sp.Start()
	old, err := client.GetVersion(ctx, name, v, md.IsSecure())
	if err != nil {
		sp.Stop()
		return "", err
	}
	current, err := client.Get(ctx, name, md.IsSecure())
	if err != nil {
		sp.Stop()
		return "", err
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Fetched version %d of %s", v, display))

	diffText, tooLarge := diff.Generate(current.Value, old.Value, "current", fmt.Sprintf("version %d", v))
	if diffText == "" && !tooLarge {
		msg := "No differences between current and selected version."
		fmt.Fprintln(os.Stderr, msg)
		return msg, nil
	}
	fmt.Fprintf(os.Stderr, "\nParameter: %s\n\n", display)
	if tooLarge {
		fmt.Fprintln(os.Stderr, "Value is too large to display a diff; skipping diff.")
	} else {
		diff.Print(os.Stderr, diffText)
	}

	if !confirm(os.Stdin, os.Stderr, "Restore this version?") {
		msg := "Restore cancelled."
		fmt.Fprintln(os.Stderr, msg)
		return msg, nil
	}

	return ssmPut(ctx, client, name, old.Value, md)
}

// compile-time check that the adapter satisfies storage.Client.
var _ storage.Client = (*ssmStorageAdapter)(nil)
