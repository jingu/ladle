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
