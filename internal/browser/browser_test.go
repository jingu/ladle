package browser

import (
	"reflect"
	"testing"
)

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
