package browser

import (
	"path"
	"strings"
)

const (
	iconDir     = "📁"
	iconBucket  = "🪣"
	iconFile    = "📄"
	iconText    = "📝"
	iconImage   = "🖼️"
	iconArchive = "📦"
)

var textExts = map[string]bool{
	".txt": true, ".md": true, ".csv": true,
	".html": true, ".htm": true, ".css": true, ".js": true, ".ts": true,
	".jsx": true, ".tsx": true, ".json": true, ".xml": true, ".yaml": true,
	".yml": true, ".toml": true, ".vue": true, ".svelte": true,
}

var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".svg": true, ".webp": true, ".ico": true, ".bmp": true, ".tiff": true,
}

var archiveExts = map[string]bool{
	".zip": true, ".tar": true, ".gz": true, ".tgz": true,
	".rar": true, ".7z": true, ".exe": true, ".bin": true,
}

func iconForEntry(name string, isDir, isBucket bool) string {
	if isBucket {
		return iconBucket
	}
	if isDir {
		return iconDir
	}
	ext := strings.ToLower(path.Ext(name))
	switch {
	case textExts[ext]:
		return iconText
	case imageExts[ext]:
		return iconImage
	case archiveExts[ext]:
		return iconArchive
	default:
		return iconFile
	}
}
