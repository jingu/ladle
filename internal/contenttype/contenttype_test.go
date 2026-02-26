package contenttype

import "testing"

func TestDetect(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"file.html", "text/html"},
		{"file.css", "text/css"},
		{"file.js", "application/javascript"},
		{"file.json", "application/json"},
		{"file.yaml", "application/x-yaml"},
		{"file.png", "image/png"},
		{"file.jpg", "image/jpeg"},
		{"file.pdf", "application/pdf"},
		{"file.svg", "image/svg+xml"},
		{"file.wasm", "application/wasm"},
		{"file.txt", "text/plain"},
		{"file", "application/octet-stream"},
		{"file.unknownext", "application/octet-stream"},
		{"path/to/file.HTML", "text/html"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := Detect(tt.filename)
			if got != tt.expected {
				t.Errorf("Detect(%q): got %q, want %q", tt.filename, got, tt.expected)
			}
		})
	}
}
