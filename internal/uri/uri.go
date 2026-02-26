// Package uri provides cloud storage URI parsing.
package uri

import (
	"fmt"
	"strings"
)

// Scheme represents a cloud storage provider scheme.
type Scheme string

const (
	SchemeS3    Scheme = "s3"
	SchemeGCS   Scheme = "gs"
	SchemeAzure Scheme = "az"
	SchemeR2    Scheme = "r2"
)

// URI represents a parsed cloud storage URI.
type URI struct {
	Scheme Scheme
	Bucket string
	Key    string
	Raw    string
}

// IsDirectory returns true if the URI points to a directory (ends with /).
func (u *URI) IsDirectory() bool {
	return u.Key == "" || strings.HasSuffix(u.Key, "/")
}

// IsBucketList returns true if the URI has no bucket (e.g. "s3://").
func (u *URI) IsBucketList() bool {
	return u.Bucket == ""
}

// String returns the original URI string.
func (u *URI) String() string {
	return u.Raw
}

// Parse parses a cloud storage URI like s3://bucket/key.
func Parse(raw string) (*URI, error) {
	idx := strings.Index(raw, "://")
	if idx < 0 {
		return nil, fmt.Errorf("invalid URI %q: missing scheme (expected s3://, gs://, az://, r2://)", raw)
	}

	scheme := Scheme(raw[:idx])
	switch scheme {
	case SchemeS3, SchemeGCS, SchemeAzure, SchemeR2:
		// OK
	default:
		return nil, fmt.Errorf("unsupported scheme %q: expected s3, gs, az, or r2", scheme)
	}

	rest := raw[idx+3:]

	var bucket, key string
	if rest == "" {
		// scheme:// with no bucket — bucket list mode
	} else {
		slashIdx := strings.Index(rest, "/")
		if slashIdx < 0 {
			bucket = rest
		} else {
			bucket = rest[:slashIdx]
			key = rest[slashIdx+1:]
		}
		if bucket == "" {
			return nil, fmt.Errorf("invalid URI %q: empty bucket name", raw)
		}
	}

	return &URI{
		Scheme: scheme,
		Bucket: bucket,
		Key:    key,
		Raw:    raw,
	}, nil
}
