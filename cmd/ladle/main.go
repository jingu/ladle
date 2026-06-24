package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/jingu/ladle/internal/apierror"
	"github.com/jingu/ladle/internal/browser"
	"github.com/jingu/ladle/internal/completion"
	"github.com/jingu/ladle/internal/contenttype"
	"github.com/jingu/ladle/internal/diff"
	"github.com/jingu/ladle/internal/editor"
	"github.com/jingu/ladle/internal/meta"
	"github.com/jingu/ladle/internal/spinner"
	"github.com/jingu/ladle/internal/storage"
	"github.com/jingu/ladle/internal/storage/azblobclient"
	"github.com/jingu/ladle/internal/storage/gcsclient"
	"github.com/jingu/ladle/internal/storage/s3client"
	"github.com/jingu/ladle/internal/uri"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		r := lipgloss.NewRenderer(os.Stderr)
		errStyle := r.NewStyle().Foreground(lipgloss.Color("9"))
		fmt.Fprintln(os.Stderr, errStyle.Render(fmt.Sprintf("Error: %s", err)))
		os.Exit(1)
	}
}

type flags struct {
	meta           bool
	versions       bool
	editorCmd      string
	profile        string
	region         string
	account        string
	project        string
	endpointURL    string
	noSignRequest  bool
	yes            bool
	force          bool
	dryRun         bool
	installComp    string
	completeBucket bool
	completePath   bool
}

func newRootCmd() *cobra.Command {
	f := &flags{}

	cmd := &cobra.Command{
		Use:     "ladle <uri>",
		Short:   "Edit cloud storage files with your local editor",
		Version: version,
		Long: `  ██
  ██
  ██       _     ____   _      _____
  ██      / \   |  _ \ | |    | ____|
  ██     / _ \  | | | || |    |  _|
  ██    / ___ \ | |_| || |___ | |___
 ▄██▄  /_/   \_\|____/ |_____||_____|
██████
 ▀████▀  ` + version + `

Edit cloud storage files directly from your terminal.
Download, edit in your favorite editor, diff, confirm, upload — all in one shot.

Supported backends: AWS S3 (s3://), Google Cloud Storage (gs://), and Azure Blob Storage (az://).

Examples:
  ladle s3://bucket/path/to/file.html
  ladle --meta s3://bucket/path/to/file.html
  ladle --profile production s3://bucket/path/to/file.html
  ladle s3://bucket/path/to/              # file browser mode
  ladle s3://                             # bucket list browser
  ladle s3://bucket/path/to/file.html > local.html        # download to local file
  ladle s3://bucket/path/to/file.html < local.html        # upload from local file
  ladle --meta s3://bucket/path/to/file.html > meta.yaml  # export metadata
  ladle --meta s3://bucket/path/to/file.html < meta.yaml  # import metadata
  ladle --versions s3://bucket/path/to/file.html          # version history

Google Cloud Storage (uses Application Default Credentials):
  ladle gs://bucket/path/to/file.html     # with gcloud auth application-default login
  ladle gs://bucket/path/to/              # file browser mode
  ladle --project myproject gs://         # bucket list browser

Azure Blob Storage (container = bucket, blob = key):
  ladle --account myaccount az://container/path/to/file.html
  ladle az://container/path/to/file.html  # with AZURE_STORAGE_ACCOUNT set
  ladle az://container/path/to/           # file browser mode
  ladle az://                             # container list browser`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return apierror.Classify(run(cmd, args, f))
		},
	}

	cmd.Flags().BoolVar(&f.meta, "meta", false, "Edit object metadata instead of file content")
	cmd.Flags().BoolVar(&f.versions, "versions", false, "Show version history for a file")
	cmd.Flags().StringVar(&f.editorCmd, "editor", "", "Editor command (overrides LADLE_EDITOR/EDITOR/VISUAL)")
	cmd.Flags().StringVar(&f.profile, "profile", "", "AWS named profile")
	cmd.Flags().StringVar(&f.region, "region", "", "AWS region")
	cmd.Flags().StringVar(&f.account, "account", "", "Azure storage account name (or AZURE_STORAGE_ACCOUNT)")
	cmd.Flags().StringVar(&f.project, "project", "", "GCP project ID for bucket listing (or GOOGLE_CLOUD_PROJECT)")
	cmd.Flags().StringVar(&f.endpointURL, "endpoint-url", "", "Custom endpoint URL (e.g. for MinIO, Azurite, or fake-gcs-server)")
	cmd.Flags().BoolVar(&f.noSignRequest, "no-sign-request", false, "Do not sign requests")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&f.force, "force", false, "Force editing of binary files")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Show diff without uploading")
	cmd.Flags().StringVar(&f.installComp, "install-completion", "", "Generate completion script (bash|zsh|fish)")
	cmd.Flags().BoolVar(&f.completeBucket, "complete-bucket", false, "Internal: complete bucket names")
	cmd.Flags().BoolVar(&f.completePath, "complete-path", false, "Internal: complete object paths")
	_ = cmd.Flags().MarkHidden("complete-bucket")
	_ = cmd.Flags().MarkHidden("complete-path")

	return cmd
}

