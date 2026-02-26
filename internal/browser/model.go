package browser

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jingu/ladle/internal/storage"
	"github.com/jingu/ladle/internal/uri"
)

// entry represents a single item in the tree.
type entry struct {
	name         string
	key          string
	isDir        bool
	isBucket     bool
	size         int64
	lastModified time.Time
}

// node represents a tree node.
type node struct {
	entry    entry
	depth    int
	expanded bool
	loaded   bool
	children []*node
}

// childrenLoadedMsg carries the loaded children back to Update.
type childrenLoadedMsg struct {
	parentKey string // identifies which node to populate
	children  []*node
	err       error
}

// editDoneMsg is sent after an edit operation completes.
type editDoneMsg struct {
	err error
}

// navigatedMsg is sent after navigation (goUp / bucket select) rebuilds the view.
type navigatedMsg struct {
	nodes   []*node
	header  string
	canGoUp bool
	err     error
}

// editCommand implements tea.ExecCommand to run the edit workflow
// while the TUI is suspended.
type editCommand struct {
	editFn EditFunc
	uri    *uri.URI
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func (c *editCommand) Run() error           { return c.editFn(c.uri) }
func (c *editCommand) SetStdin(r io.Reader)  { c.stdin = r }
func (c *editCommand) SetStdout(w io.Writer) { c.stdout = w }
func (c *editCommand) SetStderr(w io.Writer) { c.stderr = w }

// spinnerFrames defines the animation frames for the loading spinner.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// tickMsg advances the spinner animation.
type tickMsg struct{}

// model is the bubbletea Model for the browser.
type model struct {
	nodes   []*node
	cursor  int
	header  string
	version string

	canGoUp      bool      // whether backspace/.. should go up
	termHeight   int       // terminal height for scrolling
	message      string    // status message (e.g. error from last action)
	lastEscTime  time.Time // for double-Esc quit

	filtering  bool
	filterText string

	quitting     bool
	loading      bool
	spinnerFrame int

	client  storage.Client
	ctx     context.Context // stored because bubbletea Cmd closures need it
	bucket  string
	scheme  string
	browser *Browser
	editFn  EditFunc
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termHeight = msg.Height
		return m, nil
	case tea.KeyMsg:
		m.message = "" // clear message on any key
		return m.handleKey(msg)
	case childrenLoadedMsg:
		return m.handleChildrenLoaded(msg)
	case editDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.message = msg.err.Error()
		}
		return m, nil
	case tickMsg:
		if m.loading {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
			return m, tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
		}
		return m, nil
	case navigatedMsg:
		m.loading = false
		if msg.err != nil {
			m.message = msg.err.Error()
			return m, nil
		}
		m.nodes = msg.nodes
		m.header = msg.header
		m.canGoUp = msg.canGoUp
		m.cursor = 0
		return m, nil
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Filter mode handling
	if m.filtering {
		return m.handleFilterKey(msg)
	}

	// Esc handling: double-Esc to quit
	if msg.Type == tea.KeyEscape {
		now := time.Now()
		if !m.lastEscTime.IsZero() && now.Sub(m.lastEscTime) < 500*time.Millisecond {
			m.quitting = true
			return m, tea.Quit
		}
		m.lastEscTime = now
		return m, nil
	}
	m.lastEscTime = time.Time{} // reset on non-Esc key

	if m.loading {
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}

	visible := m.visibleNodes()

	switch msg.String() {
	case "up", "k", "ctrl+p":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j", "ctrl+n":
		if m.cursor < len(visible)-1 {
			m.cursor++
		}
	case "enter":
		if len(visible) == 0 {
			break
		}
		n := visible[m.cursor]
		if n == nil {
			// ".." node — navigate up
			return m, m.navigateUp()
		}
		if n.entry.isBucket {
			// Select bucket — navigate into it
			return m, m.navigateToBucket(n.entry.name)
		}
		if n.entry.isDir {
			if !n.loaded {
				return m, tea.Batch(m.startLoading(), m.loadChildren(n))
			}
			n.expanded = !n.expanded
			if newVisible := m.visibleNodes(); m.cursor >= len(newVisible) {
				m.cursor = len(newVisible) - 1
			}
		} else {
			// File selected — exec edit with TUI suspended
			cmd, err := m.execEdit(n.entry.key)
			if err != nil {
				m.message = err.Error()
				return m, nil
			}
			return m, cmd
		}
	case "right", "l", "ctrl+f":
		if len(visible) > 0 {
			n := visible[m.cursor]
			if n != nil && n.entry.isDir {
				if !n.loaded {
					return m, tea.Batch(m.startLoading(), m.loadChildren(n))
				}
				if !n.expanded {
					n.expanded = true
				}
			}
		}
	case "left", "h", "ctrl+b":
		if len(visible) > 0 {
			n := visible[m.cursor]
			if n != nil && n.entry.isDir && n.expanded {
				n.expanded = false
				if newVisible := m.visibleNodes(); m.cursor >= len(newVisible) {
					m.cursor = len(newVisible) - 1
				}
			} else if n != nil && n.depth > 0 {
				if parent, idx := m.findParent(visible, m.cursor); parent != nil {
					parent.expanded = false
					m.cursor = idx
				}
			}
		}
	case "-":
		if m.canGoUp {
			return m, m.navigateUp()
		}
	case "/":
		m.filtering = true
		m.filterText = ""
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.filtering = false
		m.filterText = ""
		m.cursor = 0
		return m, nil
	case tea.KeyEnter:
		m.filtering = false
		// keep filterText, clamp cursor
		if visible := m.visibleNodes(); m.cursor >= len(visible) {
			if len(visible) > 0 {
				m.cursor = len(visible) - 1
			} else {
				m.cursor = 0
			}
		}
		return m, nil
	case tea.KeyBackspace:
		if len(m.filterText) > 0 {
			m.filterText = m.filterText[:len(m.filterText)-1]
		}
		if m.filterText == "" {
			m.filtering = false
			m.cursor = 0
		} else {
			// clamp cursor after filter change
			if visible := m.visibleNodes(); m.cursor >= len(visible) && len(visible) > 0 {
				m.cursor = len(visible) - 1
			}
		}
		return m, nil
	case tea.KeyUp, tea.KeyDown:
		visible := m.visibleNodes()
		if msg.Type == tea.KeyUp && m.cursor > 0 {
			m.cursor--
		}
		if msg.Type == tea.KeyDown && m.cursor < len(visible)-1 {
			m.cursor++
		}
		return m, nil
	case tea.KeyCtrlP:
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case tea.KeyCtrlN:
		visible := m.visibleNodes()
		if m.cursor < len(visible)-1 {
			m.cursor++
		}
		return m, nil
	case tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit
	case tea.KeyRunes:
		m.filterText += string(msg.Runes)
		m.cursor = 0 // reset cursor on new filter input
		return m, nil
	}
	return m, nil
}

