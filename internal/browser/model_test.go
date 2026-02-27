package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jingu/ladle/internal/storage"
	"github.com/jingu/ladle/internal/uri"
)

func newTestModel(nodes []*node, canGoUp bool) model {
	mock := storage.NewMockClient()
	u, _ := uri.Parse("s3://test/")
	b := New(mock, u, nil, nil, "test")
	return model{
		nodes:      nodes,
		cursor:     0,
		header:     "s3://test",
		version:    "test",
		canGoUp:    canGoUp,
		client:     mock,
		ctx:        context.Background(),
		bucket:     "test",
		scheme:     "s3",
		browser:    b,
		editFn:     func(u *uri.URI) error { return nil },
		editMetaFn: func(u *uri.URI) error { return nil },
		downloadFn: func(u *uri.URI, dir string) error { return nil },
	}
}

func TestCursorMovement(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file1.txt", key: "file1.txt"}},
		{entry: entry{name: "file2.txt", key: "file2.txt"}},
		{entry: entry{name: "file3.txt", key: "file3.txt"}},
	}
	m := newTestModel(nodes, false)

	// Move down
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	if m.cursor != 1 {
		t.Errorf("expected cursor 1, got %d", m.cursor)
	}

	// Move down again
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	if m.cursor != 2 {
		t.Errorf("expected cursor 2, got %d", m.cursor)
	}

	// Move down past end (should stay)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	if m.cursor != 2 {
		t.Errorf("expected cursor 2, got %d", m.cursor)
	}

	// Move up
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(model)
	if m.cursor != 1 {
		t.Errorf("expected cursor 1, got %d", m.cursor)
	}

	// Move up past start (should stay)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(model)
	if m.cursor != 0 {
		t.Errorf("expected cursor 0, got %d", m.cursor)
	}
}

func TestCursorWithJK(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "a.txt", key: "a.txt"}},
		{entry: entry{name: "b.txt", key: "b.txt"}},
	}
	m := newTestModel(nodes, false)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(model)
	if m.cursor != 1 {
		t.Errorf("j: expected cursor 1, got %d", m.cursor)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(model)
	if m.cursor != 0 {
		t.Errorf("k: expected cursor 0, got %d", m.cursor)
	}
}

func TestCursorWithEmacs(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "a.txt", key: "a.txt"}},
		{entry: entry{name: "b.txt", key: "b.txt"}},
	}
	m := newTestModel(nodes, false)

	// Ctrl+N = down
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	m = updated.(model)
	if m.cursor != 1 {
		t.Errorf("ctrl+n: expected cursor 1, got %d", m.cursor)
	}

	// Ctrl+P = up
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	m = updated.(model)
	if m.cursor != 0 {
		t.Errorf("ctrl+p: expected cursor 0, got %d", m.cursor)
	}
}

func TestDirectoryExpandCollapse(t *testing.T) {
	childNodes := []*node{
		{entry: entry{name: "inner.txt", key: "dir/inner.txt"}, depth: 1},
	}
	nodes := []*node{
		{entry: entry{name: "dir/", key: "dir/", isDir: true}, loaded: true, children: childNodes},
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)

	// Initially collapsed: visible = [dir/, file.txt]
	visible := m.visibleNodes()
	if len(visible) != 2 {
		t.Fatalf("expected 2 visible nodes, got %d", len(visible))
	}

	// Press enter to expand dir
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	visible = m.visibleNodes()
	if len(visible) != 3 {
		t.Fatalf("after expand: expected 3 visible nodes, got %d", len(visible))
	}
	if visible[1].entry.name != "inner.txt" {
		t.Errorf("expected inner.txt at index 1, got %s", visible[1].entry.name)
	}

	// Press enter again to collapse
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	visible = m.visibleNodes()
	if len(visible) != 2 {
		t.Fatalf("after collapse: expected 2 visible nodes, got %d", len(visible))
	}
}

func TestArrowKeyExpandCollapse(t *testing.T) {
	childNodes := []*node{
		{entry: entry{name: "inner.txt", key: "dir/inner.txt"}, depth: 1},
	}
	nodes := []*node{
		{entry: entry{name: "dir/", key: "dir/", isDir: true}, loaded: true, children: childNodes},
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)

	// Right arrow to expand
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(model)

	visible := m.visibleNodes()
	if len(visible) != 3 {
		t.Fatalf("after right: expected 3 visible, got %d", len(visible))
	}

	// Right again on already expanded (no-op)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(model)
	if len(m.visibleNodes()) != 3 {
		t.Fatal("right on expanded should be no-op")
	}

	// Left arrow to collapse
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(model)

	visible = m.visibleNodes()
	if len(visible) != 2 {
		t.Fatalf("after left: expected 2 visible, got %d", len(visible))
	}

	// Left again on collapsed (no-op)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(model)
	if len(m.visibleNodes()) != 2 {
		t.Fatal("left on collapsed should be no-op")
	}
}

func TestFileSelection(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "dir/", key: "dir/", isDir: true}},
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)

	// Move to file.txt
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)

	// Press enter — should return a tea.Cmd (tea.Exec), not quit
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a command for file edit, got nil")
	}
}

