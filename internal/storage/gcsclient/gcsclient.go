// Package gcsclient implements the storage.Client interface for Google Cloud Storage.
//
// Mapping to the storage.Client model:
//   - bucket → GCS bucket
//   - key    → GCS object name
//
// Credentials are resolved via Application Default Credentials (ADC):
//   - GOOGLE_APPLICATION_CREDENTIALS pointing at a service account key file
//   - `gcloud auth application-default login`
//   - the metadata server when running on GCP
//
// Use --no-sign-request for anonymous access to public buckets, and
// --endpoint-url (or STORAGE_EMULATOR_HOST) for the fake-gcs-server emulator.
//
// ListBuckets requires a project ID, resolved from Options.Project or the
// GOOGLE_CLOUD_PROJECT / GOOGLE_PROJECT / CLOUDSDK_CORE_PROJECT environment
// variables. All other operations work without a project ID.
package gcsclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"cloud.google.com/go/storage"
	ladlestorage "github.com/jingu/ladle/internal/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// Options holds configuration for creating a GCS client.
type Options struct {
	// Project is the GCP project ID used by ListBuckets. Falls back to the
	// GOOGLE_CLOUD_PROJECT / GOOGLE_PROJECT / CLOUDSDK_CORE_PROJECT env vars.
	Project string
	// EndpointURL overrides the storage endpoint (e.g. for the fake-gcs-server emulator).
	EndpointURL string
	// NoSignRequest disables authentication for anonymous access to public buckets.
	NoSignRequest bool
}

// GCSClient implements storage.Client for Google Cloud Storage.
type GCSClient struct {
	client  *storage.Client
	project string
}

var _ ladlestorage.Client = (*GCSClient)(nil)

// New creates a new GCSClient using Application Default Credentials.
func New(ctx context.Context, opts Options) (*GCSClient, error) {
	var clientOpts []option.ClientOption
	if opts.EndpointURL != "" {
		clientOpts = append(clientOpts, option.WithEndpoint(opts.EndpointURL))
	}
	if opts.NoSignRequest {
		clientOpts = append(clientOpts, option.WithoutAuthentication())
	}

	client, err := storage.NewClient(ctx, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("gcs: creating client: %w", err)
	}

	project := opts.Project
	if project == "" {
		for _, env := range []string{"GOOGLE_CLOUD_PROJECT", "GOOGLE_PROJECT", "CLOUDSDK_CORE_PROJECT"} {
			if v := os.Getenv(env); v != "" {
				project = v
				break
			}
		}
	}

	return &GCSClient{client: client, project: project}, nil
}

func (c *GCSClient) object(bucket, key string) *storage.ObjectHandle {
	return c.client.Bucket(bucket).Object(key)
}

func (c *GCSClient) Download(ctx context.Context, bucket, key string, w io.Writer) error {
	r, err := c.object(bucket, key).NewReader(ctx)
	if err != nil {
		return fmt.Errorf("downloading gs://%s/%s: %w", bucket, key, err)
	}
	defer func() { _ = r.Close() }()

	if _, err := io.Copy(w, r); err != nil {
		return fmt.Errorf("reading gs://%s/%s: %w", bucket, key, err)
	}
	return nil
}

