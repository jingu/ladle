// Package contenttype detects MIME types from file extensions.
package contenttype

import (
	"mime"
	"path/filepath"
	"strings"
)

// extra mappings not always present in Go's mime package
var extraTypes = map[string]string{
	".yaml": "application/x-yaml",
	".yml":  "application/x-yaml",
	".json": "application/json",
	".html": "text/html",
	".htm":  "text/html",
	".css":  "text/css",
	".js":   "application/javascript",
	".mjs":  "application/javascript",
	".ts":   "text/typescript",
	".tsx":  "text/tsx",
	".jsx":  "text/jsx",
	".xml":  "application/xml",
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".pdf":  "application/pdf",
	".zip":  "application/zip",
	".gz":   "application/gzip",
	".tar":  "application/x-tar",
	".wasm": "application/wasm",
	".md":   "text/markdown",
	".txt":  "text/plain",
	".csv":  "text/csv",
	".toml": "application/toml",
	".woff": "font/woff",
	".woff2": "font/woff2",
	".ttf":  "font/ttf",
	".otf":  "font/otf",
	".ico":  "image/x-icon",
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mp3":  "audio/mpeg",
	".ogg":  "audio/ogg",
}

// Detect returns the MIME type for the given filename based on its extension.
// Returns "application/octet-stream" if unknown.
func Detect(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		return "application/octet-stream"
	}

	// Check extra types first
	if ct, ok := extraTypes[ext]; ok {
		return ct
	}

	// Fall back to Go's mime package
	ct := mime.TypeByExtension(ext)
	if ct != "" {
		return ct
	}

	return "application/octet-stream"
}