func TestGoUpSelection(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, true)

	visible := m.visibleNodes()
	if len(visible) != 2 {
		t.Fatalf("expected 2 visible, got %d", len(visible))
	}

	// Move to ".."
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)

	// Press enter on ".." — should return a navigate command
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a command for go up, got nil")
	}
}

func TestHyphenGoUp(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, true)

	// "-" should return a navigate command
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'-'}})
	if cmd == nil {
		t.Fatal("expected a command for go up, got nil")
	}
}

func TestDoubleEscQuit(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)

	// Single Esc should not quit
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(model)
	if m.quitting {
		t.Error("single Esc should not quit")
	}

	// Second Esc within 500ms should quit
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(model)
	if !m.quitting {
		t.Error("double Esc should quit")
	}
}

func TestSingleEscDoesNotQuit(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)

	// Single Esc
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(model)

	// Non-Esc key resets the Esc state
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)

	// Another single Esc should not quit
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(model)
	if m.quitting {
		t.Error("Esc after non-Esc key should not quit")
	}
}

func TestViewOutput(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "dir/", key: "dir/", isDir: true}},
		{entry: entry{name: "readme.md", key: "readme.md", size: 4608}},
	}
	m := newTestModel(nodes, true)

	view := m.View()

	if !strings.Contains(view, "▄██▄") {
		t.Error("expected logo in header")
	}
	if !strings.Contains(view, "test") {
		t.Error("expected version in header")
	}
	if !strings.Contains(view, "s3://test") {
		t.Error("expected path header")
	}
	if !strings.Contains(view, "dir/") {
		t.Error("expected dir in output")
	}
	if !strings.Contains(view, "readme.md") {
		t.Error("expected file in output")
	}
	if !strings.Contains(view, "..") {
		t.Error("expected .. in output")
	}
	if !strings.Contains(view, "navigate") {
		t.Error("expected help text")
	}
}

func TestBucketSelection(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "my-bucket", isBucket: true}},
		{entry: entry{name: "other-bucket", isBucket: true}},
	}
	m := newTestModel(nodes, false)

	// Move to second bucket
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)

	// Select it — should return a navigate command
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a command for bucket selection, got nil")
	}
}

func TestLeftKeyOnChildCollapsesParent(t *testing.T) {
	childNodes := []*node{
		{entry: entry{name: "a.txt", key: "dir/a.txt"}, depth: 1},
		{entry: entry{name: "b.txt", key: "dir/b.txt"}, depth: 1},
	}
	nodes := []*node{
		{entry: entry{name: "dir/", key: "dir/", isDir: true}, loaded: true, expanded: true, children: childNodes},
		{entry: entry{name: "other.txt", key: "other.txt"}},
	}
	m := newTestModel(nodes, false)

	// visible: [dir/, a.txt, b.txt, other.txt]
	visible := m.visibleNodes()
	if len(visible) != 4 {
		t.Fatalf("expected 4 visible, got %d", len(visible))
	}

	// Move cursor to b.txt (index 2)
	m.cursor = 2

	// Press left on child file
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(model)

	// Parent should be collapsed, cursor should move to dir/ (index 0)
	if m.nodes[0].expanded {
		t.Error("expected parent dir to be collapsed")
	}
	if m.cursor != 0 {
		t.Errorf("expected cursor at 0 (parent), got %d", m.cursor)
	}

	visible = m.visibleNodes()
	if len(visible) != 2 {
		t.Fatalf("expected 2 visible after collapse, got %d", len(visible))
	}
}

func TestLeftKeyOnNestedChildCollapsesImmediateParent(t *testing.T) {
	innerChild := []*node{
		{entry: entry{name: "deep.txt", key: "dir/sub/deep.txt"}, depth: 2},
	}
	subDir := &node{
		entry: entry{name: "sub/", key: "dir/sub/", isDir: true}, depth: 1,
		loaded: true, expanded: true, children: innerChild,
	}
	nodes := []*node{
		{entry: entry{name: "dir/", key: "dir/", isDir: true}, loaded: true, expanded: true, children: []*node{subDir}},
	}
	m := newTestModel(nodes, false)

	// visible: [dir/, sub/, deep.txt]
	visible := m.visibleNodes()
	if len(visible) != 3 {
		t.Fatalf("expected 3 visible, got %d", len(visible))
	}

	// Move cursor to deep.txt (index 2)
	m.cursor = 2

	// Press left: should collapse sub/ (immediate parent), not dir/
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(model)

	if subDir.expanded {
		t.Error("expected sub/ to be collapsed")
	}
	if !m.nodes[0].expanded {
		t.Error("expected dir/ to remain expanded")
	}
	if m.cursor != 1 {
		t.Errorf("expected cursor at 1 (sub/), got %d", m.cursor)
	}
}

