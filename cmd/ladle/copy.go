package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/jingu/ladle/internal/apierror"
	"github.com/jingu/ladle/internal/diff"
	"github.com/jingu/ladle/internal/editor"
	"github.com/jingu/ladle/internal/meta"
	"github.com/jingu/ladle/internal/spinner"
	"github.com/jingu/ladle/internal/storage"
	"github.com/jingu/ladle/internal/uri"
	"github.com/spf13/cobra"
)

func newCopyCmd() *cobra.Command {
	f := &flags{}
	cmd := &cobra.Command{
		Use:   "cp <source-uri> <destination-uri>",
		Short: "Copy an object with its metadata",
		Long: `Copy an object and its standard metadata.

The source is downloaded completely before the destination is changed. Use --dry-run to inspect content and metadata differences without writing.`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
			defer cancel()
			return apierror.Classify(runCopyCommand(ctx, args[0], args[1], f))
		},
	}

	cmd.Flags().StringVar(&f.profile, "profile", "", "AWS named profile")
	cmd.Flags().StringVar(&f.region, "region", "", "AWS region")
	cmd.Flags().StringVar(&f.account, "account", "", "Azure storage account name (or AZURE_STORAGE_ACCOUNT)")
	cmd.Flags().StringVar(&f.project, "project", "", "GCP project ID (or GOOGLE_CLOUD_PROJECT)")
	cmd.Flags().StringVar(&f.endpointURL, "endpoint-url", "", "Custom endpoint URL (e.g. for MinIO, Azurite, or fake-gcs-server)")
	cmd.Flags().BoolVar(&f.noSignRequest, "no-sign-request", false, "Do not sign requests")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Show differences without copying")

	return cmd
}

func runCopyCommand(ctx context.Context, sourceRaw, destinationRaw string, f *flags) error {
	source, err := uri.Parse(sourceRaw)
	if err != nil {
		return err
	}
	destination, err := uri.Parse(destinationRaw)
	if err != nil {
		return err
	}
	if source.Scheme == uri.SchemeSSM || destination.Scheme == uri.SchemeSSM {
		return fmt.Errorf("cp supports object storage URIs only")
	}
	if source.IsDirectory() || destination.IsDirectory() {
		return fmt.Errorf("cp requires source and destination file URIs")
	}

	sourceClient, err := newClient(ctx, source, f)
	if err != nil {
		return err
	}
	destinationClient, err := newClient(ctx, destination, f)
	if err != nil {
		return err
	}
	return runCopy(ctx, sourceClient, destinationClient, source, destination, f, os.Stdin, os.Stderr)
}

const maxCopyDiffBytes = 2 << 20

var errCopyDiffLimit = errors.New("copy diff limit reached")

type cappedBuffer struct {
	buffer    bytes.Buffer
	truncated bool
}

func (b *cappedBuffer) Len() int {
	return b.buffer.Len()
}

func (b *cappedBuffer) String() string {
	return b.buffer.String()
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	remaining := maxCopyDiffBytes - b.Len()
	if remaining <= 0 {
		b.truncated = true
		return 0, errCopyDiffLimit
	}
	if len(p) > remaining {
		b.truncated = true
		n, _ := b.buffer.Write(p[:remaining])
		return n, errCopyDiffLimit
	}
	return b.buffer.Write(p)
}

func readCopyDiffPrefix(r io.Reader) (string, bool, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxCopyDiffBytes+1))
	if err != nil {
		return "", false, err
	}
	return string(data), len(data) > maxCopyDiffBytes, nil
}

func writeCopyStatus(out io.Writer, format string, args ...any) error {
	if _, err := fmt.Fprintf(out, format, args...); err != nil {
		return fmt.Errorf("writing copy status: %w", err)
	}
	return nil
}

func openCopyConfirmation(in io.Reader, stdinPiped bool, openTTY func() (io.ReadCloser, error)) (io.ReadCloser, error) {
	if stdinPiped {
		return openTTY()
	}
	return io.NopCloser(in), nil
}