func run(cmd *cobra.Command, args []string, f *flags) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Handle completion installation
	if f.installComp != "" {
		return completion.Generate(os.Stdout, completion.Shell(f.installComp))
	}

	if len(args) == 0 {
		return cmd.Help()
	}

	u, err := uri.Parse(args[0])
	if err != nil {
		return err
	}

	client, err := newClient(ctx, u, f)
	if err != nil {
		return err
	}

	// Handle internal completion helpers
	if f.completeBucket {
		return handleCompleteBucket(ctx, client, u, f)
	}
	if f.completePath {
		return handleCompletePath(ctx, client, u)
	}

	// --versions: show version history directly
	if f.versions {
		if u.IsDirectory() {
			return fmt.Errorf("--versions requires a file URI (not a directory)")
		}
		versionsKey := u.Key
		// Adjust URI to parent directory for browser, then open versions view
		parentKey := path.Dir(u.Key) + "/"
		if parentKey == "./" {
			parentKey = ""
		}
		dirURI, err := uri.Parse(fmt.Sprintf("%s://%s/%s", u.Scheme, u.Bucket, parentKey))
		if err != nil {
			return err
		}
		return runBrowser(ctx, client, dirURI, f, browser.WithVersionsKey(versionsKey))
	}

	// Directory => browser mode
	if u.IsDirectory() {
		return runBrowser(ctx, client, u, f)
	}

	// Check if the key is actually a directory prefix (e.g. "s3://bucket/dir" without trailing /)
	if u.Key != "" {
		entries, err := client.List(ctx, u.Bucket, u.Key+"/", "/")
		if err == nil && len(entries) > 0 {
			// It's a directory prefix — redirect to browser mode
			dirURI, err := uri.Parse(fmt.Sprintf("%s://%s/%s/", u.Scheme, u.Bucket, u.Key))
			if err != nil {
				return err
			}
			return runBrowser(ctx, client, dirURI, f)
		}
	}

	// Check for pipe/redirect
	stdoutPiped := !isTerminal(os.Stdout)
	stdinPiped := !isTerminal(os.Stdin)

	if stdoutPiped && stdinPiped {
		return fmt.Errorf("both stdin and stdout are redirected; this is not supported")
	}
	if stdoutPiped {
		if f.meta {
			return runMetaPipeOut(ctx, client, u, os.Stdout)
		}
		return runPipeOut(ctx, client, u, os.Stdout)
	}
	if stdinPiped {
		// stdin carries the payload, so confirmation reads from /dev/tty (opened lazily).
		openConfirm := func() (io.ReadCloser, error) { return os.Open("/dev/tty") }
		if f.meta {
			return runMetaPipeIn(ctx, client, u, f, os.Stdin, openConfirm)
		}
		return runPipeIn(ctx, client, u, f, os.Stdin, openConfirm)
	}

	// File editing (interactive)
	if f.meta {
		_, err = runMetaEdit(ctx, client, u, f)
		return err
	}
	_, err = runFileEdit(ctx, client, u, f)
	return err
}

