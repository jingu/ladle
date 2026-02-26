package browser

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/jingu/ladle/internal/storage"
	"github.com/jingu/ladle/internal/uri"
)

// keyReader simulates terminal input: delivers single-byte keystrokes one at a time,
// and escape sequences (e.g., arrow keys) as 3-byte chunks.
type keyReader struct {
	data []byte
	pos  int
}

func newKeyReader(input string) *keyReader {
	return &keyReader{data: []byte(input)}
}

func (r *keyReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	// Escape sequences: deliver 3 bytes at once (like a real terminal)
	if r.data[r.pos] == 27 && r.pos+2 < len(r.data) && r.data[r.pos+1] == '[' {
		n := copy(p, r.data[r.pos:r.pos+3])
		r.pos += 3
		return n, nil
	}
	// Single keystroke: one byte at a time
	p[0] = r.data[r.pos]
	r.pos++
	return 1, nil
}

// newTestBrowser creates a Browser with simulated input for testing (bypasses raw terminal mode).
func newTestBrowser(client storage.Client, bucket, prefix string, bucketListEnabled bool, input string) (*Browser, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return &Browser{
		client:            client,
		scheme:            uri.SchemeS3,
		bucket:            bucket,
		prefix:            prefix,
		bucketListEnabled: bucketListEnabled,
		in:                newKeyReader(input),
		out:               out,
		expanded:          make(map[string]bool),
		childCache:        make(map[string][]storage.ListEntry),
	}, out
}

func TestBuildItems(t *testing.T) {
	entries := []storage.ListEntry{
		{Key: "dir/", IsDir: true},
		{Key: "file.txt", Size: 100},
	}

	b := &Browser{prefix: "", bucketListEnabled: false, expanded: make(map[string]bool)}
	items := b.buildItems(entries)

	// 2 entries + quit (no ".." since prefix is empty and bucketListEnabled is false)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0].label != "dir/" || !items[0].isDir {
		t.Errorf("item[0]: got %+v", items[0])
	}
	if items[1].label != "file.txt" || items[1].isDir {
		t.Errorf("item[1]: got %+v", items[1])
	}
	if !items[2].isNav || items[2].navID != "quit" {
		t.Errorf("item[2]: expected quit nav, got %+v", items[2])
	}
}

func TestBuildItemsWithPrefix(t *testing.T) {
	entries := []storage.ListEntry{
		{Key: "dir/sub/", IsDir: true},
		{Key: "dir/file.txt", Size: 50},
	}

	b := &Browser{prefix: "dir/", bucketListEnabled: false, expanded: make(map[string]bool)}
	items := b.buildItems(entries)

	// 2 entries + ".." + quit
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}
	if items[0].label != "sub/" {
		t.Errorf("expected label %q, got %q", "sub/", items[0].label)
	}
	if items[1].label != "file.txt" {
		t.Errorf("expected label %q, got %q", "file.txt", items[1].label)
	}
	if !items[2].isNav || items[2].navID != "up" {
		t.Errorf("item[2]: expected up nav, got %+v", items[2])
	}
}

func TestBuildItemsWithBucketListEnabled(t *testing.T) {
	entries := []storage.ListEntry{
		{Key: "file.txt", Size: 100},
	}

	b := &Browser{prefix: "", bucketListEnabled: true, expanded: make(map[string]bool)}
	items := b.buildItems(entries)

	// 1 entry + ".." + quit
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if !items[1].isNav || items[1].navID != "up" {
		t.Errorf("item[1]: expected up nav, got %+v", items[1])
	}
}

func TestBuildItemsSkipsEmptyLabel(t *testing.T) {
	entries := []storage.ListEntry{
		{Key: "dir/", IsDir: true}, // prefix is "dir/", so label becomes ""
		{Key: "dir/file.txt", Size: 50},
	}

	b := &Browser{prefix: "dir/", bucketListEnabled: false, expanded: make(map[string]bool)}
	items := b.buildItems(entries)

	// "dir/" stripped to "" is skipped, so: file.txt + ".." + quit
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0].label != "file.txt" {
		t.Errorf("expected label %q, got %q", "file.txt", items[0].label)
	}
}

