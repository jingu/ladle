package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/jingu/ladle/internal/browser"
	"github.com/jingu/ladle/internal/completion"
	"github.com/jingu/ladle/internal/contenttype"
	"github.com/jingu/ladle/internal/diff"
	"github.com/jingu/ladle/internal/editor"
	"github.com/jingu/ladle/internal/meta"
	"github.com/jingu/ladle/internal/storage"
	"github.com/jingu/ladle/internal/storage/s3client"
	"github.com/jingu/ladle/internal/uri"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

type flags struct {
	meta           bool
	editorCmd      string
	profile        string
	region         string
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
		Long: `ladle downloads a file from cloud storage, opens it in your editor,
and uploads the changes back when you save and close the editor.

Examples:
  ladle s3://bucket/path/to/file.html
  ladle --meta s3://bucket/path/to/file.html
  ladle --profile production s3://bucket/path/to/file.html
  ladle s3://bucket/path/to/              # file browser mode
  ladle s3://                             # bucket list browser`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, args, f)
		},
	}

	cmd.Flags().BoolVar(&f.meta, "meta", false, "Edit object metadata instead of file content")
	cmd.Flags().StringVar(&f.editorCmd, "editor", "", "Editor command (overrides LADLE_EDITOR/EDITOR/VISUAL)")
	cmd.Flags().StringVar(&f.profile, "profile", "", "AWS named profile")
	cmd.Flags().StringVar(&f.region, "region", "", "AWS region")
	cmd.Flags().StringVar(&f.endpointURL, "endpoint-url", "", "Custom endpoint URL (e.g. for MinIO)")
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
		return handleCompleteBucket(ctx, client, u, f.profile)
	}
	if f.completePath {
		return handleCompletePath(ctx, client, u)
	}

	// Directory => browser mode
	if u.IsDirectory() {
		return runBrowser(ctx, client, u, f)
	}

	// File editing
	if f.meta {
		return runMetaEdit(ctx, client, u, f)
	}
	return runFileEdit(ctx, client, u, f)
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
	default:
		return nil, fmt.Errorf("scheme %q is not yet supported (coming soon)", u.Scheme)
	}
}

func runFileEdit(ctx context.Context, client storage.Client, u *uri.URI, f *flags) error {
	// Download file
	var buf strings.Builder
	fmt.Fprintf(os.Stderr, "Downloading %s ...\n", u)
	if err := client.Download(ctx, u.Bucket, u.Key, &buf); err != nil {
		return err
	}
	original := buf.String()

	// Binary check
	if editor.IsBinary([]byte(original)) && !f.force {
		fmt.Fprintf(os.Stderr, "Warning: %s appears to be a binary file.\n", u)
		fmt.Fprintf(os.Stderr, "Use --force to edit anyway.\n")
		return fmt.Errorf("binary file detected")
	}

	// Create temp file
	filename := filepath.Base(u.Key)
	tmpPath, err := editor.TempFile(filename, []byte(original))
	if err != nil {
		return err
	}

	// Crash recovery: print temp path on interrupt
	fmt.Fprintf(os.Stderr, "Temp file: %s\n", tmpPath)

	// Open editor
	editorCmd := editor.ResolveEditor(f.editorCmd)
	if err := editor.Open(editorCmd, tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "Recovery: your edits are saved at %s\n", tmpPath)
		return err
	}

	// Read modified content
	modifiedBytes, err := os.ReadFile(tmpPath)
	if err != nil {
		return fmt.Errorf("reading modified file: %w", err)
	}
	modified := string(modifiedBytes)

	// Cleanup temp file
	defer editor.Cleanup(tmpPath)

	// Check for changes
	diffText := diff.Generate(original, modified, "original", "modified")
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

	// Confirm
	if !f.yes {
		if !confirm(os.Stdin, os.Stderr, "Upload changes?") {
			fmt.Fprintln(os.Stderr, "Upload cancelled.")
			return nil
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
	fmt.Fprintf(os.Stderr, "Uploading to %s ...\n", u)
	if err := client.Upload(ctx, u.Bucket, u.Key, strings.NewReader(modified), existingMeta); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Done.")
	return nil
}

func runMetaEdit(ctx context.Context, client storage.Client, u *uri.URI, f *flags) error {
	// Fetch metadata
	fmt.Fprintf(os.Stderr, "Fetching metadata for %s ...\n", u)
	objMeta, err := client.HeadObject(ctx, u.Bucket, u.Key)
	if err != nil {
		return err
	}

	// Marshal to YAML
	originalYAML, err := meta.Marshal(u.String(), objMeta)
	if err != nil {
		return err
	}
	originalStr := string(originalYAML)

	// Create temp file
	tmpPath, err := editor.TempFile("metadata.yaml", originalYAML)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Temp file: %s\n", tmpPath)

	// Open editor
	editorCmd := editor.ResolveEditor(f.editorCmd)
	if err := editor.Open(editorCmd, tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "Recovery: your edits are saved at %s\n", tmpPath)
		return err
	}

	// Read modified content
	modifiedBytes, err := os.ReadFile(tmpPath)
	if err != nil {
		return fmt.Errorf("reading modified file: %w", err)
	}
	modifiedStr := string(modifiedBytes)

	defer editor.Cleanup(tmpPath)

	// Check for changes
	diffText := diff.Generate(originalStr, modifiedStr, "original", "modified")
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

	// Confirm
	if !f.yes {
		if !confirm(os.Stdin, os.Stderr, "Update metadata?") {
			fmt.Fprintln(os.Stderr, "Update cancelled.")
			return nil
		}
	}

	// Parse modified YAML
	newMeta, err := meta.Unmarshal(modifiedBytes)
	if err != nil {
		return err
	}

	// Update metadata using CopyObject
	fmt.Fprintf(os.Stderr, "Updating metadata for %s ...\n", u)
	if err := client.UpdateMetadata(ctx, u.Bucket, u.Key, newMeta); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Done.")
	return nil
}

func runBrowser(ctx context.Context, client storage.Client, u *uri.URI, f *flags) error {
	b := browser.New(client, u, os.Stdin, os.Stderr)
	for {
		sel, err := b.Run(ctx)
		if err != nil {
			return err
		}
		if sel.Action == browser.ActionQuit {
			return nil
		}
		if sel.Action == browser.ActionEdit {
			if f.meta {
				if err := runMetaEdit(ctx, client, sel.URI, f); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				}
			} else {
				if err := runFileEdit(ctx, client, sel.URI, f); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				}
			}
			// Return to browser after editing
			continue
		}
	}
}

const bucketCacheTTL = 5 * time.Minute

func handleCompleteBucket(ctx context.Context, client storage.Client, u *uri.URI, profile string) error {
	buckets, err := loadBucketCache(profile)
	if err != nil {
		buckets, err = client.ListBuckets(ctx)
		if err != nil {
			return nil // Silently fail for completions
		}
		_ = saveBucketCache(profile, buckets)
	}
	for _, b := range buckets {
		if strings.HasPrefix(b, u.Bucket) {
			fmt.Println(b)
		}
	}
	return nil
}

func bucketCachePath(profile string) string {
	if profile == "" {
		profile = "default"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "ladle", "buckets_"+profile+".cache")
}

func loadBucketCache(profile string) ([]string, error) {
	p := bucketCachePath(profile)
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

func saveBucketCache(profile string, buckets []string) error {
	p := bucketCachePath(profile)
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
	fmt.Fprintf(out, "%s [y/N]: ", prompt)
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return false
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "y" || answer == "yes"
}