func TestViewScrolling(t *testing.T) {
	// Create many nodes
	var nodes []*node
	for i := 0; i < 50; i++ {
		nodes = append(nodes, &node{
			entry: entry{name: fmt.Sprintf("file%02d.txt", i), key: fmt.Sprintf("file%02d.txt", i)},
		})
	}
	m := newTestModel(nodes, false)
	m.termHeight = 20
	m.cursor = 25

	view := m.View()
	if !strings.Contains(view, "more above") {
		t.Error("expected 'more above' indicator")
	}
	if !strings.Contains(view, "more below") {
		t.Error("expected 'more below' indicator")
	}
	// Current cursor item should be visible
	if !strings.Contains(view, "file25.txt") {
		t.Error("expected cursor item to be visible")
	}
}

func TestChildrenLoadedMsg(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "dir/", key: "dir/", isDir: true}},
	}
	m := newTestModel(nodes, false)

	children := []*node{
		{entry: entry{name: "child.txt", key: "dir/child.txt"}, depth: 1},
	}
	msg := childrenLoadedMsg{parentKey: "dir/", children: children}
	updated, _ := m.Update(msg)
	m = updated.(model)

	if !m.nodes[0].loaded {
		t.Error("expected node to be loaded")
	}
	if !m.nodes[0].expanded {
		t.Error("expected node to be expanded")
	}
	if len(m.nodes[0].children) != 1 {
		t.Errorf("expected 1 child, got %d", len(m.nodes[0].children))
	}
}

func TestEditDoneWithError(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)

	msg := editDoneMsg{err: fmt.Errorf("binary file detected")}
	updated, _ := m.Update(msg)
	m = updated.(model)

	if m.message != "binary file detected" {
		t.Errorf("expected error message, got %q", m.message)
	}
	if m.quitting {
		t.Error("should not be quitting after edit error")
	}
}

func TestEditDoneSuccess(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)

	msg := editDoneMsg{err: nil}
	updated, _ := m.Update(msg)
	m = updated.(model)

	if m.message != "" {
		t.Errorf("expected empty message, got %q", m.message)
	}
}

func TestFilterMode(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "config.json", key: "config.json"}},
		{entry: entry{name: "config.yaml", key: "config.yaml"}},
		{entry: entry{name: "readme.md", key: "readme.md"}},
		{entry: entry{name: "data/", key: "data/", isDir: true}},
	}
	m := newTestModel(nodes, false)

	// Press "/" to enter filter mode
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(model)
	if !m.filtering {
		t.Fatal("expected filtering mode")
	}

	// Type "config"
	for _, r := range "config" {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(model)
	}
	if m.filterText != "config" {
		t.Errorf("expected filterText 'config', got %q", m.filterText)
	}

	visible := m.visibleNodes()
	if len(visible) != 2 {
		t.Errorf("expected 2 visible nodes, got %d", len(visible))
	}
	for _, n := range visible {
		if !strings.Contains(n.entry.name, "config") {
			t.Errorf("unexpected node %q in filtered results", n.entry.name)
		}
	}
}

func TestFilterMatchesExpandedChildren(t *testing.T) {
	// Simulates: dev/ (expanded) > admin/ (expanded) > callback.html
	innerChild := []*node{
		{entry: entry{name: "callback.html", key: "dev/admin/callback.html"}, depth: 2},
	}
	adminDir := &node{
		entry: entry{name: "admin/", key: "dev/admin/", isDir: true}, depth: 1,
		loaded: true, expanded: true, children: innerChild,
	}
	cdnDir := &node{
		entry: entry{name: "cdn/", key: "dev/cdn/", isDir: true}, depth: 1,
		loaded: true, expanded: false,
	}
	nodes := []*node{
		{entry: entry{name: "dev/", key: "dev/", isDir: true}, loaded: true, expanded: true,
			children: []*node{adminDir, cdnDir}},
		{entry: entry{name: "static/", key: "static/", isDir: true}},
	}
	m := newTestModel(nodes, false)

	// Filter "call" — should find callback.html inside expanded dev/admin/
	m.filterText = "call"
	visible := m.visibleNodes()

	var names []string
	for _, n := range visible {
		if n != nil {
			names = append(names, n.entry.name)
		}
	}
	// Expect: dev/ > admin/ > callback.html (ancestors shown for context)
	if len(visible) != 3 {
		t.Fatalf("expected 3 visible (dev/, admin/, callback.html), got %d: %v", len(visible), names)
	}
	if visible[2].entry.name != "callback.html" {
		t.Errorf("expected callback.html at index 2, got %q", visible[2].entry.name)
	}

	// Filter "cdn" — cdn/ name matches directly
	m.filterText = "cdn"
	visible = m.visibleNodes()
	names = nil
	for _, n := range visible {
		if n != nil {
			names = append(names, n.entry.name)
		}
	}
	// cdn/ is collapsed so no children, but dev/ should show as parent
	if len(visible) != 2 {
		t.Fatalf("expected 2 visible (dev/, cdn/), got %d: %v", len(visible), names)
	}
}