// execEdit returns a tea.Exec command that suspends the TUI and runs the edit.
func (m model) execEdit(key string) (tea.Cmd, error) {
	raw := fmt.Sprintf("%s://%s/%s", m.scheme, m.bucket, key)
	u, err := uri.Parse(raw)
	if err != nil {
		return nil, err
	}
	cmd := &editCommand{editFn: m.editFn, uri: u}
	return tea.Exec(cmd, func(err error) tea.Msg {
		return editDoneMsg{err: err}
	}), nil
}

// navigateUp triggers goUp on the browser and rebuilds the view.
func (m model) navigateUp() tea.Cmd {
	b := m.browser
	ctx := m.ctx
	return func() tea.Msg {
		b.goUp()
		nodes, header, canGoUp, err := b.buildView(ctx)
		return navigatedMsg{nodes: nodes, header: header, canGoUp: canGoUp, err: err}
	}
}

// navigateToBucket sets the bucket and rebuilds the view.
func (m model) navigateToBucket(bucket string) tea.Cmd {
	b := m.browser
	ctx := m.ctx
	return func() tea.Msg {
		b.bucket = bucket
		b.prefix = ""
		nodes, header, canGoUp, err := b.buildView(ctx)
		return navigatedMsg{nodes: nodes, header: header, canGoUp: canGoUp, err: err}
	}
}