func (c *GCSClient) Upload(ctx context.Context, bucket, key string, r io.Reader, meta *ladlestorage.ObjectMetadata) error {
	w := c.object(bucket, key).NewWriter(ctx)
	applyMetadata(w, meta)

	if _, err := io.Copy(w, r); err != nil {
		_ = w.Close()
		return fmt.Errorf("uploading to gs://%s/%s: %w", bucket, key, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("uploading to gs://%s/%s: %w", bucket, key, err)
	}
	return nil
}

func (c *GCSClient) HeadObject(ctx context.Context, bucket, key string) (*ladlestorage.ObjectMetadata, error) {
	attrs, err := c.object(bucket, key).Attrs(ctx)
	if err != nil {
		return nil, fmt.Errorf("head gs://%s/%s: %w", bucket, key, err)
	}
	return &ladlestorage.ObjectMetadata{
		ContentType:        attrs.ContentType,
		CacheControl:       attrs.CacheControl,
		ContentEncoding:    attrs.ContentEncoding,
		ContentDisposition: attrs.ContentDisposition,
		Metadata:           copyMeta(attrs.Metadata),
	}, nil
}

func (c *GCSClient) UpdateMetadata(ctx context.Context, bucket, key string, meta *ladlestorage.ObjectMetadata) error {
	update := storage.ObjectAttrsToUpdate{
		ContentType:        meta.ContentType,
		CacheControl:       meta.CacheControl,
		ContentEncoding:    meta.ContentEncoding,
		ContentDisposition: meta.ContentDisposition,
		Metadata:           meta.Metadata,
	}
	if _, err := c.object(bucket, key).Update(ctx, update); err != nil {
		return fmt.Errorf("updating metadata for gs://%s/%s: %w", bucket, key, err)
	}
	return nil
}

func (c *GCSClient) List(ctx context.Context, bucket, prefix, delimiter string) ([]ladlestorage.ListEntry, error) {
	it := c.client.Bucket(bucket).Objects(ctx, &storage.Query{
		Prefix:    prefix,
		Delimiter: delimiter,
	})

	var entries []ladlestorage.ListEntry
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing gs://%s/%s: %w", bucket, prefix, err)
		}
		// A synthetic "directory" result carries only Prefix; real objects carry Name.
		if attrs.Prefix != "" {
			entries = append(entries, ladlestorage.ListEntry{Key: attrs.Prefix, IsDir: true})
			continue
		}
		entries = append(entries, ladlestorage.ListEntry{
			Key:          attrs.Name,
			Size:         attrs.Size,
			LastModified: attrs.Updated,
		})
	}
	return entries, nil
}

func (c *GCSClient) ListBuckets(ctx context.Context) ([]string, error) {
	if c.project == "" {
		return nil, fmt.Errorf("gcs: listing buckets requires a project ID: set --project or GOOGLE_CLOUD_PROJECT")
	}
	it := c.client.Buckets(ctx, c.project)
	var names []string
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing buckets: %w", err)
		}
		names = append(names, attrs.Name)
	}
	return names, nil
}

func (c *GCSClient) Delete(ctx context.Context, bucket, key string) error {
	if err := c.object(bucket, key).Delete(ctx); err != nil {
		return fmt.Errorf("deleting gs://%s/%s: %w", bucket, key, err)
	}
	return nil
}

func (c *GCSClient) Copy(ctx context.Context, bucket, srcKey, dstKey string) error {
	src := c.object(bucket, srcKey)
	dst := c.object(bucket, dstKey)
	if _, err := dst.CopierFrom(src).Run(ctx); err != nil {
		return fmt.Errorf("copying gs://%s/%s to gs://%s/%s: %w", bucket, srcKey, bucket, dstKey, err)
	}
	return nil
}

func (c *GCSClient) ListVersions(ctx context.Context, bucket, key string) ([]ladlestorage.ObjectVersion, error) {
	it := c.client.Bucket(bucket).Objects(ctx, &storage.Query{
		Prefix:   key,
		Versions: true,
	})

	var versions []ladlestorage.ObjectVersion
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("listing versions for gs://%s/%s: %w", bucket, key, err)
		}
		if attrs.Name != key {
			continue
		}
		versions = append(versions, ladlestorage.ObjectVersion{
			// GCS identifies versions by generation number.
			VersionID: fmt.Sprintf("%d", attrs.Generation),
			// The live generation has no deletion timestamp.
			IsLatest:     attrs.Deleted.IsZero(),
			Size:         attrs.Size,
			LastModified: attrs.Updated,
		})
	}

	// GCS lists generations oldest-first; the browser UI expects newest-first
	// with the current version at the top.
	sortVersionsNewestFirst(versions)
	return versions, nil
}

func (c *GCSClient) DownloadVersion(ctx context.Context, bucket, key, versionID string, w io.Writer) error {
	gen, err := parseGeneration(versionID)
	if err != nil {
		return fmt.Errorf("downloading gs://%s/%s (version %s): %w", bucket, key, versionID, err)
	}
	r, err := c.object(bucket, key).Generation(gen).NewReader(ctx)
	if err != nil {
		return fmt.Errorf("downloading gs://%s/%s (version %s): %w", bucket, key, versionID, err)
	}
	defer func() { _ = r.Close() }()

	if _, err := io.Copy(w, r); err != nil {
		return fmt.Errorf("reading gs://%s/%s (version %s): %w", bucket, key, versionID, err)
	}
	return nil
}