func TestFilterCollapsedDirHidesChildren(t *testing.T) {
	// Collapsed dir with matching child — child should NOT appear
	children := []*node{
		{entry: entry{name: "match.txt", key: "dir/match.txt"}, depth: 1},
	}
	nodes := []*node{
		{entry: entry{name: "dir/", key: "dir/", isDir: true}, loaded: true, expanded: false, children: children},
	}
	m := newTestModel(nodes, false)
	m.filterText = "match"

	visible := m.visibleNodes()
	if len(visible) != 0 {
		var names []string
		for _, n := range visible {
			if n != nil {
				names = append(names, n.entry.name)
			}
		}
		t.Errorf("expected 0 visible (dir is collapsed), got %d: %v", len(visible), names)
	}
}

func TestFilterCaseInsensitive(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "README.md", key: "README.md"}},
		{entry: entry{name: "other.txt", key: "other.txt"}},
	}
	m := newTestModel(nodes, false)

	// Enter filter mode and type "readme" (lowercase)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(model)
	for _, r := range "readme" {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(model)
	}

	visible := m.visibleNodes()
	if len(visible) != 1 {
		t.Fatalf("expected 1 visible node, got %d", len(visible))
	}
	if visible[0].entry.name != "README.md" {
		t.Errorf("expected README.md, got %q", visible[0].entry.name)
	}
}

func TestFilterEscClear(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "config.json", key: "config.json"}},
		{entry: entry{name: "readme.md", key: "readme.md"}},
	}
	m := newTestModel(nodes, false)

	// Enter filter mode and type something
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = updated.(model)

	// Esc should clear filter and exit filter mode
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(model)
	if m.filtering {
		t.Error("expected filtering to be false")
	}
	if m.filterText != "" {
		t.Errorf("expected filterText empty, got %q", m.filterText)
	}

	// All nodes should be visible again
	visible := m.visibleNodes()
	if len(visible) != 2 {
		t.Errorf("expected 2 visible nodes, got %d", len(visible))
	}
}

func TestFilterEnterKeep(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "config.json", key: "config.json"}},
		{entry: entry{name: "config.yaml", key: "config.yaml"}},
		{entry: entry{name: "readme.md", key: "readme.md"}},
	}
	m := newTestModel(nodes, false)

	// Enter filter mode and type "config"
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(model)
	for _, r := range "config" {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(model)
	}

	// Enter should exit filter mode but keep filter text
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.filtering {
		t.Error("expected filtering to be false")
	}
	if m.filterText != "config" {
		t.Errorf("expected filterText 'config', got %q", m.filterText)
	}

	// Only config files should be visible
	visible := m.visibleNodes()
	if len(visible) != 2 {
		t.Errorf("expected 2 visible nodes, got %d", len(visible))
	}
}

func TestFilterBackspace(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "config.json", key: "config.json"}},
		{entry: entry{name: "readme.md", key: "readme.md"}},
	}
	m := newTestModel(nodes, false)

	// Enter filter mode and type "co"
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = updated.(model)
	if m.filterText != "co" {
		t.Errorf("expected filterText 'co', got %q", m.filterText)
	}

	// Backspace removes one char
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(model)
	if m.filterText != "c" {
		t.Errorf("expected filterText 'c', got %q", m.filterText)
	}
	if !m.filtering {
		t.Error("expected still in filter mode")
	}

	// Backspace again — empty, should exit filter mode
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(model)
	if m.filterText != "" {
		t.Errorf("expected filterText empty, got %q", m.filterText)
	}
	if m.filtering {
		t.Error("expected filtering to be false when text is empty")
	}
}

func TestFilterNavigationKeys(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "config.json", key: "config.json"}},
		{entry: entry{name: "config.yaml", key: "config.yaml"}},
		{entry: entry{name: "readme.md", key: "readme.md"}},
	}
	m := newTestModel(nodes, false)

	// Enter filter mode and type "config"
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(model)
	for _, r := range "config" {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(model)
	}

	// Arrow down should move cursor within filtered results
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	if m.cursor != 1 {
		t.Errorf("expected cursor 1, got %d", m.cursor)
	}
	if !m.filtering {
		t.Error("should still be in filter mode")
	}
}

func TestFilterGoUpEntryAlwaysVisible(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "config.json", key: "config.json"}},
		{entry: entry{name: "readme.md", key: "readme.md"}},
	}
	m := newTestModel(nodes, true) // canGoUp = true

	// Enter filter mode and type "xyz" (matches nothing)
	m.filtering = true
	m.filterText = "xyz"

	visible := m.visibleNodes()
	// ".." should still be visible even though no files match
	if len(visible) != 1 {
		t.Fatalf("expected 1 visible node (..), got %d", len(visible))
	}
	if visible[0] != nil {
		t.Error("expected nil (..) entry")
	}
}

func TestFilterViewShowsFilterLine(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.filtering = true
	m.filterText = "test"

	view := m.View()
	if !strings.Contains(view, "/ test") {
		t.Error("expected filter line in view")
	}
}