func newClient(ctx context.Context, u *uri.URI, f *flags) (storage.Client, error) {
	switch u.Scheme {
	case uri.SchemeS3:
		return s3client.New(ctx, s3client.Options{
			Profile:       f.profile,
			Region:        f.region,
			EndpointURL:   f.endpointURL,
			NoSignRequest: f.noSignRequest,
		})
	case uri.SchemeAzure:
		return azblobclient.New(ctx, azblobclient.Options{
			Account:     f.account,
			EndpointURL: f.endpointURL,
		})
	case uri.SchemeGCS:
		return gcsclient.New(ctx, gcsclient.Options{
			Project:       f.project,
			EndpointURL:   f.endpointURL,
			NoSignRequest: f.noSignRequest,
		})
	default:
		return nil, fmt.Errorf("scheme %q is not yet supported (coming soon)", u.Scheme)
	}
}

func runFileEdit(ctx context.Context, client storage.Client, u *uri.URI, f *flags) (string, error) {
	// Download file
	var buf strings.Builder
	sp := spinner.New(os.Stderr, fmt.Sprintf("Downloading %s ...", u))
	sp.Start()
	if err := client.Download(ctx, u.Bucket, u.Key, &buf); err != nil {
		sp.Stop()
		return "", err
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Downloaded %s", u))
	original := buf.String()

	// Binary check
	if editor.IsBinary([]byte(original)) && !f.force {
		fmt.Fprintf(os.Stderr, "Warning: %s appears to be a binary file.\n", u)
		fmt.Fprintf(os.Stderr, "Use --force to edit anyway.\n")
		return "", fmt.Errorf("binary file detected")
	}

	// Create temp file
	filename := filepath.Base(u.Key)
	tmpPath, err := editor.TempFile(filename, []byte(original))
	if err != nil {
		return "", err
	}

	// Crash recovery: print temp path on interrupt
	fmt.Fprintf(os.Stderr, "Temp file: %s\n", tmpPath)

	// Open editor
	editorCmd := editor.ResolveEditor(f.editorCmd)
	if err := editor.Open(editorCmd, tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "Recovery: your edits are saved at %s\n", tmpPath)
		return "", err
	}

	// Read modified content
	modifiedBytes, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("reading modified file: %w", err)
	}
	modified := string(modifiedBytes)

	// Cleanup temp file
	defer editor.Cleanup(tmpPath)

	// Check for changes
	diffText := diff.Generate(original, modified, "original", "modified")
	if diffText == "" {
		msg := "No changes detected. Skipping upload."
		fmt.Fprintln(os.Stderr, msg)
		return msg, nil
	}

	// Show diff
	fmt.Fprintf(os.Stderr, "\nFile: %s\n\n", u)
	diff.Print(os.Stderr, diffText)

	if f.dryRun {
		msg := "(dry-run: upload skipped)"
		fmt.Fprintln(os.Stderr, "\n"+msg)
		return msg, nil
	}

	// Confirm
	if !f.yes {
		if !confirm(os.Stdin, os.Stderr, "Upload changes?") {
			msg := "Upload cancelled."
			fmt.Fprintln(os.Stderr, msg)
			return msg, nil
		}
	}

	// Detect content type
	ct := contenttype.Detect(u.Key)

	// Get existing metadata to preserve it
	existingMeta, err := client.HeadObject(ctx, u.Bucket, u.Key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not fetch metadata: %v\n", err)
		existingMeta = &storage.ObjectMetadata{}
	}
	existingMeta.ContentType = ct

	// Upload
	sp = spinner.New(os.Stderr, fmt.Sprintf("Uploading to %s ...", u))
	sp.Start()
	if err := client.Upload(ctx, u.Bucket, u.Key, strings.NewReader(modified), existingMeta); err != nil {
		sp.Stop()
		return "", err
	}
	msg := fmt.Sprintf("✓ Uploaded to %s", u)
	sp.StopWithMessage(msg)
	return msg, nil
}