func TestHandleInputEnterSelectsFirst(t *testing.T) {
	items := []item{
		{label: "file.txt"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	b, _ := newTestBrowser(nil, "b", "", false, "\r") // Enter
	idx, quit, _ := b.handleInput(context.Background(), items, 0)
	if quit {
		t.Fatal("expected no quit")
	}
	if idx != 0 {
		t.Errorf("expected idx 0, got %d", idx)
	}
}

func TestHandleInputNavigateDownAndSelect(t *testing.T) {
	items := []item{
		{label: "first.txt"},
		{label: "second.txt"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	b, _ := newTestBrowser(nil, "b", "", false, "j\r") // j(down) + Enter
	idx, quit, _ := b.handleInput(context.Background(), items, 0)
	if quit {
		t.Fatal("expected no quit")
	}
	if idx != 1 {
		t.Errorf("expected idx 1, got %d", idx)
	}
}

func TestHandleInputNavigateUpAndSelect(t *testing.T) {
	items := []item{
		{label: "first.txt"},
		{label: "second.txt"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	b, _ := newTestBrowser(nil, "b", "", false, "k\r") // k(up, clamped at 0) + Enter
	idx, quit, _ := b.handleInput(context.Background(), items, 1)
	if quit {
		t.Fatal("expected no quit")
	}
	if idx != 0 {
		t.Errorf("expected idx 0, got %d", idx)
	}
}

func TestHandleInputArrowKeys(t *testing.T) {
	items := []item{
		{label: "first.txt"},
		{label: "second.txt"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	// Down arrow (\x1b[B) then Enter
	b, _ := newTestBrowser(nil, "b", "", false, "\x1b[B\r")
	idx, quit, _ := b.handleInput(context.Background(), items, 0)
	if quit {
		t.Fatal("expected no quit")
	}
	if idx != 1 {
		t.Errorf("expected idx 1, got %d", idx)
	}
}

func TestHandleInputQuitWithQ(t *testing.T) {
	items := []item{
		{label: "file.txt"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	b, _ := newTestBrowser(nil, "b", "", false, "q")
	_, quit, _ := b.handleInput(context.Background(), items, 0)
	if !quit {
		t.Fatal("expected quit")
	}
}

func TestHandleInputQuitWithCtrlC(t *testing.T) {
	items := []item{
		{label: "file.txt"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	b, _ := newTestBrowser(nil, "b", "", false, "\x03") // Ctrl+C
	_, quit, _ := b.handleInput(context.Background(), items, 0)
	if !quit {
		t.Fatal("expected quit")
	}
}

func TestHandleInputFilterAndSelect(t *testing.T) {
	items := []item{
		{label: "alpha.txt"},
		{label: "beta.go"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	// "/" enters filter, "beta" filters, Enter exits filter, Enter selects
	b, _ := newTestBrowser(nil, "b", "", false, "/beta\r\r")
	idx, quit, _ := b.handleInput(context.Background(), items, 0)
	if quit {
		t.Fatal("expected no quit")
	}
	// After filtering "beta", visible = [1(beta.go), 2(quit nav)], cursor at 0 → index 1
	if idx != 1 {
		t.Errorf("expected idx 1, got %d", idx)
	}
}

func TestHandleInputFilterEscapeClearsFilter(t *testing.T) {
	items := []item{
		{label: "alpha.txt"},
		{label: "beta.go"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	// "/" enters filter, "x" filters (no match except nav), Escape clears, Enter selects first
	b, _ := newTestBrowser(nil, "b", "", false, "/x\x1b\r")
	idx, quit, _ := b.handleInput(context.Background(), items, 0)
	if quit {
		t.Fatal("expected no quit")
	}
	// After Escape clears filter, all items visible, cursor at 0
	if idx != 0 {
		t.Errorf("expected idx 0, got %d", idx)
	}
}

func TestHandleInputFilterBackspace(t *testing.T) {
	items := []item{
		{label: "alpha.txt"},
		{label: "beta.go"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	// "/" enters filter, "bx" types, backspace deletes "x", leaving "b" → matches beta.go
	// Enter exits filter, Enter selects
	b, _ := newTestBrowser(nil, "b", "", false, "/bx\x7f\r\r")
	idx, quit, _ := b.handleInput(context.Background(), items, 0)
	if quit {
		t.Fatal("expected no quit")
	}
	// Filter "b" matches "beta.go" + nav. cursor 0 → beta.go (idx 1)
	if idx != 1 {
		t.Errorf("expected idx 1, got %d", idx)
	}
}

func TestHandleInputEOFQuitsGracefully(t *testing.T) {
	items := []item{
		{label: "file.txt"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	b, _ := newTestBrowser(nil, "b", "", false, "") // empty input → EOF
	_, quit, _ := b.handleInput(context.Background(), items, 0)
	if !quit {
		t.Fatal("expected quit on EOF")
	}
}

func TestHandleInputCursorClampedAtBounds(t *testing.T) {
	items := []item{
		{label: "only.txt"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	// Try to go up (k) when already at 0, then down (j) past end, then select
	b, _ := newTestBrowser(nil, "b", "", false, "kkjjj\r")
	idx, quit, _ := b.handleInput(context.Background(), items, 0)
	if quit {
		t.Fatal("expected no quit")
	}
	// k×2 stays at 0, j×3 moves to 1 (clamped at len-1=1)
	if idx != 1 {
		t.Errorf("expected idx 1, got %d", idx)
	}
}

func TestRenderFilteredShowsHeader(t *testing.T) {
	items := []item{
		{label: "file.txt", entry: storage.ListEntry{Size: 1024}},
		{label: "quit", isNav: true, navID: "quit"},
	}

	b, out := newTestBrowser(nil, "mybucket", "path/", false, "")
	off := 0
	b.renderFiltered(items, allIndices(len(items)), 0, "", false, &off)

	output := out.String()
	if !strings.Contains(output, "mybucket") {
		t.Error("expected output to contain bucket name")
	}
	if !strings.Contains(output, "path/") {
		t.Error("expected output to contain prefix")
	}
	if !strings.Contains(output, "file.txt") {
		t.Error("expected output to contain file name")
	}
}

func TestRenderFilteredBucketListHeader(t *testing.T) {
	items := []item{
		{label: "mybucket", isDir: true},
		{label: "quit", isNav: true, navID: "quit"},
	}

	b, out := newTestBrowser(nil, "", "", true, "")
	off := 0
	b.renderFiltered(items, allIndices(len(items)), 0, "", false, &off)

	output := out.String()
	if !strings.Contains(output, "s3://") {
		t.Error("expected output to contain scheme-only header")
	}
}

func TestRenderFilteredEmptyDir(t *testing.T) {
	items := []item{
		{label: "quit", isNav: true, navID: "quit"},
	}

	b, out := newTestBrowser(nil, "b", "", false, "")
	off := 0
	b.renderFiltered(items, allIndices(len(items)), 0, "", false, &off)

	if !strings.Contains(out.String(), "(empty)") {
		t.Error("expected (empty) indicator")
	}
}

func TestRenderFilteredNoMatches(t *testing.T) {
	items := []item{
		{label: "file.txt"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	// Only nav items visible (simulating filter with no content matches)
	visible := []int{1} // only quit
	b, out := newTestBrowser(nil, "b", "", false, "")
	off := 0
	b.renderFiltered(items, visible, 0, "zzz", false, &off)

	if !strings.Contains(out.String(), "(no matches)") {
		t.Error("expected (no matches) indicator")
	}
}

func TestRenderFilteredShowsFilterBar(t *testing.T) {
	items := []item{
		{label: "file.txt"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	b, out := newTestBrowser(nil, "b", "", false, "")
	off := 0
	b.renderFiltered(items, allIndices(len(items)), 0, "test", true, &off)

	output := out.String()
	if !strings.Contains(output, "test") {
		t.Error("expected filter text in output")
	}
	if !strings.Contains(output, "type to filter") {
		t.Error("expected filter mode help text")
	}
}

func TestRunFileSelection(t *testing.T) {
	mock := storage.NewMockClient()
	mock.PutObject("mybucket", "file.txt", []byte("hello"), nil)

	// Enter selects first item (file.txt)
	b, _ := newTestBrowser(mock, "mybucket", "", false, "\r")
	sel, err := b.runLoop(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Action != ActionEdit {
		t.Fatalf("expected ActionEdit, got %d", sel.Action)
	}
	if sel.URI.Bucket != "mybucket" {
		t.Errorf("expected bucket %q, got %q", "mybucket", sel.URI.Bucket)
	}
	if sel.URI.Key != "file.txt" {
		t.Errorf("expected key %q, got %q", "file.txt", sel.URI.Key)
	}
}

func TestRunQuit(t *testing.T) {
	mock := storage.NewMockClient()
	mock.PutObject("mybucket", "file.txt", []byte("hello"), nil)

	b, _ := newTestBrowser(mock, "mybucket", "", false, "q")
	sel, err := b.runLoop(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Action != ActionQuit {
		t.Fatalf("expected ActionQuit, got %d", sel.Action)
	}
}

func TestRunBucketListSelection(t *testing.T) {
	mock := storage.NewMockClient()
	mock.SetBuckets([]string{"alpha", "bravo"})
	mock.PutObject("bravo", "file.txt", []byte("hello"), nil)

	// Down arrow to "bravo" + Enter selects bucket, then Enter selects file
	b, _ := newTestBrowser(mock, "", "", true, "j\r\r")
	sel, err := b.runLoop(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Action != ActionEdit {
		t.Fatalf("expected ActionEdit, got %d", sel.Action)
	}
	if sel.URI.Bucket != "bravo" {
		t.Errorf("expected bucket %q, got %q", "bravo", sel.URI.Bucket)
	}
}

func TestRunBucketListQuit(t *testing.T) {
	mock := storage.NewMockClient()
	mock.SetBuckets([]string{"mybucket"})

	b, _ := newTestBrowser(mock, "", "", true, "q")
	sel, err := b.runLoop(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Action != ActionQuit {
		t.Fatalf("expected ActionQuit, got %d", sel.Action)
	}
}

func TestRunDirNavigation(t *testing.T) {
	mock := storage.NewMockClient()
	mock.PutObject("mybucket", "dir/file.txt", []byte("hello"), nil)

	// Enter selects "dir/" (directory), then Enter selects "file.txt"
	b, _ := newTestBrowser(mock, "mybucket", "", false, "\r\r")
	sel, err := b.runLoop(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Action != ActionEdit {
		t.Fatalf("expected ActionEdit, got %d", sel.Action)
	}
	if sel.URI.Key != "dir/file.txt" {
		t.Errorf("expected key %q, got %q", "dir/file.txt", sel.URI.Key)
	}
}

func TestFilterIndices(t *testing.T) {
	items := []item{
		{label: "docs/", isDir: true},
		{label: "config.yaml"},
		{label: "README.md"},
		{label: "main.go"},
		{label: "..", isNav: true, navID: "up"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	tests := []struct {
		name   string
		filter string
		want   []int
	}{
		{"empty filter returns all", "", []int{0, 1, 2, 3, 4, 5}},
		{"matches file", "config", []int{1, 4, 5}},
		{"case insensitive", "readme", []int{2, 4, 5}},
		{"matches dir", "docs", []int{0, 4, 5}},
		{"no match keeps nav", "zzz", []int{4, 5}},
		{"partial match", ".go", []int{3, 4, 5}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterIndices(items, tt.filter)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("filterIndices(%q): got %v, want %v", tt.filter, got, tt.want)
			}
		})
	}
}

func TestGoUp(t *testing.T) {
	tests := []struct {
		name              string
		bucket            string
		prefix            string
		bucketListEnabled bool
		expectedBucket    string
		expectedPrefix    string
	}{
		{"root stays root", "b", "", false, "b", ""},
		{"one level up to root", "b", "dir/", false, "b", ""},
		{"nested to parent", "b", "a/b/c/", false, "b", "a/b/"},
		{"root to bucket list", "b", "", true, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Browser{bucket: tt.bucket, prefix: tt.prefix, bucketListEnabled: tt.bucketListEnabled}
			b.goUp()
			if b.bucket != tt.expectedBucket {
				t.Errorf("goUp bucket: got %q, want %q", b.bucket, tt.expectedBucket)
			}
			if b.prefix != tt.expectedPrefix {
				t.Errorf("goUp prefix: got %q, want %q", b.prefix, tt.expectedPrefix)
			}
		})
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		size     int64
		expected string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := formatSize(tt.size)
			if got != tt.expected {
				t.Errorf("formatSize(%d): got %q, want %q", tt.size, got, tt.expected)
			}
		})
	}
}

func TestBuildTreeItems(t *testing.T) {
	topEntries := []storage.ListEntry{
		{Key: "image/", IsDir: true},
		{Key: "prod/", IsDir: true},
		{Key: "readme.txt", Size: 100},
	}
	childEntries := []storage.ListEntry{
		{Key: "image/thumbnails/", IsDir: true},
		{Key: "image/logo.png", Size: 12800},
	}

	b := &Browser{
		prefix:     "",
		expanded:   map[string]bool{"image/": true},
		childCache: map[string][]storage.ListEntry{"image/": childEntries},
	}
	items := b.buildItems(topEntries)

	// Expected: image/(depth=0), thumbnails/(depth=1), logo.png(depth=1), prod/(depth=0), readme.txt(depth=0), quit
	if len(items) != 6 {
		t.Fatalf("expected 6 items, got %d", len(items))
	}
	if items[0].label != "image/" || items[0].depth != 0 {
		t.Errorf("item[0]: got %+v", items[0])
	}
	if items[1].label != "thumbnails/" || items[1].depth != 1 {
		t.Errorf("item[1]: got %+v", items[1])
	}
	if items[2].label != "logo.png" || items[2].depth != 1 {
		t.Errorf("item[2]: got %+v", items[2])
	}
	if items[3].label != "prod/" || items[3].depth != 0 {
		t.Errorf("item[3]: got %+v", items[3])
	}
	if items[4].label != "readme.txt" || items[4].depth != 0 {
		t.Errorf("item[4]: got %+v", items[4])
	}
	if !items[5].isNav || items[5].navID != "quit" {
		t.Errorf("item[5]: expected quit nav, got %+v", items[5])
	}
}

func TestBuildTreeItemsNested(t *testing.T) {
	topEntries := []storage.ListEntry{
		{Key: "a/", IsDir: true},
	}
	childA := []storage.ListEntry{
		{Key: "a/b/", IsDir: true},
	}
	childB := []storage.ListEntry{
		{Key: "a/b/file.txt", Size: 50},
	}

	b := &Browser{
		prefix:   "",
		expanded: map[string]bool{"a/": true, "a/b/": true},
		childCache: map[string][]storage.ListEntry{
			"a/":   childA,
			"a/b/": childB,
		},
	}
	items := b.buildItems(topEntries)

	// a/(0), b/(1), file.txt(2), quit
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}
	if items[0].depth != 0 || items[0].label != "a/" {
		t.Errorf("item[0]: got %+v", items[0])
	}
	if items[1].depth != 1 || items[1].label != "b/" {
		t.Errorf("item[1]: got %+v", items[1])
	}
	if items[2].depth != 2 || items[2].label != "file.txt" {
		t.Errorf("item[2]: got %+v", items[2])
	}
}

func TestHandleInputExpandDir(t *testing.T) {
	mock := storage.NewMockClient()
	mock.PutObject("mybucket", "dir/file.txt", []byte("hello"), nil)

	// Right arrow (\x1b[C) on dir/ expands it, then Enter selects dir/
	b, _ := newTestBrowser(mock, "mybucket", "", false, "\x1b[C\r")

	entries := []storage.ListEntry{
		{Key: "dir/", IsDir: true},
		{Key: "readme.txt", Size: 100},
	}
	items := b.buildItems(entries)
	idx, quit, _ := b.handleInput(context.Background(), items, 0)
	if quit {
		t.Fatal("expected no quit")
	}
	// After expand, items are rebuilt. dir/ is still at index 0.
	if idx != 0 {
		t.Errorf("expected idx 0, got %d", idx)
	}
	if !b.expanded["dir/"] {
		t.Error("expected dir/ to be expanded")
	}
	if _, ok := b.childCache["dir/"]; !ok {
		t.Error("expected dir/ children to be cached")
	}
}

func TestHandleInputCollapseDir(t *testing.T) {
	mock := storage.NewMockClient()
	mock.PutObject("mybucket", "dir/file.txt", []byte("hello"), nil)

	// Left arrow (\x1b[D) on expanded dir/ collapses it, then Enter
	b, _ := newTestBrowser(mock, "mybucket", "", false, "\x1b[D\r")
	b.expanded["dir/"] = true
	b.childCache["dir/"] = []storage.ListEntry{
		{Key: "dir/file.txt", Size: 5},
	}

	entries := []storage.ListEntry{
		{Key: "dir/", IsDir: true},
		{Key: "readme.txt", Size: 100},
	}
	items := b.buildItems(entries)
	_, quit, _ := b.handleInput(context.Background(), items, 0)
	if quit {
		t.Fatal("expected no quit")
	}
	if b.expanded["dir/"] {
		t.Error("expected dir/ to be collapsed")
	}
}

func TestHandleInputLeftArrowOnChild(t *testing.T) {
	mock := storage.NewMockClient()
	mock.PutObject("mybucket", "dir/file.txt", []byte("hello"), nil)

	// Cursor starts at index 1 (child item at depth 1).
	// Left arrow should move cursor to parent (index 0), then Enter selects.
	b, _ := newTestBrowser(mock, "mybucket", "", false, "\x1b[D\r")
	b.expanded["dir/"] = true
	b.childCache["dir/"] = []storage.ListEntry{
		{Key: "dir/file.txt", Size: 5},
	}

	entries := []storage.ListEntry{
		{Key: "dir/", IsDir: true},
	}
	items := b.buildItems(entries)
	// items: dir/(depth=0), file.txt(depth=1), quit
	if len(items) < 2 {
		t.Fatalf("expected at least 2 items, got %d", len(items))
	}

	idx, quit, _ := b.handleInput(context.Background(), items, 1)
	if quit {
		t.Fatal("expected no quit")
	}
	// Left arrow on child (depth=1) should move to parent dir/(index 0)
	if idx != 0 {
		t.Errorf("expected idx 0 (parent), got %d", idx)
	}
}

func TestHandleInputEmacsCtrlN(t *testing.T) {
	items := []item{
		{label: "first.txt"},
		{label: "second.txt"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	// Ctrl+N (0x0e) = down, then Enter
	b, _ := newTestBrowser(nil, "b", "", false, "\x0e\r")
	idx, quit, _ := b.handleInput(context.Background(), items, 0)
	if quit {
		t.Fatal("expected no quit")
	}
	if idx != 1 {
		t.Errorf("expected idx 1, got %d", idx)
	}
}

func TestHandleInputEmacsCtrlP(t *testing.T) {
	items := []item{
		{label: "first.txt"},
		{label: "second.txt"},
		{label: "quit", isNav: true, navID: "quit"},
	}

	// Ctrl+P (0x10) = up, then Enter
	b, _ := newTestBrowser(nil, "b", "", false, "\x10\r")
	idx, quit, _ := b.handleInput(context.Background(), items, 1)
	if quit {
		t.Fatal("expected no quit")
	}
	if idx != 0 {
		t.Errorf("expected idx 0, got %d", idx)
	}
}

func TestHandleInputEmacsCtrlFExpand(t *testing.T) {
	mock := storage.NewMockClient()
	mock.PutObject("mybucket", "dir/file.txt", []byte("hello"), nil)

	// Ctrl+F (0x06) on dir/ expands it, then Enter
	b, _ := newTestBrowser(mock, "mybucket", "", false, "\x06\r")
	entries := []storage.ListEntry{
		{Key: "dir/", IsDir: true},
		{Key: "readme.txt", Size: 100},
	}
	items := b.buildItems(entries)
	idx, quit, _ := b.handleInput(context.Background(), items, 0)
	if quit {
		t.Fatal("expected no quit")
	}
	if idx != 0 {
		t.Errorf("expected idx 0, got %d", idx)
	}
	if !b.expanded["dir/"] {
		t.Error("expected dir/ to be expanded")
	}
}

func TestHandleInputEmacsCtrlBCollapse(t *testing.T) {
	mock := storage.NewMockClient()
	mock.PutObject("mybucket", "dir/file.txt", []byte("hello"), nil)

	// Ctrl+B (0x02) on expanded dir/ collapses it, then Enter
	b, _ := newTestBrowser(mock, "mybucket", "", false, "\x02\r")
	b.expanded["dir/"] = true
	b.childCache["dir/"] = []storage.ListEntry{
		{Key: "dir/file.txt", Size: 5},
	}
	entries := []storage.ListEntry{
		{Key: "dir/", IsDir: true},
		{Key: "readme.txt", Size: 100},
	}
	items := b.buildItems(entries)
	_, quit, _ := b.handleInput(context.Background(), items, 0)
	if quit {
		t.Fatal("expected no quit")
	}
	if b.expanded["dir/"] {
		t.Error("expected dir/ to be collapsed")
	}
}

func TestComputeTreePrefixes(t *testing.T) {
	// Simulates:
	//   ├── ▶ dev/
	//   ├── ▼ image/
	//   │   ├── ▶ thumbnails/
	//   │   └── logo.png
	//   ├── ▶ prod/
	//   └── ▶ stage/
	//   ..
	//   quit
	items := []item{
		{label: "dev/", isDir: true, depth: 0},
		{label: "image/", isDir: true, depth: 0},
		{label: "thumbnails/", isDir: true, depth: 1},
		{label: "logo.png", depth: 1},
		{label: "prod/", isDir: true, depth: 0},
		{label: "stage/", isDir: true, depth: 0},
		{label: "..", isNav: true, navID: "up"},
		{label: "quit", isNav: true, navID: "quit"},
	}
	visible := allIndices(len(items))
	prefixes := computeTreePrefixes(items, visible)

	want := []string{
		"├── ",           // dev/
		"├── ",           // image/
		"│   ├── ",       // thumbnails/
		"│   └── ",       // logo.png
		"├── ",           // prod/
		"└── ",           // stage/
		"",               // ..
		"",               // quit
	}

	if len(prefixes) != len(want) {
		t.Fatalf("expected %d prefixes, got %d", len(want), len(prefixes))
	}
	for i, w := range want {
		if prefixes[i] != w {
			t.Errorf("prefix[%d] (%s): got %q, want %q", i, items[i].label, prefixes[i], w)
		}
	}
}

func TestScrollViewport(t *testing.T) {
	// 10 files + quit = 11 items, terminal height = 10
	// overhead = header(2) + blank(1) + help(2) = 5, so maxRows = 5
	var items []item
	for i := 0; i < 10; i++ {
		items = append(items, item{label: fmt.Sprintf("file%d.txt", i)})
	}
	items = append(items, item{label: "quit", isNav: true, navID: "quit"})

	b, out := newTestBrowser(nil, "b", "", false, "")
	b.heightOverride = 10

	// Cursor at 0, scrollOffset starts at 0
	off := 0
	b.renderFiltered(items, allIndices(len(items)), 0, "", false, &off)
	output := out.String()

	// Should show "more below" indicator
	if !strings.Contains(output, "more below") {
		t.Error("expected 'more below' indicator")
	}
	// file0.txt should be visible
	if !strings.Contains(output, "file0.txt") {
		t.Error("expected file0.txt to be visible")
	}

	// Cursor at 8 (near bottom), should scroll
	out.Reset()
	off = 0
	b.renderFiltered(items, allIndices(len(items)), 8, "", false, &off)
	output = out.String()

	// Should show "more above" indicator
	if !strings.Contains(output, "more above") {
		t.Error("expected 'more above' indicator")
	}
	// file8.txt should be visible
	if !strings.Contains(output, "file8.txt") {
		t.Error("expected file8.txt to be visible")
	}
}

func TestComputeTreePrefixesNested(t *testing.T) {
	// Simulates:
	//   └── ▼ a/
	//       └── ▼ b/
	//           └── file.txt
	//   quit
	items := []item{
		{label: "a/", isDir: true, depth: 0},
		{label: "b/", isDir: true, depth: 1},
		{label: "file.txt", depth: 2},
		{label: "quit", isNav: true, navID: "quit"},
	}
	visible := allIndices(len(items))
	prefixes := computeTreePrefixes(items, visible)

	want := []string{
		"└── ",           // a/
		"    └── ",       // b/
		"        └── ",   // file.txt
		"",               // quit
	}

	for i, w := range want {
		if prefixes[i] != w {
			t.Errorf("prefix[%d] (%s): got %q, want %q", i, items[i].label, prefixes[i], w)
		}
	}
}