func TestNavigatedMsg(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.cursor = 0

	newNodes := []*node{
		{entry: entry{name: "other.txt", key: "other.txt"}},
		{entry: entry{name: "another.txt", key: "another.txt"}},
	}
	msg := navigatedMsg{nodes: newNodes, header: "s3://newbucket", canGoUp: true, bucket: "newbucket"}
	updated, _ := m.Update(msg)
	m = updated.(model)

	if len(m.nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(m.nodes))
	}
	if m.header != "s3://newbucket" {
		t.Errorf("expected new header, got %q", m.header)
	}
	if !m.canGoUp {
		t.Error("expected canGoUp to be true")
	}
	if m.cursor != 0 {
		t.Errorf("expected cursor reset to 0, got %d", m.cursor)
	}
	if m.bucket != "newbucket" {
		t.Errorf("expected bucket 'newbucket', got %q", m.bucket)
	}
}

func TestNavigatedMsgSyncsBucket(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	// Simulate initial bucket = "test"
	if m.bucket != "test" {
		t.Fatalf("expected initial bucket 'test', got %q", m.bucket)
	}

	// Simulate navigateToBucket result
	msg := navigatedMsg{
		nodes:   []*node{{entry: entry{name: "new.txt", key: "new.txt"}}},
		header:  "s3://otherbucket",
		canGoUp: true,
		bucket:  "otherbucket",
	}
	updated, _ := m.Update(msg)
	m = updated.(model)

	if m.bucket != "otherbucket" {
		t.Errorf("expected bucket synced to 'otherbucket', got %q", m.bucket)
	}
}

func TestNavigatedMsgResetsFilter(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.filtering = true
	m.filterText = "old"

	msg := navigatedMsg{
		nodes:   []*node{{entry: entry{name: "new.txt", key: "new.txt"}}},
		header:  "s3://test",
		canGoUp: true,
		bucket:  "test",
	}
	updated, _ := m.Update(msg)
	m = updated.(model)

	if m.filtering {
		t.Error("expected filtering to be false after navigation")
	}
	if m.filterText != "" {
		t.Errorf("expected filterText empty after navigation, got %q", m.filterText)
	}
}

func TestContextMenuOpenOnFile(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "dir/", key: "dir/", isDir: true}},
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)

	// Move cursor to file.txt
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)

	// Press right arrow — should open context menu
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(model)

	if !m.menuOpen {
		t.Fatal("expected context menu to be open")
	}
	if m.menuTarget == nil || m.menuTarget.entry.name != "file.txt" {
		t.Error("expected menu target to be file.txt")
	}
	if m.menuCursor != 0 {
		t.Errorf("expected menu cursor at 0, got %d", m.menuCursor)
	}
}

func TestContextMenuNotOnDir(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "dir/", key: "dir/", isDir: true}, loaded: true},
	}
	m := newTestModel(nodes, false)

	// Right arrow on dir should expand, not open menu
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(model)

	if m.menuOpen {
		t.Error("context menu should not open on directory")
	}
}

func TestContextMenuNavigation(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.menuOpen = true
	m.menuTarget = nodes[0]
	m.menuCursor = 0

	// Move down
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	if m.menuCursor != 1 {
		t.Errorf("expected menu cursor 1, got %d", m.menuCursor)
	}

	// Move down with j
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(model)
	if m.menuCursor != 2 {
		t.Errorf("expected menu cursor 2, got %d", m.menuCursor)
	}

	// Move up
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(model)
	if m.menuCursor != 1 {
		t.Errorf("expected menu cursor 1, got %d", m.menuCursor)
	}

	// Move up with k
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(model)
	if m.menuCursor != 0 {
		t.Errorf("expected menu cursor 0, got %d", m.menuCursor)
	}
}

func TestContextMenuEscClose(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.menuOpen = true
	m.menuTarget = nodes[0]

	// Esc should close menu
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(model)

	if m.menuOpen {
		t.Error("expected menu to be closed after Esc")
	}
	if m.menuTarget != nil {
		t.Error("expected menu target to be nil after close")
	}
}

func TestContextMenuLeftClose(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.menuOpen = true
	m.menuTarget = nodes[0]

	// Left arrow should close menu
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(model)

	if m.menuOpen {
		t.Error("expected menu to be closed after left arrow")
	}
}

func TestContextMenuEditAction(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.menuOpen = true
	m.menuTarget = nodes[0]
	m.menuCursor = 0 // Edit

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a command for edit action")
	}
}

func TestContextMenuDeleteAction(t *testing.T) {
	mock := storage.NewMockClient()
	mock.PutObject("test", "file.txt", []byte("hello"), nil)

	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	u, _ := uri.Parse("s3://test/")
	b := New(mock, u, nil, nil, "test")
	m := model{
		nodes:      nodes,
		cursor:     0,
		header:     "s3://test",
		version:    "test",
		client:     mock,
		ctx:        context.Background(),
		bucket:     "test",
		scheme:     "s3",
		browser:    b,
		editFn:     func(u *uri.URI) error { return nil },
		editMetaFn: func(u *uri.URI) error { return nil },
		menuOpen:   true,
		menuTarget: nodes[0],
		menuCursor: 5, // Delete
	}

	// Selecting Delete should open confirm dialog, not delete immediately
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if m.menuOpen {
		t.Error("menu should be closed after selecting delete")
	}
	if !m.confirmMode {
		t.Fatal("expected confirm mode to be active")
	}
	if m.loading {
		t.Error("should not be loading yet (waiting for confirmation)")
	}
	if cmd != nil {
		t.Error("no command should be returned before confirmation")
	}
	if !strings.Contains(m.confirmPrompt, "file.txt") {
		t.Errorf("confirm prompt should mention the file, got %q", m.confirmPrompt)
	}
}