func runMetaEdit(ctx context.Context, client storage.Client, u *uri.URI, f *flags) (string, error) {
	// Fetch metadata
	sp := spinner.New(os.Stderr, fmt.Sprintf("Fetching metadata for %s ...", u))
	sp.Start()
	objMeta, err := client.HeadObject(ctx, u.Bucket, u.Key)
	if err != nil {
		sp.Stop()
		return "", err
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Fetched metadata for %s", u))

	// Marshal to YAML
	originalYAML, err := meta.Marshal(u.String(), objMeta)
	if err != nil {
		return "", err
	}
	originalStr := string(originalYAML)

	// Create temp file
	tmpPath, err := editor.TempFile("metadata.yaml", originalYAML)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "Temp file: %s\n", tmpPath)

	// Open editor
	editorCmd := editor.ResolveEditor(f.editorCmd)
	if err := editor.Open(editorCmd, tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "Recovery: your edits are saved at %s\n", tmpPath)
		return "", err
	}

	// Read modified content
	modifiedBytes, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("reading modified file: %w", err)
	}
	modifiedStr := string(modifiedBytes)

	defer editor.Cleanup(tmpPath)

	// Check for changes
	diffText := diff.Generate(originalStr, modifiedStr, "original", "modified")
	if diffText == "" {
		msg := "No changes detected. Skipping update."
		fmt.Fprintln(os.Stderr, msg)
		return msg, nil
	}

	// Show diff
	fmt.Fprintf(os.Stderr, "\nMetadata: %s\n\n", u)
	diff.Print(os.Stderr, diffText)

	if f.dryRun {
		msg := "(dry-run: update skipped)"
		fmt.Fprintln(os.Stderr, "\n"+msg)
		return msg, nil
	}

	// Confirm
	if !f.yes {
		if !confirm(os.Stdin, os.Stderr, "Update metadata?") {
			msg := "Update cancelled."
			fmt.Fprintln(os.Stderr, msg)
			return msg, nil
		}
	}

	// Parse modified YAML
	newMeta, err := meta.Unmarshal(modifiedBytes)
	if err != nil {
		return "", err
	}

	// Update metadata using CopyObject
	sp = spinner.New(os.Stderr, fmt.Sprintf("Updating metadata for %s ...", u))
	sp.Start()
	if err := client.UpdateMetadata(ctx, u.Bucket, u.Key, newMeta); err != nil {
		sp.Stop()
		return "", err
	}
	msg := fmt.Sprintf("✓ Updated metadata for %s", u)
	sp.StopWithMessage(msg)
	return msg, nil
}

func runBrowser(ctx context.Context, client storage.Client, u *uri.URI, f *flags, extraOpts ...browser.RunOption) error {
	b := browser.New(client, u, os.Stdin, os.Stderr, version)
	editFn := func(selected *uri.URI) (string, error) {
		return runFileEdit(ctx, client, selected, f)
	}
	editMetaFn := func(selected *uri.URI) (string, error) {
		return runMetaEdit(ctx, client, selected, f)
	}
	downloadFn := func(selected *uri.URI, dir string) (string, error) {
		return runDownload(ctx, client, selected, dir)
	}
	restoreVersionFn := func(selected *uri.URI, versionID string) (string, error) {
		return runRestoreVersion(ctx, client, selected, versionID)
	}
	opts := []browser.RunOption{
		browser.WithEditMeta(editMetaFn),
		browser.WithDownload(downloadFn),
		browser.WithRestoreVersion(restoreVersionFn),
	}
	opts = append(opts, extraOpts...)
	return b.Run(ctx, editFn, opts...)
}

