// Package browser implements the interactive file browser for cloud storage.
package browser

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/jingu/ladle/internal/storage"
	"github.com/jingu/ladle/internal/uri"
)

// Action represents what the user wants to do after selecting an item.
type Action int

const (
	ActionEdit Action = iota
	ActionBrowse
	ActionQuit
)

// Selection holds the user's selection in the browser.
type Selection struct {
	Action Action
	URI    *uri.URI
}

// Browser provides an interactive file browser.
type Browser struct {
	client            storage.Client
	scheme            uri.Scheme
	bucket            string
	prefix            string
	bucketListEnabled bool
	in                io.Reader
	out               io.Writer
}

// New creates a new Browser.
func New(client storage.Client, u *uri.URI, in io.Reader, out io.Writer) *Browser {
	return &Browser{
		client:            client,
		scheme:            u.Scheme,
		bucket:            u.Bucket,
		prefix:            u.Key,
		bucketListEnabled: u.IsBucketList(),
		in:                in,
		out:               out,
	}
}

// Run starts the interactive browser loop. It returns the URI of a file
// selected for editing, or nil if the user quit.
func (b *Browser) Run(ctx context.Context) (*Selection, error) {
	for {
		// Bucket list mode
		if b.bucket == "" {
			sel, done, err := b.runBucketList(ctx)
			if err != nil {
				return nil, err
			}
			if done {
				return sel, nil
			}
			continue
		}

		entries, err := b.client.List(ctx, b.bucket, b.prefix, "/")
		if err != nil {
			return nil, fmt.Errorf("listing objects: %w", err)
		}

		if len(entries) == 0 {
			fmt.Fprintf(b.out, "  (empty)\n")
		}

		b.printHeader()
		for i, e := range entries {
			name := e.Key
			// Strip prefix to show relative names
			name = strings.TrimPrefix(name, b.prefix)
			if name == "" {
				continue
			}
			if e.IsDir {
				fmt.Fprintf(b.out, "  [%d] %s\n", i+1, name)
			} else {
				fmt.Fprintf(b.out, "  [%d] %s  (%s)\n", i+1, name, formatSize(e.Size))
			}
		}
		fmt.Fprintf(b.out, "\n")

		// Show navigation options
		if b.prefix != "" || b.bucketListEnabled {
			fmt.Fprintf(b.out, "  [..] Go up\n")
		}
		fmt.Fprintf(b.out, "  [q]  Quit\n\n")
		fmt.Fprintf(b.out, "Select: ")

		var input string
		if _, err := fmt.Fscan(b.in, &input); err != nil {
			return &Selection{Action: ActionQuit}, nil
		}
		input = strings.TrimSpace(input)

		switch input {
		case "q", "Q":
			return &Selection{Action: ActionQuit}, nil
		case "..":
			b.goUp()
			continue
		}

		var idx int
		if _, err := fmt.Sscanf(input, "%d", &idx); err != nil || idx < 1 || idx > len(entries) {
			fmt.Fprintf(b.out, "Invalid selection.\n\n")
			continue
		}

		entry := entries[idx-1]
		if entry.IsDir {
			b.prefix = entry.Key
			continue
		}

		// File selected
		raw := fmt.Sprintf("%s://%s/%s", b.scheme, b.bucket, entry.Key)
		u, err := uri.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("parsing selected URI: %w", err)
		}
		return &Selection{
			Action: ActionEdit,
			URI:    u,
		}, nil
	}
}

func (b *Browser) printHeader() {
	if b.bucket == "" {
		fmt.Fprintf(b.out, "\n%s://\n\n", b.scheme)
		return
	}
	p := b.prefix
	if p == "" {
		p = "/"
	}
	fmt.Fprintf(b.out, "\n%s://%s/%s\n\n", b.scheme, b.bucket, p)
}

func (b *Browser) goUp() {
	if b.prefix == "" {
		if b.bucketListEnabled {
			b.bucket = ""
		}
		return
	}
	// Remove trailing slash
	p := strings.TrimSuffix(b.prefix, "/")
	// Go to parent
	parent := path.Dir(p)
	if parent == "." {
		b.prefix = ""
	} else {
		b.prefix = parent + "/"
	}
}

// runBucketList displays a list of buckets for the user to select.
// Returns (selection, done, error). done=true means the caller should return.
func (b *Browser) runBucketList(ctx context.Context) (*Selection, bool, error) {
	buckets, err := b.client.ListBuckets(ctx)
	if err != nil {
		return nil, true, fmt.Errorf("listing buckets: %w", err)
	}

	b.printHeader()
	if len(buckets) == 0 {
		fmt.Fprintf(b.out, "  (no buckets)\n")
	}
	for i, name := range buckets {
		fmt.Fprintf(b.out, "  [%d] %s\n", i+1, name)
	}
	fmt.Fprintf(b.out, "\n")
	fmt.Fprintf(b.out, "  [q]  Quit\n\n")
	fmt.Fprintf(b.out, "Select: ")

	var input string
	if _, err := fmt.Fscan(b.in, &input); err != nil {
		return &Selection{Action: ActionQuit}, true, nil
	}
	input = strings.TrimSpace(input)

	if input == "q" || input == "Q" {
		return &Selection{Action: ActionQuit}, true, nil
	}

	var idx int
	if _, err := fmt.Sscanf(input, "%d", &idx); err != nil || idx < 1 || idx > len(buckets) {
		fmt.Fprintf(b.out, "Invalid selection.\n\n")
		return nil, false, nil
	}

	b.bucket = buckets[idx-1]
	b.prefix = ""
	return nil, false, nil
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
