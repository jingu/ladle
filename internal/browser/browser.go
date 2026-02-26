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

	ansiCyan   = "\033[36m"
	ansiBlue   = "\033[34m"
	ansiYellow = "\033[33m"
	ansiGray   = "\033[90m"

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
	depth int    // nesting depth (0 = top level)
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
	client            storage.Client
	scheme            uri.Scheme
	bucket            string
	prefix            string
	bucketListEnabled bool
	fd                int
	in                io.Reader
	out               io.Writer
	expanded          map[string]bool               // expanded directories keyed by full key
	childCache        map[string][]storage.ListEntry // cached child entries
	heightOverride    int                            // for testing; 0 means use terminal
}

// New creates a new Browser.
func New(client storage.Client, u *uri.URI, in *os.File, out io.Writer) *Browser {
	return &Browser{
		client:            client,
		scheme:            u.Scheme,
		bucket:            u.Bucket,
		prefix:            u.Key,
		bucketListEnabled: u.IsBucketList(),
		fd:                int(in.Fd()),
		in:                in,
		out:               out,
		expanded:          make(map[string]bool),
		childCache:        make(map[string][]storage.ListEntry),
	}
}

// termHeight returns the terminal height, or a large fallback if unavailable.
func (b *Browser) termHeight() int {
	if b.heightOverride > 0 {
		return b.heightOverride
	}
	_, h, err := term.GetSize(b.fd)
	if err != nil || h <= 0 {
		return 1000 // fallback: no scrolling
	}
	return h
}

// Run starts the interactive browser loop. It returns the user's selection.
func (b *Browser) Run(ctx context.Context) (*Selection, error) {
	oldState, err := term.MakeRaw(b.fd)
	if err != nil {
		return nil, fmt.Errorf("setting terminal raw mode: %w", err)
	}
	defer func() { _ = term.Restore(b.fd, oldState) }()

	_, _ = fmt.Fprint(b.out, ansiAltScreenOn+ansiHideCursor)
	defer func() { _, _ = fmt.Fprint(b.out, ansiShowCursor+ansiAltScreenOff) }()

	return b.runLoop(ctx)
}