func TestDeleteConfirmYes(t *testing.T) {
	mock := storage.NewMockClient()
	mock.PutObject("test", "file.txt", []byte("hello"), nil)

	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	u, _ := uri.Parse("s3://test/")
	b := New(mock, u, nil, nil, "test")
	m := model{
		nodes:      nodes,
		cursor:     0,
		header:     "s3://test",
		version:    "test",
		client:     mock,
		ctx:        context.Background(),
		bucket:     "test",
		scheme:     "s3",
		browser:    b,
		editFn:     func(u *uri.URI) error { return nil },
		editMetaFn: func(u *uri.URI) error { return nil },
		menuOpen:   true,
		menuTarget: nodes[0],
		menuCursor: 5, // Delete
	}

	// Open confirm dialog
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	// Press "y" to confirm
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(model)

	if m.confirmMode {
		t.Error("confirm mode should be closed after confirmation")
	}
	if !m.loading {
		t.Error("expected loading state during delete")
	}
	if cmd == nil {
		t.Fatal("expected a command for delete")
	}
}

func TestDeleteConfirmNo(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.menuOpen = true
	m.menuTarget = nodes[0]
	m.menuCursor = 5 // Delete

	// Open confirm dialog
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	// Press "n" to cancel
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(model)

	if m.confirmMode {
		t.Error("confirm mode should be closed")
	}
	if m.loading {
		t.Error("should not be loading after cancel")
	}
}

func TestDeleteConfirmEsc(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.menuOpen = true
	m.menuTarget = nodes[0]
	m.menuCursor = 5 // Delete

	// Open confirm dialog
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	// Press Esc to cancel
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(model)

	if m.confirmMode {
		t.Error("confirm mode should be closed")
	}
}

func TestDeleteConfirmEnterDefaultNo(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.menuOpen = true
	m.menuTarget = nodes[0]
	m.menuCursor = 5 // Delete

	// Open confirm dialog
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	// Press Enter without typing "y" -> default No
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if m.confirmMode {
		t.Error("confirm mode should be closed")
	}
	if m.loading {
		t.Error("should not be loading after default cancel")
	}
}

func TestContextMenuCopyOpensInput(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "path/file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.menuOpen = true
	m.menuTarget = nodes[0]
	m.menuCursor = 3 // Copy to...

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if !m.inputMode {
		t.Fatal("expected input mode for copy")
	}
	if m.inputText != "path/file.txt" {
		t.Errorf("expected input text to be pre-filled with key, got %q", m.inputText)
	}
	if m.inputAction != menuCopy {
		t.Errorf("expected input action to be menuCopy")
	}
}

func TestContextMenuMoveOpensInput(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "path/file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.menuOpen = true
	m.menuTarget = nodes[0]
	m.menuCursor = 4 // Move to...

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if !m.inputMode {
		t.Fatal("expected input mode for move")
	}
	if m.inputAction != menuMove {
		t.Errorf("expected input action to be menuMove")
	}
}

func TestContextMenuDownloadOpensInput(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.menuOpen = true
	m.menuTarget = nodes[0]
	m.menuCursor = 2 // Download to...

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if !m.inputMode {
		t.Fatal("expected input mode for download")
	}
	if m.inputText != "./" {
		t.Errorf("expected default input './', got %q", m.inputText)
	}
}

func TestInputModeEscCancel(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.inputMode = true
	m.inputText = "some/path"
	m.inputAction = menuCopy

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(model)

	if m.inputMode {
		t.Error("expected input mode to be cancelled")
	}
	if m.inputText != "" {
		t.Errorf("expected input text to be cleared, got %q", m.inputText)
	}
}

func TestInputModeTyping(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.inputMode = true
	m.inputText = ""
	m.inputAction = menuCopy

	// Type characters
	for _, r := range "new/path" {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(model)
	}
	if m.inputText != "new/path" {
		t.Errorf("expected input text 'new/path', got %q", m.inputText)
	}

	// Backspace
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(model)
	if m.inputText != "new/pat" {
		t.Errorf("expected input text 'new/pat', got %q", m.inputText)
	}
}

func TestInputSamePathError(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.inputMode = true
	m.inputText = "file.txt"
	m.inputAction = menuCopy
	m.menuTarget = nodes[0]

	// Enter with same path as source
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	if m.message != "Source and destination are the same" {
		t.Errorf("expected same path error, got %q", m.message)
	}
}

func TestViewWithContextMenu(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.menuOpen = true
	m.menuTarget = nodes[0]
	m.menuCursor = 0

	view := m.View()
	if !strings.Contains(view, "Edit") {
		t.Error("expected Edit in context menu")
	}
	if !strings.Contains(view, "Delete") {
		t.Error("expected Delete in context menu")
	}
	if !strings.Contains(view, "Copy to") {
		t.Error("expected Copy to... in context menu")
	}
	if !strings.Contains(view, "esc") {
		t.Error("expected menu help text")
	}
}

