package meta

import (
	"strings"
	"testing"

	"github.com/jingu/ladle/internal/storage"
)

func TestMarshalUnmarshal(t *testing.T) {
	original := &storage.ObjectMetadata{
		ContentType:  "text/html",
		CacheControl: "max-age=3600",
		Metadata: map[string]string{
			"author":  "yoshitaka",
			"version": "1.0",
		},
	}

	data, err := Marshal("s3://bucket/file.html", original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	str := string(data)
	if !strings.HasPrefix(str, "# s3://bucket/file.html\n") {
		t.Errorf("expected comment header, got:\n%s", str)
	}

	parsed, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if parsed.ContentType != original.ContentType {
		t.Errorf("ContentType: got %q, want %q", parsed.ContentType, original.ContentType)
	}
	if parsed.CacheControl != original.CacheControl {
		t.Errorf("CacheControl: got %q, want %q", parsed.CacheControl, original.CacheControl)
	}
	if parsed.Metadata["author"] != "yoshitaka" {
		t.Errorf("Metadata[author]: got %q, want %q", parsed.Metadata["author"], "yoshitaka")
	}
}

func TestUnmarshalEmpty(t *testing.T) {
	data := []byte(`ContentType: ""
CacheControl: ""
ContentEncoding: ""
ContentDisposition: ""
Metadata: {}
`)
	meta, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if meta.ContentType != "" {
		t.Errorf("expected empty ContentType, got %q", meta.ContentType)
	}
}
