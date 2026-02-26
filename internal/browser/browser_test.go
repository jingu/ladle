package browser

import (
	"bytes"
	"context"
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
	}, out
}

func TestBuildItems(t *testing.T) {
	entries := []storage.ListEntry{
		{Key: "dir/", IsDir: true},
		{Key: "file.txt", Size: 100},
	}

	b := &Browser{prefix: "", bucketListEnabled: false}
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

	b := &Browser{prefix: "dir/", bucketListEnabled: false}
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

	b := &Browser{prefix: "", bucketListEnabled: true}
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

	b := &Browser{prefix: "dir/", bucketListEnabled: false}
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
	idx, quit := b.handleInput(items, 0)
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
	idx, quit := b.handleInput(items, 0)
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
	idx, quit := b.handleInput(items, 1)
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
	idx, quit := b.handleInput(items, 0)
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
	_, quit := b.handleInput(items, 0)
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
	_, quit := b.handleInput(items, 0)
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
	idx, quit := b.handleInput(items, 0)
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
	idx, quit := b.handleInput(items, 0)
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
	idx, quit := b.handleInput(items, 0)
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
	_, quit := b.handleInput(items, 0)
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
	idx, quit := b.handleInput(items, 0)
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
	b.renderFiltered(items, allIndices(len(items)), 0, "", false)

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
	b.renderFiltered(items, allIndices(len(items)), 0, "", false)

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
	b.renderFiltered(items, allIndices(len(items)), 0, "", false)

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
	b.renderFiltered(items, visible, 0, "zzz", false)

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
	b.renderFiltered(items, allIndices(len(items)), 0, "test", true)

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