// runLoop contains the main browser loop, separated from terminal setup for testability.
func (b *Browser) runLoop(ctx context.Context) (*Selection, error) {
	cursor := 0
	for {
		// Bucket list mode
		if b.bucket == "" {
			sel, done, err := b.runBucketList(ctx, &cursor)
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

		items := b.buildItems(entries)
		if cursor >= len(items) {
			cursor = len(items) - 1
		}

		idx, quit, currentItems := b.handleInput(ctx, items, cursor)
		if quit {
			return &Selection{Action: ActionQuit}, nil
		}

		sel := currentItems[idx]
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
			b.expanded = make(map[string]bool)
			b.childCache = make(map[string][]storage.ListEntry)
			cursor = 0
			continue
		}

		raw := fmt.Sprintf("%s://%s/%s", b.scheme, b.bucket, sel.entry.Key)
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

func (b *Browser) buildItems(entries []storage.ListEntry) []item {
	var items []item
	b.appendTreeItems(&items, entries, b.prefix, 0)
	if b.prefix != "" || b.bucketListEnabled {
		items = append(items, item{label: "..", isNav: true, navID: "up"})
	}
	items = append(items, item{label: "quit", isNav: true, navID: "quit"})
	return items
}

// appendTreeItems recursively builds the flat item list with depth, expanding directories as needed.
func (b *Browser) appendTreeItems(items *[]item, entries []storage.ListEntry, prefix string, depth int) {
	for _, e := range entries {
		name := strings.TrimPrefix(e.Key, prefix)
		if name == "" {
			continue
		}
		*items = append(*items, item{
			label: name,
			entry: e,
			isDir: e.IsDir,
			depth: depth,
		})
		if e.IsDir && b.expanded[e.Key] {
			if children, ok := b.childCache[e.Key]; ok {
				b.appendTreeItems(items, children, e.Key, depth+1)
			}
		}
	}
}

// handleInput renders the item list and blocks until the user makes a selection.
// It returns the selected index (into the returned items slice), whether the user
// explicitly quit, and the current items slice (which may have been rebuilt by
// tree expansion/collapse).
func (b *Browser) handleInput(ctx context.Context, items []item, cursor int) (int, bool, []item) {
	filter := ""
	filtering := false
	visible := allIndices(len(items))
	scrollOffset := 0

	if cursor >= len(visible) {
		cursor = len(visible) - 1
	}
	if cursor < 0 {
		cursor = 0
	}

	b.renderFiltered(items, visible, cursor, filter, filtering, &scrollOffset)

	buf := make([]byte, 3)
	for {
		n, err := b.in.Read(buf)
		if err != nil {
			return 0, true, items
		}

		if filtering {
			changed := false
			if n == 1 {
				switch buf[0] {
				case 27: // Escape — clear filter and exit filter mode
					filter = ""
					filtering = false
					changed = true
				case 13: // Enter — exit filter mode, keep filter
					filtering = false
				case 127, 8: // Backspace / Ctrl+H
					if len(filter) > 0 {
						filter = filter[:len(filter)-1]
						changed = true
					}
				default:
					if buf[0] >= 32 && buf[0] < 127 {
						filter += string(buf[0])
						changed = true
					}
				}
			} else if n == 3 && buf[0] == 27 && buf[1] == '[' {
				// Arrow keys work in filter mode too
				switch buf[2] {
				case 'A':
					if cursor > 0 {
						cursor--
					}
				case 'B':
					if cursor < len(visible)-1 {
						cursor++
					}
				}
			}
			if changed {
				visible = filterIndices(items, filter)
				cursor = 0
				scrollOffset = 0
			}
		} else {
			if n == 1 {
				switch buf[0] {
				case 3: // Ctrl+C
					return 0, true, items
				case 'q', 'Q':
					return 0, true, items
				case 27: // Escape — clear active filter
					if filter != "" {
						filter = ""
						visible = allIndices(len(items))
						cursor = 0
						scrollOffset = 0
					}
				case 13: // Enter
					if len(visible) == 0 {
						continue
					}
					return visible[cursor], false, items
				case 'j', 14: // j / Ctrl+N — down
					if cursor < len(visible)-1 {
						cursor++
					}
				case 'k', 16: // k / Ctrl+P — up
					if cursor > 0 {
						cursor--
					}
				case 6: // Ctrl+F — expand directory
					items, visible, cursor = b.expandDir(ctx, items, visible, cursor, filter)
				case 2: // Ctrl+B — collapse or move to parent
					items, visible, cursor = b.collapseDir(ctx, items, visible, cursor, filter)
				case '/':
					filtering = true
				}
			} else if n == 3 && buf[0] == 27 && buf[1] == '[' {
				switch buf[2] {
				case 'A': // Up arrow
					if cursor > 0 {
						cursor--
					}
				case 'B': // Down arrow
					if cursor < len(visible)-1 {
						cursor++
					}
				case 'C': // Right arrow — expand directory
					items, visible, cursor = b.expandDir(ctx, items, visible, cursor, filter)
				case 'D': // Left arrow — collapse or move to parent
					items, visible, cursor = b.collapseDir(ctx, items, visible, cursor, filter)
				}
			}
		}

		// Clamp cursor
		if len(visible) == 0 {
			cursor = 0
		} else if cursor >= len(visible) {
			cursor = len(visible) - 1
		}

		b.renderFiltered(items, visible, cursor, filter, filtering, &scrollOffset)
	}
}

// expandDir expands the directory at the current cursor position.
func (b *Browser) expandDir(ctx context.Context, items []item, visible []int, cursor int, filter string) ([]item, []int, int) {
	if len(visible) == 0 {
		return items, visible, cursor
	}
	it := items[visible[cursor]]
	if it.isDir && !it.isNav && it.entry.Key != "" && !b.expanded[it.entry.Key] {
		children, err := b.client.List(ctx, b.bucket, it.entry.Key, "/")
		if err == nil {
			b.childCache[it.entry.Key] = children
			b.expanded[it.entry.Key] = true
			items = b.rebuildItems(ctx, items)
			visible = filterIndices(items, filter)
			if cursor >= len(visible) {
				cursor = len(visible) - 1
			}
		}
	}
	return items, visible, cursor
}

// collapseDir collapses the directory at the current cursor position, or moves to its parent.
func (b *Browser) collapseDir(ctx context.Context, items []item, visible []int, cursor int, filter string) ([]item, []int, int) {
	if len(visible) == 0 {
		return items, visible, cursor
	}
	it := items[visible[cursor]]
	if it.isDir && !it.isNav && b.expanded[it.entry.Key] {
		delete(b.expanded, it.entry.Key)
		items = b.rebuildItems(ctx, items)
		visible = filterIndices(items, filter)
		if cursor >= len(visible) {
			cursor = len(visible) - 1
		}
	} else if it.depth > 0 {
		cursor = b.findParentIndex(items, visible, cursor)
	}
	return items, visible, cursor
}

// rebuildItems rebuilds the flat item list from current entries, using the current expansion state.
// On error, it returns fallback unchanged.
func (b *Browser) rebuildItems(ctx context.Context, fallback []item) []item {
	entries, err := b.client.List(ctx, b.bucket, b.prefix, "/")
	if err != nil {
		return fallback
	}
	return b.buildItems(entries)
}

// findParentIndex returns the visible cursor position of the parent directory for the current item.
func (b *Browser) findParentIndex(items []item, visible []int, cursor int) int {
	currentDepth := items[visible[cursor]].depth
	for i := cursor - 1; i >= 0; i-- {
		it := items[visible[i]]
		if it.isDir && !it.isNav && it.depth < currentDepth {
			return i
		}
	}
	return cursor
}

func allIndices(n int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	return idx
}

func filterIndices(items []item, filter string) []int {
	if filter == "" {
		return allIndices(len(items))
	}
	lower := strings.ToLower(filter)
	var idx []int
	for i, it := range items {
		if it.isNav || strings.Contains(strings.ToLower(it.label), lower) {
			idx = append(idx, i)
		}
	}
	return idx
}

// w writes a formatted string to the browser output, discarding errors.
func (b *Browser) w(format string, a ...any) {
	_, _ = fmt.Fprintf(b.out, format, a...)
}

// headerLines returns the number of fixed lines used by header, filter bar, and blank line.
func headerLines(filtering bool, filter string) int {
	// header: 2 lines (blank + URI)
	n := 2
	if filtering || filter != "" {
		n++ // filter bar
	}
	n++ // blank line after header
	return n
}

func (b *Browser) renderFiltered(items []item, visible []int, cursor int, filter string, filtering bool, scrollOffset *int) {
	b.w("%s%s", ansiHome, ansiClearScreen)

	// Header
	if b.bucket == "" {
		b.w("\r\n  %s%s%s://%s\r\n", ansiBold, ansiCyan, b.scheme, ansiReset)
	} else {
		b.w("\r\n  %s%s%s://%s/%s%s\r\n", ansiBold, ansiCyan, b.scheme, b.bucket, b.prefix, ansiReset)
	}

	// Filter bar
	if filtering {
		b.w("  %s/%s %s%s\u2588%s\r\n", ansiYellow, ansiReset, filter, ansiDim, ansiReset)
	} else if filter != "" {
		b.w("  %s/ %s%s\r\n", ansiDim, filter, ansiReset)
	}

	b.w("\r\n")

	// Check if there are any non-nav visible items
	hasContent := false
	for _, vi := range visible {
		if !items[vi].isNav {
			hasContent = true
			break
		}
	}
	if !hasContent {
		if filter != "" {
			b.w("    %s(no matches)%s\r\n", ansiDim, ansiReset)
		} else {
			b.w("    %s(empty)%s\r\n", ansiDim, ansiReset)
		}
	}

	// Calculate viewport
	// Fixed overhead: header area + help (2 lines: blank + text)
	overhead := headerLines(filtering, filter) + 2
	if !hasContent {
		overhead++ // empty/no-matches line
	}
	maxRows := b.termHeight() - overhead
	if maxRows < 1 {
		maxRows = 1
	}

	total := len(visible)

	// First pass: reserve 1 line for bottom indicator if scrolling is needed
	itemRows := maxRows
	if total > maxRows {
		itemRows = maxRows - 1 // at minimum we need "more below"
		if itemRows < 1 {
			itemRows = 1
		}
	}

	// Adjust scrollOffset to keep cursor visible
	if cursor < *scrollOffset {
		*scrollOffset = cursor
	}
	if cursor >= *scrollOffset+itemRows {
		*scrollOffset = cursor - itemRows + 1
	}
	if *scrollOffset > total-itemRows {
		*scrollOffset = total - itemRows
	}
	if *scrollOffset < 0 {
		*scrollOffset = 0
	}

	// Second pass: if "more above" indicator is needed, reserve one more line
	if *scrollOffset > 0 && itemRows > 1 {
		itemRows--
		// Re-adjust scrollOffset with reduced itemRows
		if cursor >= *scrollOffset+itemRows {
			*scrollOffset = cursor - itemRows + 1
		}
	}

	end := *scrollOffset + itemRows
	if end > total {
		end = total
	}

	hasAbove := *scrollOffset > 0
	hasBelow := end < total

	// Compute tree prefixes for ALL visible items (needed for correct │ lines)
	prefixes := computeTreePrefixes(items, visible)

	if hasAbove {
		b.w("    %s(%d more above)%s\r\n", ansiDim, *scrollOffset, ansiReset)
	}

	for i := *scrollOffset; i < end; i++ {
		vi := visible[i]
		it := items[vi]
		selected := i == cursor
		if it.isNav {
			b.renderNav(it, selected)
		} else if it.isDir {
			b.renderDir(it, selected, prefixes[i])
		} else {
			b.renderFile(it, selected, prefixes[i])
		}
	}

	if hasBelow {
		b.w("    %s(%d more below)%s\r\n", ansiDim, total-end, ansiReset)
	}

	// Help line
	if filtering {
		b.w("\r\n  %stype to filter    Esc clear    Enter done%s\r\n", ansiDim, ansiReset)
	} else {
		b.w("\r\n  %s\u2191\u2193/jk/C-n,p navigate    \u2190\u2192/C-f,b expand/collapse    Enter select    / filter    q quit%s\r\n", ansiDim, ansiReset)
	}
}

func (b *Browser) renderNav(it item, selected bool) {
	if selected {
		b.w("  %s \u25b8 %s %s\r\n", ansiReverse, it.label, ansiReset)
	} else {
		b.w("    %s%s%s\r\n", ansiGray, it.label, ansiReset)
	}
}

func (b *Browser) renderDir(it item, selected bool, prefix string) {
	if selected {
		b.w("    %s%s%s%s\r\n", prefix, ansiReverse, it.label, ansiReset)
	} else {
		b.w("    %s%s%s%s%s\r\n", prefix, ansiBold, ansiBlue, it.label, ansiReset)
	}
}

func (b *Browser) renderFile(it item, selected bool, prefix string) {
	size := formatSize(it.entry.Size)
	if selected {
		b.w("    %s%s%-30s  %s%s\r\n", prefix, ansiReverse, it.label, size, ansiReset)
	} else {
		b.w("    %s%-30s  %s%s%s\r\n", prefix, it.label, ansiDim, size, ansiReset)
	}
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

// runBucketList displays a list of buckets using the cursor-based UI.
// Returns (selection, done, error). done=true means the caller should return.
func (b *Browser) runBucketList(ctx context.Context, cursor *int) (*Selection, bool, error) {
	buckets, err := b.client.ListBuckets(ctx)
	if err != nil {
		return nil, true, fmt.Errorf("listing buckets: %w", err)
	}

	var items []item
	for _, name := range buckets {
		items = append(items, item{
			label: name,
			isDir: true,
		})
	}
	items = append(items, item{label: "quit", isNav: true, navID: "quit"})

	if *cursor >= len(items) {
		*cursor = len(items) - 1
	}

	idx, quit, currentItems := b.handleInput(ctx, items, *cursor)
	if quit {
		return &Selection{Action: ActionQuit}, true, nil
	}

	sel := currentItems[idx]
	if sel.isNav && sel.navID == "quit" {
		return &Selection{Action: ActionQuit}, true, nil
	}

	b.bucket = sel.label
	b.prefix = ""
	*cursor = 0
	return nil, false, nil
}

// computeTreePrefixes returns a tree-line prefix string for each visible item.
// Nav items get an empty prefix. Directory/file items get box-drawing characters
// similar to the `tree` command (├──, └──, │).
func computeTreePrefixes(items []item, visible []int) []string {
	prefixes := make([]string, len(visible))
	continueLine := make(map[int]bool) // whether depth d has more siblings below

	for i, vi := range visible {
		it := items[vi]
		if it.isNav {
			prefixes[i] = ""
			continue
		}

		// Determine if this is the last sibling at its depth among remaining visible items.
		isLast := true
		for j := i + 1; j < len(visible); j++ {
			peer := items[visible[j]]
			if peer.isNav {
				continue
			}
			if peer.depth < it.depth {
				break // went up a level — no more siblings
			}
			if peer.depth == it.depth {
				isLast = false
				break
			}
		}

		var b strings.Builder
		// Ancestor levels: draw "│   " or "    "
		for d := 0; d < it.depth; d++ {
			if continueLine[d] {
				b.WriteString("│   ")
			} else {
				b.WriteString("    ")
			}
		}
		// Current level connector
		if isLast {
			b.WriteString("└── ")
		} else {
			b.WriteString("├── ")
		}

		// Update continueLine for this depth
		continueLine[it.depth] = !isLast
		// Clear deeper levels (they are no longer valid)
		for d := range continueLine {
			if d > it.depth {
				delete(continueLine, d)
			}
		}

		prefixes[i] = b.String()
	}
	return prefixes
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
