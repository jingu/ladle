// Package browser implements the interactive file browser for cloud storage.
package browser

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jingu/ladle/internal/storage"
	"github.com/jingu/ladle/internal/uri"
)

// EditFunc is called when a file is selected for editing.
// It receives the parsed URI and should perform the edit workflow.
type EditFunc func(u *uri.URI) error

// EditMetaFunc is called to edit object metadata.
type EditMetaFunc func(u *uri.URI) error

// DownloadFunc is called to download a file to a local directory.
// It receives the parsed URI and a local directory path.
type DownloadFunc func(u *uri.URI, dir string) error

// Browser provides an interactive file browser.
type Browser struct {
	client            storage.Client
	scheme            uri.Scheme
	bucket            string
	prefix            string
	bucketListEnabled bool
	in                io.Reader
	out               io.Writer
	version           string
}

// New creates a new Browser.
func New(client storage.Client, u *uri.URI, in io.Reader, out io.Writer, version string) *Browser {
	return &Browser{
		client:            client,
		scheme:            u.Scheme,
		bucket:            u.Bucket,
		prefix:            u.Key,
		bucketListEnabled: true,
		in:                in,
		out:               out,
		version:           version,
	}
}

// RunOption configures optional callbacks for the browser.
type RunOption func(*runOptions)

type runOptions struct {
	editMetaFn EditMetaFunc
	downloadFn DownloadFunc
}

// WithEditMeta sets the callback for editing metadata.
func WithEditMeta(fn EditMetaFunc) RunOption {
	return func(o *runOptions) { o.editMetaFn = fn }
}

// WithDownload sets the callback for downloading files.
func WithDownload(fn DownloadFunc) RunOption {
	return func(o *runOptions) { o.downloadFn = fn }
}

// Run starts the interactive browser. It runs a single TUI program.
// editFn is called (with TUI suspended) when a file is selected.
func (b *Browser) Run(ctx context.Context, editFn EditFunc, opts ...RunOption) error {
	nodes, header, canGoUp, err := b.buildView(ctx)
	if err != nil {
		return err
	}

	var ro runOptions
	for _, o := range opts {
		o(&ro)
	}

	m := model{
		nodes:      nodes,
		header:     header,
		version:    b.version,
		canGoUp:    canGoUp,
		client:     b.client,
		ctx:        ctx,
		bucket:     b.bucket,
		scheme:     string(b.scheme),
		browser:    b,
		editFn:     editFn,
		editMetaFn: ro.editMetaFn,
		downloadFn: ro.downloadFn,
	}

	p := tea.NewProgram(m, tea.WithInput(b.in), tea.WithOutput(b.out))
	_, err = p.Run()
	if err != nil {
		return fmt.Errorf("running browser: %w", err)
	}
	return nil
}

// buildView returns the nodes, header, and canGoUp for the current Browser state.
func (b *Browser) buildView(ctx context.Context) ([]*node, string, bool, error) {
	return b.buildViewFor(ctx, b.bucket, b.prefix)
}

// buildViewFor returns nodes, header, and canGoUp for the given bucket/prefix
// without reading or modifying Browser fields. Safe to call from goroutines.
func (b *Browser) buildViewFor(ctx context.Context, bucket, prefix string) ([]*node, string, bool, error) {
	if bucket == "" {
		// Bucket list mode
		buckets, err := b.client.ListBuckets(ctx)
		if err != nil {
			return nil, "", false, fmt.Errorf("listing buckets: %w", err)
		}
		nodes := make([]*node, len(buckets))
		for i, name := range buckets {
			nodes[i] = &node{entry: entry{name: name, isBucket: true}}
		}
		header := fmt.Sprintf("%s://", b.scheme)
		return nodes, header, false, nil
	}

	// Object list mode
	nodes, err := b.loadEntries(ctx, bucket, prefix, 0)
	if err != nil {
		return nil, "", false, fmt.Errorf("listing objects: %w", err)
	}
	if len(nodes) == 0 && prefix != "" {
		return nil, "", false, fmt.Errorf("directory not found: %s://%s/%s", b.scheme, bucket, prefix)
	}

	header := fmt.Sprintf("%s://%s", b.scheme, bucket)
	if prefix != "" {
		header += "/" + strings.TrimSuffix(prefix, "/")
	}
	canGoUp := prefix != "" || b.bucketListEnabled
	return nodes, header, canGoUp, nil
}

// loadEntries fetches storage entries and converts them to tree nodes.
func (b *Browser) loadEntries(ctx context.Context, bucket, prefix string, depth int) ([]*node, error) {
	entries, err := b.client.List(ctx, bucket, prefix, "/")
	if err != nil {
		return nil, err
	}

	var nodes []*node
	for _, e := range entries {
		name := strings.TrimPrefix(e.Key, prefix)
		if name == "" {
			continue
		}
		nodes = append(nodes, &node{
			entry: entry{
				name:         name,
				key:          e.Key,
				isDir:        e.IsDir,
				size:         e.Size,
				lastModified: e.LastModified,
			},
			depth: depth,
		})
	}
	return nodes, nil
}

// computeUp computes the parent bucket/prefix without modifying Browser.
func (b *Browser) computeUp(bucket, prefix string) (newBucket, newPrefix string) {
	if prefix == "" {
		if b.bucketListEnabled {
			return "", ""
		}
		return bucket, ""
	}
	// Remove trailing slash
	p := strings.TrimSuffix(prefix, "/")
	// Go to parent
	parent := path.Dir(p)
	if parent == "." {
		return bucket, ""
	}
	return bucket, parent + "/"
}

func formatSize(size int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case size >= gb:
		return fmt.Sprintf("%.1f GB", float64(size)/float64(gb))
	case size >= mb:
		return fmt.Sprintf("%.1f MB", float64(size)/float64(mb))
	case size >= kb:
		return fmt.Sprintf("%.1f KB", float64(size)/float64(kb))
	default:
		return fmt.Sprintf("%d B", size)
	}
}
