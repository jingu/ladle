// Package browser implements the interactive file browser for cloud storage.
package browser

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/jingu/ladle/internal/spinner"
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
	client storage.Client
	scheme uri.Scheme
	bucket string
	prefix string
	in     io.Reader
	out    io.Writer
}

// New creates a new Browser.
func New(client storage.Client, u *uri.URI, in io.Reader, out io.Writer) *Browser {
	return &Browser{
		client: client,
		scheme: u.Scheme,
		bucket: u.Bucket,
		prefix: u.Key,
		in:     in,
		out:    out,
	}
}

// Run starts the interactive browser loop. It returns the URI of a file
// selected for editing, or nil if the user quit.
func (b *Browser) Run(ctx context.Context) (*Selection, error) {
	for {
		sp := spinner.New(b.out, "Loading ...")
		sp.Start()
		entries, err := b.client.List(ctx, b.bucket, b.prefix, "/")
		if err != nil {
			sp.Stop()
			return nil, fmt.Errorf("listing objects: %w", err)
		}
		sp.Stop()

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
		if b.prefix != "" {
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
		u, _ := uri.Parse(raw)
		return &Selection{
			Action: ActionEdit,
			URI:    u,
		}, nil
	}
}

func (b *Browser) printHeader() {
	p := b.prefix
	if p == "" {
		p = "/"
	}
	fmt.Fprintf(b.out, "\n%s://%s/%s\n\n", b.scheme, b.bucket, p)
}

func (b *Browser) goUp() {
	if b.prefix == "" {
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