func runCopy(ctx context.Context, sourceClient, destinationClient storage.Client, source, destination *uri.URI, f *flags, in io.Reader, out io.Writer) error {
	tmp, err := os.CreateTemp("", "ladle-copy-*")
	if err != nil {
		return fmt.Errorf("creating temporary copy file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	defer func() { _ = tmp.Close() }()

	sp := spinner.New(out, fmt.Sprintf("Downloading %s ...", source))
	sp.Start()
	if err := sourceClient.Download(ctx, source.Bucket, source.Key, tmp); err != nil {
		sp.Stop()
		return fmt.Errorf("downloading source %s: %w", source, err)
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Downloaded %s", source))

	sourceMeta, err := sourceClient.HeadObject(ctx, source.Bucket, source.Key)
	if err != nil {
		return fmt.Errorf("getting source metadata for %s: %w", source, err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewinding temporary copy file: %w", err)
	}

	sourceContent, sourceTooLarge, err := readCopyDiffPrefix(tmp)
	if err != nil {
		return fmt.Errorf("reading source for diff: %w", err)
	}

	var destinationContent cappedBuffer
	sp = spinner.New(out, fmt.Sprintf("Downloading %s for diff ...", destination))
	sp.Start()
	destinationExists := false
	err = destinationClient.Download(ctx, destination.Bucket, destination.Key, &destinationContent)
	if err != nil {
		if errors.Is(err, errCopyDiffLimit) {
			destinationExists = true
			sp.StopWithMessage(fmt.Sprintf("✓ Downloaded %s for diff (truncated)", destination))
		} else {
			sp.Stop()
			classified := apierror.Classify(err)
			var apiErr *apierror.Error
			if !errors.As(classified, &apiErr) || apiErr.Kind != apierror.KindNotFound {
				return fmt.Errorf("downloading destination %s: %w", destination, err)
			}
			if err := writeCopyStatus(out, "Object %s does not exist — will create new.\n", destination); err != nil {
				return err
			}
		}
	} else {
		destinationExists = true
		sp.StopWithMessage(fmt.Sprintf("✓ Downloaded %s", destination))
	}

	destinationMeta, err := destinationClient.HeadObject(ctx, destination.Bucket, destination.Key)
	if err != nil {
		classified := apierror.Classify(err)
		var apiErr *apierror.Error
		if !errors.As(classified, &apiErr) || apiErr.Kind != apierror.KindNotFound {
			return fmt.Errorf("getting destination metadata for %s: %w", destination, err)
		}
		destinationMeta = &storage.ObjectMetadata{}
	}
	destinationBody := destinationContent.String()
	binaryContent := editor.IsBinary([]byte(sourceContent)) || editor.IsBinary([]byte(destinationBody))
	contentDifferent := sourceTooLarge || destinationContent.truncated || sourceContent != destinationBody
	contentDiff := ""
	contentTooLarge := sourceTooLarge || destinationContent.truncated
	if !binaryContent && !contentTooLarge {
		contentDiff, contentTooLarge = diff.Generate(destinationBody, sourceContent, "destination", "source")
		contentDifferent = contentDiff != "" || contentTooLarge
	}
	sourceMetaYAML, err := meta.Marshal("metadata", sourceMeta)
	if err != nil {
		return err
	}
	destinationMetaYAML, err := meta.Marshal("metadata", destinationMeta)
	if err != nil {
		return err
	}
	metadataDiff, metadataTooLarge := diff.Generate(string(destinationMetaYAML), string(sourceMetaYAML), "destination metadata", "source metadata")

	if destinationExists && !contentDifferent && metadataDiff == "" && !metadataTooLarge {
		if err := writeCopyStatus(out, "No changes detected. Skipping copy.\n"); err != nil {
			return err
		}
		return nil
	}

	if err := writeCopyStatus(out, "\nSource: %s\nDestination: %s\n\n", source, destination); err != nil {
		return err
	}
	if binaryContent {
		if err := writeCopyStatus(out, "Binary content; skipping content diff.\n"); err != nil {
			return err
		}
	} else if contentTooLarge {
		if err := writeCopyStatus(out, "Content is too large to display a diff; skipping content diff.\n"); err != nil {
			return err
		}
	} else if contentDiff != "" {
		diff.Print(out, contentDiff)
	}
	if metadataTooLarge {
		if err := writeCopyStatus(out, "Metadata is too large to display a diff; skipping metadata diff.\n"); err != nil {
			return err
		}
	} else if metadataDiff != "" {
		if err := writeCopyStatus(out, "\nMetadata:\n"); err != nil {
			return err
		}
		diff.Print(out, metadataDiff)
	}

	if f.dryRun {
		if err := writeCopyStatus(out, "\n(dry-run: copy skipped)\n"); err != nil {
			return err
		}
		return nil
	}
	if !f.yes {
		confirmIn, err := openCopyConfirmation(
			in,
			!isTerminal(os.Stdin),
			func() (io.ReadCloser, error) { return os.Open("/dev/tty") },
		)
		if err != nil {
			return fmt.Errorf("cannot open terminal for confirmation (use --yes to skip): %w", err)
		}
		defer func() { _ = confirmIn.Close() }()
		if !confirm(confirmIn, out, "Copy this object?") {
			if err := writeCopyStatus(out, "Copy cancelled.\n"); err != nil {
				return err
			}
			return nil
		}
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewinding temporary copy file: %w", err)
	}

	sp = spinner.New(out, fmt.Sprintf("Copying to %s ...", destination))
	sp.Start()
	if err := destinationClient.Upload(ctx, destination.Bucket, destination.Key, tmp, sourceMeta); err != nil {
		sp.Stop()
		return fmt.Errorf("uploading destination %s: %w", destination, err)
	}
	sp.StopWithMessage(fmt.Sprintf("✓ Copied %s to %s", source, destination))
	return nil
}