func (m model) handleChildrenLoaded(msg childrenLoadedMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.message = msg.err.Error()
		return m, nil
	}
	for _, n := range m.allNodes() {
		if n.entry.key == msg.parentKey && n.entry.isDir {
			n.children = msg.children
			n.loaded = true
			n.expanded = true
			break
		}
	}
	return m, nil
}

// allNodes returns all nodes in the tree (recursive).
func (m model) allNodes() []*node {
	var all []*node
	var walk func(nodes []*node)
	walk = func(nodes []*node) {
		for _, n := range nodes {
			all = append(all, n)
			walk(n.children)
		}
	}
	walk(m.nodes)
	return all
}

// visibleNodes returns a flat list of visible nodes.
// nil entries represent the ".." go-up item.
func (m model) visibleNodes() []*node {
	filter := strings.ToLower(m.filterText)

	var visible []*node
	var walk func(nodes []*node) bool
	walk = func(nodes []*node) bool {
		anyAdded := false
		for _, n := range nodes {
			if filter == "" {
				visible = append(visible, n)
				if n.expanded {
					walk(n.children)
				}
				anyAdded = true
				continue
			}
			nameMatch := strings.Contains(strings.ToLower(n.entry.name), filter)
			if n.entry.isDir && n.expanded {
				pos := len(visible)
				visible = append(visible, n)
				childAdded := walk(n.children)
				if nameMatch || childAdded {
					anyAdded = true
				} else {
					visible = visible[:pos]
				}
			} else if nameMatch {
				visible = append(visible, n)
				anyAdded = true
			}
		}
		return anyAdded
	}
	walk(m.nodes)

	if m.canGoUp {
		visible = append(visible, nil) // nil = ".."
	}
	return visible
}

// findParent finds the nearest ancestor directory node for the item at cursorIdx.
func (m model) findParent(visible []*node, cursorIdx int) (*node, int) {
	target := visible[cursorIdx]
	if target == nil {
		return nil, -1
	}
	for i := cursorIdx - 1; i >= 0; i-- {
		n := visible[i]
		if n != nil && n.entry.isDir && n.depth < target.depth {
			return n, i
		}
	}
	return nil, -1
}