func TestViewWithInputMode(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.inputMode = true
	m.inputPrompt = "Copy to path"
	m.inputText = "new/path"

	view := m.View()
	if !strings.Contains(view, "Copy to path") {
		t.Error("expected input prompt in view")
	}
	if !strings.Contains(view, "new/path") {
		t.Error("expected input text in view")
	}
}

func TestActionDoneMsg(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.loading = true

	msg := actionDoneMsg{message: "Deleted file.txt"}
	updated, _ := m.Update(msg)
	m = updated.(model)

	if m.loading {
		t.Error("expected loading to be false")
	}
	if m.message != "Deleted file.txt" {
		t.Errorf("expected message 'Deleted file.txt', got %q", m.message)
	}
}

func TestActionDoneMsgWithError(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.loading = true

	msg := actionDoneMsg{err: fmt.Errorf("permission denied")}
	updated, _ := m.Update(msg)
	m = updated.(model)

	if m.loading {
		t.Error("expected loading to be false")
	}
	if m.message != "permission denied" {
		t.Errorf("expected error message, got %q", m.message)
	}
}

func TestCompleteInputSingleMatch(t *testing.T) {
	candidates := []string{"path/to/file.txt", "path/to/other.txt"}
	result := completeInput("path/to/f", "path/to/", candidates)
	if result != "path/to/file.txt" {
		t.Errorf("expected 'path/to/file.txt', got %q", result)
	}
}

func TestCompleteInputMultipleMatches(t *testing.T) {
	candidates := []string{"dir/config.json", "dir/config.yaml", "dir/readme.md"}
	result := completeInput("dir/con", "dir/", candidates)
	if result != "dir/config." {
		t.Errorf("expected 'dir/config.', got %q", result)
	}
}

func TestCompleteInputNoMatch(t *testing.T) {
	candidates := []string{"path/to/file.txt"}
	result := completeInput("path/xyz", "path/", candidates)
	if result != "path/xyz" {
		t.Errorf("expected unchanged 'path/xyz', got %q", result)
	}
}

func TestCompleteInputEmptyCandidates(t *testing.T) {
	result := completeInput("some/path", "some/", nil)
	if result != "some/path" {
		t.Errorf("expected unchanged 'some/path', got %q", result)
	}
}

func TestCompleteInputExactMatch(t *testing.T) {
	candidates := []string{"dir/file.txt"}
	result := completeInput("dir/file.txt", "dir/", candidates)
	if result != "dir/file.txt" {
		t.Errorf("expected 'dir/file.txt', got %q", result)
	}
}

func TestCompleteInputRootLevel(t *testing.T) {
	candidates := []string{"readme.md", "readme.txt"}
	result := completeInput("read", "", candidates)
	if result != "readme." {
		t.Errorf("expected 'readme.', got %q", result)
	}
}

func TestCompleteInputDirPrefix(t *testing.T) {
	// When input is "logs/" and candidates include dirs
	candidates := []string{"logs/2024/", "logs/2025/"}
	result := completeInput("logs/20", "logs/", candidates)
	if result != "logs/202" {
		t.Errorf("expected 'logs/202', got %q", result)
	}
}

func TestTabKeyInInputModeCopyMove(t *testing.T) {
	mock := storage.NewMockClient()
	mock.PutObject("test", "dir/file1.txt", []byte("a"), nil)
	mock.PutObject("test", "dir/file2.txt", []byte("b"), nil)

	nodes := []*node{
		{entry: entry{name: "dir/file1.txt", key: "dir/file1.txt"}},
	}
	u, _ := uri.Parse("s3://test/")
	b := New(mock, u, nil, nil, "test")
	m := model{
		nodes:       nodes,
		client:      mock,
		ctx:         context.Background(),
		bucket:      "test",
		scheme:      "s3",
		browser:     b,
		editFn:      func(u *uri.URI) error { return nil },
		editMetaFn:  func(u *uri.URI) error { return nil },
		inputMode:   true,
		inputText:   "dir/file",
		inputAction: menuCopy,
	}

	// Tab should return a command (async completion)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if cmd == nil {
		t.Fatal("expected a command for tab completion")
	}

	// Execute the command to get the message
	msg := cmd()
	tcMsg, ok := msg.(tabCompleteMsg)
	if !ok {
		t.Fatalf("expected tabCompleteMsg, got %T", msg)
	}
	if len(tcMsg.candidates) != 2 {
		t.Errorf("expected 2 candidates, got %d", len(tcMsg.candidates))
	}

	// Apply the message
	updated, _ := m.Update(tcMsg)
	m = updated.(model)
	if m.inputText != "dir/file" {
		// Both match "dir/file" prefix, so LCP is "dir/file" - stays the same
		// since both are "dir/file1.txt" and "dir/file2.txt", LCP = "dir/file"
		t.Errorf("expected 'dir/file', got %q", m.inputText)
	}
}