func runRestoreVersion(ctx context.Context, client storage.Client, u *uri.URI, versionID string) (string, error) {
	// Download current version
	var currentBuf strings.Builder
	sp := spinner.New(os.Stderr, fmt.Sprintf("Downloading current %s ...", u))
	sp.Start()
	if err := client.Download(ctx, u.Bucket, u.Key, &currentBuf); err != nil {
		sp.Stop()
		return "", err
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Downloaded current %s", u))
	current := currentBuf.String()

	// Download selected version
	var selectedBuf strings.Builder
	sp = spinner.New(os.Stderr, fmt.Sprintf("Downloading version %s ...", versionID))
	sp.Start()
	if err := client.DownloadVersion(ctx, u.Bucket, u.Key, versionID, &selectedBuf); err != nil {
		sp.Stop()
		return "", err
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Downloaded version %s", versionID))
	selected := selectedBuf.String()

	// Binary check
	isBinary := editor.IsBinary([]byte(selected))
	if isBinary {
		fmt.Fprintf(os.Stderr, "Warning: %s appears to be a binary file. Diff is not shown.\n", u)
	}

	if !isBinary {
		// Check for differences
		diffText := diff.Generate(current, selected, "current", "version "+versionID)
		if diffText == "" {
			msg := "No differences between current and selected version."
			fmt.Fprintln(os.Stderr, msg)
			return msg, nil
		}

		// Show diff
		fmt.Fprintf(os.Stderr, "\nFile: %s\n\n", u)
		diff.Print(os.Stderr, diffText)
	}

	// Confirm
	if !confirm(os.Stdin, os.Stderr, "Restore this version?") {
		msg := "Restore cancelled."
		fmt.Fprintln(os.Stderr, msg)
		return msg, nil
	}

	// Get existing metadata to preserve it
	existingMeta, err := client.HeadObject(ctx, u.Bucket, u.Key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not fetch metadata: %v\n", err)
		existingMeta = &storage.ObjectMetadata{}
	}

	// Upload restored content
	sp = spinner.New(os.Stderr, fmt.Sprintf("Restoring %s ...", u))
	sp.Start()
	if err := client.Upload(ctx, u.Bucket, u.Key, strings.NewReader(selected), existingMeta); err != nil {
		sp.Stop()
		return "", err
	}
	msg := fmt.Sprintf("✓ Restored %s to version %s", u, versionID)
	sp.StopWithMessage(msg)
	return msg, nil
}

func runDownload(ctx context.Context, client storage.Client, u *uri.URI, dir string) (string, error) {
	// Ensure directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating directory %s: %w", dir, err)
	}

	filename := filepath.Base(u.Key)
	destPath := filepath.Join(dir, filename)

	f, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("creating file %s: %w", destPath, err)
	}

	sp := spinner.New(os.Stderr, fmt.Sprintf("Downloading %s ...", u))
	sp.Start()
	if err := client.Download(ctx, u.Bucket, u.Key, f); err != nil {
		sp.Stop()
		_ = f.Close()
		_ = os.Remove(destPath)
		return "", err
	}
	msg := fmt.Sprintf("✓ Downloaded to %s", destPath)
	sp.StopWithMessage(msg)
	if err := f.Close(); err != nil {
		return "", err
	}
	return msg, nil
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return true
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func runPipeOut(ctx context.Context, client storage.Client, u *uri.URI, out io.Writer) error {
	sp := spinner.New(os.Stderr, fmt.Sprintf("Downloading %s ...", u))
	sp.Start()
	if err := client.Download(ctx, u.Bucket, u.Key, out); err != nil {
		sp.Stop()
		return err
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Downloaded %s", u))
	return nil
}

func runPipeIn(ctx context.Context, client storage.Client, u *uri.URI, f *flags, in io.Reader, openConfirm func() (io.ReadCloser, error)) error {
	// Read all input
	newContent, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("reading input: %w", err)
	}
	modified := string(newContent)

	// Binary check
	if editor.IsBinary(newContent) && !f.force {
		return fmt.Errorf("stdin appears to contain binary data (use --force to override)")
	}

	// Download current content for diff
	var original string
	var buf strings.Builder
	sp := spinner.New(os.Stderr, fmt.Sprintf("Downloading %s for diff ...", u))
	sp.Start()
	if err := client.Download(ctx, u.Bucket, u.Key, &buf); err != nil {
		sp.Stop()
		classified := apierror.Classify(err)
		var ae *apierror.Error
		if !errors.As(classified, &ae) || ae.Kind != apierror.KindNotFound {
			return err
		}
		fmt.Fprintf(os.Stderr, "Object %s does not exist — will create new.\n", u)
	} else {
		sp.StopWithMessage(fmt.Sprintf("✓ Downloaded %s", u))
		original = buf.String()
	}

	// Check for changes
	diffText := diff.Generate(original, modified, "remote", "stdin")
	if diffText == "" {
		fmt.Fprintln(os.Stderr, "No changes detected. Skipping upload.")
		return nil
	}

	// Show diff
	fmt.Fprintf(os.Stderr, "\nFile: %s\n\n", u)
	diff.Print(os.Stderr, diffText)

	if f.dryRun {
		fmt.Fprintln(os.Stderr, "\n(dry-run: upload skipped)")
		return nil
	}

	// Confirm (stdin is used for file content, so read from /dev/tty)
	if !f.yes {
		tty, err := openConfirm()
		if err != nil {
			return fmt.Errorf("cannot open terminal for confirmation (use --yes to skip): %w", err)
		}
		defer func() { _ = tty.Close() }()
		if !confirm(tty, os.Stderr, "Upload changes?") {
			fmt.Fprintln(os.Stderr, "Upload cancelled.")
			return nil
		}
	}

	// Detect content type
	ct := contenttype.Detect(u.Key)

	// Get existing metadata to preserve it
	existingMeta, err := client.HeadObject(ctx, u.Bucket, u.Key)
	if err != nil {
		existingMeta = &storage.ObjectMetadata{}
	}
	existingMeta.ContentType = ct

	// Upload
	sp = spinner.New(os.Stderr, fmt.Sprintf("Uploading to %s ...", u))
	sp.Start()
	if err := client.Upload(ctx, u.Bucket, u.Key, strings.NewReader(modified), existingMeta); err != nil {
		sp.Stop()
		return err
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Uploaded to %s", u))
	return nil
}

func runMetaPipeOut(ctx context.Context, client storage.Client, u *uri.URI, out io.Writer) error {
	sp := spinner.New(os.Stderr, fmt.Sprintf("Fetching metadata for %s ...", u))
	sp.Start()
	objMeta, err := client.HeadObject(ctx, u.Bucket, u.Key)
	if err != nil {
		sp.Stop()
		return err
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Fetched metadata for %s", u))

	yamlBytes, err := meta.Marshal(u.String(), objMeta)
	if err != nil {
		return err
	}

	_, err = out.Write(yamlBytes)
	return err
}

func runMetaPipeIn(ctx context.Context, client storage.Client, u *uri.URI, f *flags, in io.Reader, openConfirm func() (io.ReadCloser, error)) error {
	// Read YAML from input
	newYAML, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("reading input: %w", err)
	}

	// Validate YAML by parsing
	newMeta, err := meta.Unmarshal(newYAML)
	if err != nil {
		return err
	}

	// Fetch current metadata for diff
	sp := spinner.New(os.Stderr, fmt.Sprintf("Fetching metadata for %s ...", u))
	sp.Start()
	objMeta, err := client.HeadObject(ctx, u.Bucket, u.Key)
	if err != nil {
		sp.Stop()
		return err
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Fetched metadata for %s", u))

	originalYAML, err := meta.Marshal(u.String(), objMeta)
	if err != nil {
		return err
	}

	// Check for changes
	diffText := diff.Generate(string(originalYAML), string(newYAML), "remote", "stdin")
	if diffText == "" {
		fmt.Fprintln(os.Stderr, "No changes detected. Skipping update.")
		return nil
	}

	// Show diff
	fmt.Fprintf(os.Stderr, "\nMetadata: %s\n\n", u)
	diff.Print(os.Stderr, diffText)

	if f.dryRun {
		fmt.Fprintln(os.Stderr, "\n(dry-run: update skipped)")
		return nil
	}

	// Confirm via /dev/tty
	if !f.yes {
		tty, err := openConfirm()
		if err != nil {
			return fmt.Errorf("cannot open terminal for confirmation (use --yes to skip): %w", err)
		}
		defer func() { _ = tty.Close() }()
		if !confirm(tty, os.Stderr, "Update metadata?") {
			fmt.Fprintln(os.Stderr, "Update cancelled.")
			return nil
		}
	}

	// Update metadata
	sp = spinner.New(os.Stderr, fmt.Sprintf("Updating metadata for %s ...", u))
	sp.Start()
	if err := client.UpdateMetadata(ctx, u.Bucket, u.Key, newMeta); err != nil {
		sp.Stop()
		return err
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Updated metadata for %s", u))
	return nil
}

const bucketCacheTTL = 5 * time.Minute

func handleCompleteBucket(ctx context.Context, client storage.Client, u *uri.URI, f *flags) error {
	key := bucketCacheKey(u.Scheme, f.profile, f.account, f.project, f.endpointURL)
	buckets, err := loadBucketCache(key)
	if err != nil {
		buckets, err = client.ListBuckets(ctx)
		if err != nil {
			return nil // Silently fail for completions
		}
		_ = saveBucketCache(key, buckets)
	}
	for _, b := range buckets {
		if strings.HasPrefix(b, u.Bucket) {
			fmt.Println(b)
		}
	}
	return nil
}

// bucketCacheKey namespaces the bucket cache by backend so that buckets from
// different providers/accounts/endpoints do not collide. The endpoint is
// included because the same scheme/profile can point at different backends
// (e.g. real AWS vs MinIO/LocalStack, real Azure vs Azurite). Region and
// no-sign-request are intentionally omitted: ListBuckets returns the account's
// buckets regardless of region, and anonymous listing does not produce a
// distinct usable result.
func bucketCacheKey(scheme uri.Scheme, profile, account, project, endpoint string) string {
	key := string(scheme)
	if profile != "" {
		key += "_" + profile
	}
	if account != "" {
		key += "_" + account
	}
	if project != "" {
		key += "_" + project
	}
	if endpoint != "" {
		key += "_" + endpoint
	}
	return key
}

func bucketCachePath(key string) string {
	key = sanitizeCacheKey(key)
	if key == "" {
		key = "default"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "ladle", "buckets_"+key+".cache")
}

// sanitizeCacheKey reduces a cache key to a safe filename component. The key is
// derived from user-controlled values (profile, account), so path separators or
// ".." must not be able to escape the cache directory.
func sanitizeCacheKey(key string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, key)
}

func loadBucketCache(key string) ([]string, error) {
	p := bucketCachePath(key)
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if time.Since(info.ModTime()) > bucketCacheTTL {
		return nil, fmt.Errorf("cache expired")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil, nil
	}
	return strings.Split(content, "\n"), nil
}

func saveBucketCache(key string, buckets []string) error {
	p := bucketCachePath(key)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strings.Join(buckets, "\n")+"\n"), 0600)
}

func handleCompletePath(ctx context.Context, client storage.Client, u *uri.URI) error {
	entries, err := client.List(ctx, u.Bucket, u.Key, "/")
	if err != nil {
		return nil // Silently fail for completions
	}
	for _, e := range entries {
		raw := fmt.Sprintf("%s://%s/%s", u.Scheme, u.Bucket, e.Key)
		fmt.Println(raw)
	}
	return nil
}

func confirm(in io.Reader, out io.Writer, prompt string) bool {
	_, _ = fmt.Fprintf(out, "%s [y/N]: ", prompt)
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return false
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "y" || answer == "yes"
}
