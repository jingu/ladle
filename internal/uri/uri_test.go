package uri

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		input   string
		scheme  Scheme
		bucket  string
		key     string
		isDir   bool
		wantErr bool
	}{
		{"s3://mybucket/path/to/file.html", SchemeS3, "mybucket", "path/to/file.html", false, false},
		{"s3://mybucket/path/to/", SchemeS3, "mybucket", "path/to/", true, false},
		{"s3://mybucket/", SchemeS3, "mybucket", "", true, false},
		{"s3://mybucket", SchemeS3, "mybucket", "", true, false},
		{"gs://mybucket/file.txt", SchemeGCS, "mybucket", "file.txt", false, false},
		{"az://container/blob.txt", SchemeAzure, "container", "blob.txt", false, false},
		{"r2://mybucket/file.txt", SchemeR2, "mybucket", "file.txt", false, false},
		// Bare scheme names (without ://)
		{"s3", SchemeS3, "", "", true, false},
		{"gs", SchemeGCS, "", "", true, false},
		{"az", SchemeAzure, "", "", true, false},
		{"r2", SchemeR2, "", "", true, false},
		{"invalid", "", "", "", false, true},
		{"http://example.com", "", "", "", false, true},
		{"s3://", SchemeS3, "", "", true, false},
		{"s3:///path", "", "", "", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			u, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.input, err)
			}
			if u.Scheme != tt.scheme {
				t.Errorf("scheme: got %q, want %q", u.Scheme, tt.scheme)
			}
			if u.Bucket != tt.bucket {
				t.Errorf("bucket: got %q, want %q", u.Bucket, tt.bucket)
			}
			if u.Key != tt.key {
				t.Errorf("key: got %q, want %q", u.Key, tt.key)
			}
			if u.IsDirectory() != tt.isDir {
				t.Errorf("isDir: got %v, want %v", u.IsDirectory(), tt.isDir)
			}
		})
	}
}

func TestIsBucketList(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"s3://", true},
		{"s3", true},
		{"s3://mybucket", false},
		{"s3://mybucket/key", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			u, err := Parse(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if got := u.IsBucketList(); got != tt.want {
				t.Errorf("IsBucketList(): got %v, want %v", got, tt.want)
			}
		})
	}
}
