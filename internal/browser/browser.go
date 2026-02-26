// Package browser implements the interactive file browser for cloud storage.
package browser

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/jingu/ladle/internal/storage"
	"github.com/jingu/ladle/internal/uri"
	"golang.org/x/term"
)

// ANSI escape codes for colors and cursor control.
const (
	ansiReset   = "\033[0m"
	ansiBold    = "\033[1m"
	ansiDim     = "\033[2m"
	ansiReverse = "\033[7m"

	ansiCyan = "\033[36m"
	ansiBlue = "\033[34m"
	ansiGray = "\033[90m"

	ansiClearScreen  = "\033[2J"
	ansiHome         = "\033[H"
	ansiHideCursor   = "\033[?25l"
	ansiShowCursor   = "\033[?25h"
	ansiAltScreenOn  = "\033[?1049h"
	ansiAltScreenOff = "\033[?1049l"
)

// item represents a selectable row in the browser.
type item struct {
	label string
	entry storage.ListEntry
	isDir bool
	isNav bool
	navID string // "up" or "quit"
}

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

// Browser provides an interactive file browser with cursor-based selection.
type Browser struct {
	client storage.Client
	scheme uri.Scheme
	bucket string
	prefix string
	fd     int
	in     *os.File
	out    io.Writer
}

// New creates a new Browser.
func New(client storage.Client, u *uri.URI, in *os.File, out io.Writer) *Browser {
	return &Browser{
		client: client,
		scheme: u.Scheme,
		bucket: u.Bucket,
		prefix: u.Key,
		fd:     int(in.Fd()),
		in:     in,
		out:    out,
	}
}

// Run starts the interactive browser loop. It returns the user's selection.
func (b *Browser) Run(ctx context.Context) (*Selection, error) {
	oldState, err := term.MakeRaw(b.fd)
	if err != nil {
		return nil, fmt.Errorf("setting terminal raw mode: %w", err)
	}
	defer term.Restore(b.fd, oldState)

	fmt.Fprint(b.out, ansiAltScreenOn+ansiHideCursor)
	defer fmt.Fprint(b.out, ansiShowCursor+ansiAltScreenOff)

	cursor := 0
	for {
		entries, err := b.client.List(ctx, b.bucket, b.prefix, "/")
		if err != nil {
			return nil, fmt.Errorf("listing objects: %w", err)
		}

		items := b.buildItems(entries)
		if len(items) == 0 {
			return &Selection{Action: ActionQuit}, nil
		}

		if cursor >= len(items) {
			cursor = len(items) - 1
		}

		idx, quit := b.handleInput(items, cursor)
		if quit {
			return &Selection{Action: ActionQuit}, nil
		}

		sel := items[idx]
		if sel.isNav {
			if sel.navID == "quit" {
				return &Selection{Action: ActionQuit}, nil
			}
			if sel.navID == "up" {
				b.goUp()
				cursor = 0
				continue
			}
		}

		if sel.isDir {
			b.prefix = sel.entry.Key
			cursor = 0
			continue
		}

		raw := fmt.Sprintf("%s://%s/%s", b.scheme, b.bucket, sel.entry.Key)
		u, _ := uri.Parse(raw)
		return &Selection{
			Action: ActionEdit,
			URI:    u,
		}, nil
	}
}

func (b *Browser) buildItems(entries []storage.ListEntry) []item {
	var items []item
	for _, e := range entries {
		name := strings.TrimPrefix(e.Key, b.prefix)
		if name == "" {
			continue
		}
		items = append(items, item{
			label: name,
			entry: e,
			isDir: e.IsDir,
		})
	}
	if b.prefix != "" {
		items = append(items, item{label: "..", isNav: true, navID: "up"})
	}
	items = append(items, item{label: "quit", isNav: true, navID: "quit"})
	return items
}

// handleInput renders the item list and blocks until the user makes a selection.
// It returns the selected index and whether the user explicitly quit (q/Ctrl+C).
func (b *Browser) handleInput(items []item, cursor int) (int, bool) {
	b.render(items, cursor)

	buf := make([]byte, 3)
	for {
		n, err := b.in.Read(buf)
		if err != nil {
			return 0, true
		}

		if n == 1 {
			switch buf[0] {
			case 'q', 'Q', 3: // q, Q, Ctrl+C
				return 0, true
			case 13: // Enter
				return cursor, false
			case 'j':
				if cursor < len(items)-1 {
					cursor++
				}
			case 'k':
				if cursor > 0 {
					cursor--
				}
			}
		} else if n == 3 && buf[0] == 27 && buf[1] == '[' {
			switch buf[2] {
			case 'A': // Up arrow
				if cursor > 0 {
					cursor--
				}
			case 'B': // Down arrow
				if cursor < len(items)-1 {
					cursor++
				}
			}
		}

		b.render(items, cursor)
	}
}

func (b *Browser) render(items []item, cursor int) {
	fmt.Fprint(b.out, ansiHome+ansiClearScreen)

	// Header
	p := b.prefix
	if p == "" {
		p = "/"
	}
	fmt.Fprintf(b.out, "\r\n  %s%s%s://%s/%s%s\r\n\r\n",
		ansiBold, ansiCyan, b.scheme, b.bucket, p, ansiReset)

	// Check if there are any non-nav items
	hasContent := false
	for _, it := range items {
		if !it.isNav {
			hasContent = true
			break
		}
	}
	if !hasContent {
		fmt.Fprintf(b.out, "    %s(empty)%s\r\n\r\n", ansiDim, ansiReset)
	}

	for i, it := range items {
		selected := i == cursor
		if it.isNav {
			b.renderNav(it, selected)
		} else if it.isDir {
			b.renderDir(it, selected)
		} else {
			b.renderFile(it, selected)
		}
	}

	// Help line
	fmt.Fprintf(b.out, "\r\n  %s%s  navigate    Enter  select    q  quit%s\r\n",
		ansiDim, "\u2191\u2193/jk", ansiReset)
}

func (b *Browser) renderNav(it item, selected bool) {
	if selected {
		fmt.Fprintf(b.out, "  %s \u25b8 %s %s\r\n", ansiReverse, it.label, ansiReset)
	} else {
		fmt.Fprintf(b.out, "    %s%s%s\r\n", ansiGray, it.label, ansiReset)
	}
}

func (b *Browser) renderDir(it item, selected bool) {
	if selected {
		fmt.Fprintf(b.out, "  %s \u25b8 %s %s\r\n", ansiReverse, it.label, ansiReset)
	} else {
		fmt.Fprintf(b.out, "    %s%s%s%s\r\n", ansiBold, ansiBlue, it.label, ansiReset)
	}
}

func (b *Browser) renderFile(it item, selected bool) {
	size := formatSize(it.entry.Size)
	if selected {
		fmt.Fprintf(b.out, "  %s \u25b8 %-30s  %s %s\r\n", ansiReverse, it.label, size, ansiReset)
	} else {
		fmt.Fprintf(b.out, "    %-30s  %s%s%s\r\n", it.label, ansiDim, size, ansiReset)
	}
}

func (b *Browser) goUp() {
	if b.prefix == "" {
		return
	}
	p := strings.TrimSuffix(b.prefix, "/")
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
