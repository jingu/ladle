// Package storage defines the interface for cloud storage backends.
package storage

import (
	"context"
	"io"
	"time"
)

// ObjectMetadata holds standard and user-defined metadata for a storage object.
type ObjectMetadata struct {
	ContentType        string
	CacheControl       string
	ContentEncoding    string
	ContentDisposition string
	Metadata           map[string]string
}

// ListEntry represents a single item in a storage listing.
type ListEntry struct {
	Key          string
	IsDir        bool
	Size         int64
	LastModified time.Time
}

// Client defines the operations required for a cloud storage backend.
type Client interface {
	// Download retrieves an object and writes it to the given writer.
	Download(ctx context.Context, bucket, key string, w io.Writer) error

	// Upload reads from the given reader and writes to the object.
	Upload(ctx context.Context, bucket, key string, r io.Reader, meta *ObjectMetadata) error

	// HeadObject retrieves the metadata for an object.
	HeadObject(ctx context.Context, bucket, key string) (*ObjectMetadata, error)

	// UpdateMetadata updates only the metadata of an object without re-uploading the body.
	UpdateMetadata(ctx context.Context, bucket, key string, meta *ObjectMetadata) error

	// List lists objects under the given prefix. If delimiter is non-empty,
	// it groups results by that delimiter (typically "/").
	List(ctx context.Context, bucket, prefix, delimiter string) ([]ListEntry, error)

	// ListBuckets lists all accessible buckets.
	ListBuckets(ctx context.Context) ([]string, error)
}