// startLoading sets loading state and returns a tick command to start the spinner.
func (m *model) startLoading() tea.Cmd {
	m.loading = true
	m.spinnerFrame = 0
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) loadChildren(n *node) tea.Cmd {
	key := n.entry.key
	depth := n.depth
	bucket := m.bucket
	client := m.client
	ctx := m.ctx

	return func() tea.Msg {
		entries, err := client.List(ctx, bucket, key, "/")
		if err != nil {
			return childrenLoadedMsg{parentKey: key, err: err}
		}
		var children []*node
		for _, e := range entries {
			name := strings.TrimPrefix(e.Key, key)
			if name == "" {
				continue
			}
			children = append(children, &node{
				entry: entry{
					name:         name,
					key:          e.Key,
					isDir:        e.IsDir,
					size:         e.Size,
					lastModified: e.LastModified,
				},
				depth: depth + 1,
			})
		}
		return childrenLoadedMsg{parentKey: key, children: children}
	}
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	// Header art
	b.WriteString("\n")
	b.WriteString("      ██  _   ___  _    ____\n")
	b.WriteString("     ██  /_\\ | _ \\| |  | __|\n")
	b.WriteString("  ▄▄██▄ / _ \\| | || |__| _|\n")
	b.WriteString("  ██████_/ \\_\\___/|____|____|\n")
	b.WriteString("   ▀██▀  " + styleMeta.Render(m.version) + "\n")
	b.WriteString("\n")

	// Path
	b.WriteString("  " + styleHeader.Render(m.header) + "\n\n")

	visible := m.visibleNodes()

	// Determine visible range for scrolling.
	// Header uses 9 lines (art + path + blanks), help uses 3 lines.
	const headerLines = 9
	const footerLines = 3
	listHeight := len(visible)
	startIdx := 0
	endIdx := listHeight
	if m.termHeight > 0 {
		maxItems := m.termHeight - headerLines - footerLines
		if m.message != "" {
			maxItems -= 2 // message line + blank
		}
		if maxItems < 1 {
			maxItems = 1
		}
		if listHeight > maxItems {
			half := maxItems / 2
			startIdx = m.cursor - half
			if startIdx < 0 {
				startIdx = 0
			}
			endIdx = startIdx + maxItems
			if endIdx > listHeight {
				endIdx = listHeight
				startIdx = endIdx - maxItems
			}
		}
	}

	// Calculate max name column width for alignment.
	// All emoji icons render as 2 cells in terminal, but runewidth
	// misreports some (e.g. 🖼️), so we use a constant.
	const iconDisplayWidth = 2
	maxNameWidth := 0
	for i := startIdx; i < endIdx; i++ {
		n := visible[i]
		if n == nil || n.entry.isDir || n.entry.isBucket {
			continue
		}
		w := n.depth*4 + iconDisplayWidth + 1 + len(n.entry.name)
		if w > maxNameWidth {
			maxNameWidth = w
		}
	}

	if startIdx > 0 {
		b.WriteString(styleHelp.Render(fmt.Sprintf("  (%d more above)", startIdx)) + "\n")
	}

	for i := startIdx; i < endIdx; i++ {
		n := visible[i]
		isCursor := i == m.cursor
		prefix := "  "
		if isCursor {
			prefix = styleCursor.Render("> ")
		}

		if n == nil {
			// ".." entry
			line := ".."
			if isCursor {
				line = styleSelected.Render(line)
			}
			b.WriteString(prefix + line + "\n")
			continue
		}

		indent := strings.Repeat("    ", n.depth)
		icon := iconForEntry(n.entry.name, n.entry.isDir, n.entry.isBucket)
		nameCol := indent + icon + " " + n.entry.name

		// Build metadata suffix for files, aligned to a common column
		var metaSuffix string
		if !n.entry.isDir && !n.entry.isBucket {
			nameWidth := n.depth*4 + iconDisplayWidth + 1 + len(n.entry.name)
			pad := ""
			if maxNameWidth > nameWidth {
				pad = strings.Repeat(" ", maxNameWidth-nameWidth)
			}
			var metaParts []string
			if n.entry.size > 0 {
				s := formatSize(n.entry.size)
				metaParts = append(metaParts, fmt.Sprintf("%10s", s))
			}
			if !n.entry.lastModified.IsZero() {
				metaParts = append(metaParts, n.entry.lastModified.Format("2006-01-02 15:04"))
			}
			if len(metaParts) > 0 {
				metaSuffix = pad + "  " + styleMeta.Render(strings.Join(metaParts, "  "))
			}
		}

		if isCursor {
			nameCol = styleSelected.Render(nameCol)
		}
		b.WriteString(prefix + nameCol + metaSuffix + "\n")
	}

	if endIdx < listHeight {
		b.WriteString(styleHelp.Render(fmt.Sprintf("  (%d more below)", listHeight-endIdx)) + "\n")
	}

	// Message (e.g. error from last action)
	if m.message != "" {
		b.WriteString("\n  " + styleMessage.Render(m.message) + "\n")
	}

	// Filter line
	if m.filtering {
		b.WriteString("\n  " + styleFilter.Render("/ "+m.filterText) + "▏\n")
	}

	// Help text
	b.WriteString("\n")
	if m.loading {
		frame := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
		b.WriteString(styleHelp.Render("  "+frame+" Loading...") + "\n")
	}
	help := "  ↑/↓ navigate  ←/→ collapse/expand  enter select"
	if m.canGoUp {
		help += "  - up"
	}
	help += "  / filter  esc×2 quit"
	b.WriteString(styleHelp.Render(help) + "\n")

	return b.String()
}