func TestTabCompleteMsg(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.inputMode = true
	m.inputText = "path/fi"

	msg := tabCompleteMsg{
		candidates: []string{"path/file.txt", "path/final.txt"},
		prefix:     "path/",
	}
	updated, _ := m.Update(msg)
	m = updated.(model)

	if m.inputText != "path/fi" {
		// LCP of "path/file.txt" and "path/final.txt" matching "path/fi" is "path/fi"
		// Actually: both start with "path/fi", LCP = "path/fi" (l vs n at index 7)
		t.Errorf("expected 'path/fi', got %q", m.inputText)
	}
}

func TestTabCompleteMsgSingleCandidate(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.inputMode = true
	m.inputText = "path/fi"

	msg := tabCompleteMsg{
		candidates: []string{"path/file.txt"},
		prefix:     "path/",
	}
	updated, _ := m.Update(msg)
	m = updated.(model)

	if m.inputText != "path/file.txt" {
		t.Errorf("expected 'path/file.txt', got %q", m.inputText)
	}
}

func TestTabKeyInDownloadMode(t *testing.T) {
	nodes := []*node{
		{entry: entry{name: "file.txt", key: "file.txt"}},
	}
	m := newTestModel(nodes, false)
	m.inputMode = true
	m.inputText = "./"
	m.inputAction = menuDownload

	// Tab in download mode should now trigger local completion
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if cmd == nil {
		t.Fatal("expected a command for local tab completion")
	}

	// Execute the command - it should return localTabCompleteMsg
	msg := cmd()
	_, ok := msg.(localTabCompleteMsg)
	if !ok {
		t.Fatalf("expected localTabCompleteMsg, got %T", msg)
	}
}

func TestLocalTabComplete(t *testing.T) {
	// Create a temp directory with some files
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "alpha.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "alpha.log"), []byte("b"), 0644)
	os.Mkdir(filepath.Join(tmpDir, "beta"), 0755)

	cmd := localTabComplete(filepath.Join(tmpDir, "al"))
	msg := cmd().(localTabCompleteMsg)

	// Should have 3 candidates: alpha.log, alpha.txt, beta/
	if len(msg.candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d: %v", len(msg.candidates), msg.candidates)
	}
}

func TestLocalTabCompleteSingleMatch(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "unique.txt"), []byte("a"), 0644)

	input := filepath.Join(tmpDir, "uni")
	cmd := localTabComplete(input)
	msg := cmd().(localTabCompleteMsg)

	nodes := []*node{{entry: entry{name: "file.txt", key: "file.txt"}}}
	m := newTestModel(nodes, false)
	m.inputMode = true
	m.inputText = input

	updated, _ := m.Update(msg)
	m = updated.(model)

	expected := filepath.Join(tmpDir, "unique.txt")
	if m.inputText != expected {
		t.Errorf("expected %q, got %q", expected, m.inputText)
	}
}

func TestLocalTabCompleteDir(t *testing.T) {
	tmpDir := t.TempDir()
	os.Mkdir(filepath.Join(tmpDir, "subdir"), 0755)

	input := filepath.Join(tmpDir, "sub")
	cmd := localTabComplete(input)
	msg := cmd().(localTabCompleteMsg)

	nodes := []*node{{entry: entry{name: "file.txt", key: "file.txt"}}}
	m := newTestModel(nodes, false)
	m.inputMode = true
	m.inputText = input

	updated, _ := m.Update(msg)
	m = updated.(model)

	expected := filepath.Join(tmpDir, "subdir") + string(os.PathSeparator)
	if m.inputText != expected {
		t.Errorf("expected %q, got %q", expected, m.inputText)
	}
}

func TestCompleteLocalInputSingleMatch(t *testing.T) {
	candidates := []string{"/tmp/dir/file.txt", "/tmp/dir/other.log"}
	result := completeLocalInput("/tmp/dir/f", candidates)
	if result != "/tmp/dir/file.txt" {
		t.Errorf("expected '/tmp/dir/file.txt', got %q", result)
	}
}

func TestCompleteLocalInputMultipleMatches(t *testing.T) {
	candidates := []string{"/tmp/dir/config.json", "/tmp/dir/config.yaml"}
	result := completeLocalInput("/tmp/dir/con", candidates)
	if result != "/tmp/dir/config." {
		t.Errorf("expected '/tmp/dir/config.', got %q", result)
	}
}

func TestCompleteLocalInputNoMatch(t *testing.T) {
	candidates := []string{"/tmp/dir/file.txt"}
	result := completeLocalInput("/tmp/dir/xyz", candidates)
	if result != "/tmp/dir/xyz" {
		t.Errorf("expected unchanged '/tmp/dir/xyz', got %q", result)
	}
}

func TestLongestCommonPrefix(t *testing.T) {
	tests := []struct {
		a, b     string
		expected string
	}{
		{"abc", "abd", "ab"},
		{"abc", "abc", "abc"},
		{"abc", "xyz", ""},
		{"abc", "ab", "ab"},
		{"", "abc", ""},
		{"abc", "", ""},
	}
	for _, tt := range tests {
		got := longestCommonPrefix(tt.a, tt.b)
		if got != tt.expected {
			t.Errorf("longestCommonPrefix(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.expected)
		}
	}
}
