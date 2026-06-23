// Package azblobclient implements the storage.Client interface for Azure Blob Storage.
//
// Mapping to the storage.Client model:
//   - bucket → Azure Blob "container"
//   - key    → Azure "blob"
//
// The storage account and credentials are resolved (in priority order) from:
//  1. AZURE_STORAGE_CONNECTION_STRING
//  2. account name (Options.Account or AZURE_STORAGE_ACCOUNT) + AZURE_STORAGE_KEY
//  3. account name + AZURE_STORAGE_SAS_TOKEN
//  4. account name + Azure AD (DefaultAzureCredential, e.g. `az login`)
package azblobclient

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/jingu/ladle/internal/storage"
)

// Options holds configuration for creating an Azure Blob Storage client.
type Options struct {
	// Account is the storage account name. Falls back to AZURE_STORAGE_ACCOUNT.
	// Ignored when AZURE_STORAGE_CONNECTION_STRING is set.
	Account string
	// EndpointURL overrides the blob service URL (e.g. for the Azurite emulator).
	EndpointURL string
}

// AzblobClient implements storage.Client for Azure Blob Storage.
type AzblobClient struct {
	client *azblob.Client
}

var _ storage.Client = (*AzblobClient)(nil)

// New creates a new AzblobClient, resolving the account and credentials from
// Options and the AZURE_STORAGE_* environment variables.
func New(_ context.Context, opts Options) (*AzblobClient, error) {
	if cs := os.Getenv("AZURE_STORAGE_CONNECTION_STRING"); cs != "" {
		client, err := azblob.NewClientFromConnectionString(cs, nil)
		if err != nil {
			return nil, fmt.Errorf("azure: connection string: %w", err)
		}
		return &AzblobClient{client: client}, nil
	}

	account := opts.Account
	if account == "" {
		account = os.Getenv("AZURE_STORAGE_ACCOUNT")
	}
	if account == "" {
		return nil, fmt.Errorf("azure storage account not specified: set --account or AZURE_STORAGE_ACCOUNT (or AZURE_STORAGE_CONNECTION_STRING)")
	}

	serviceURL := opts.EndpointURL
	if serviceURL == "" {
		serviceURL = fmt.Sprintf("https://%s.blob.core.windows.net/", account)
	}

	if key := os.Getenv("AZURE_STORAGE_KEY"); key != "" {
		cred, err := azblob.NewSharedKeyCredential(account, key)
		if err != nil {
			return nil, fmt.Errorf("azure: shared key: %w", err)
		}
		client, err := azblob.NewClientWithSharedKeyCredential(serviceURL, cred, nil)
		if err != nil {
			return nil, fmt.Errorf("azure: %w", err)
		}
		return &AzblobClient{client: client}, nil
	}

	if sas := os.Getenv("AZURE_STORAGE_SAS_TOKEN"); sas != "" {
		u := serviceURL
		if !strings.HasSuffix(u, "/") {
			u += "/"
		}
		u += "?" + strings.TrimPrefix(sas, "?")
		client, err := azblob.NewClientWithNoCredential(u, nil)
		if err != nil {
			return nil, fmt.Errorf("azure: SAS token: %w", err)
		}
		return &AzblobClient{client: client}, nil
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure: default credential: %w", err)
	}
	client, err := azblob.NewClient(serviceURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("azure: %w", err)
	}
	return &AzblobClient{client: client}, nil
}

func (c *AzblobClient) containerClient(bucket string) *container.Client {
	return c.client.ServiceClient().NewContainerClient(bucket)
}

func (c *AzblobClient) blobClient(bucket, key string) *blob.Client {
	return c.containerClient(bucket).NewBlobClient(key)
}

func (c *AzblobClient) Download(ctx context.Context, bucket, key string, w io.Writer) error {
	resp, err := c.client.DownloadStream(ctx, bucket, key, nil)
	if err != nil {
		return fmt.Errorf("downloading az://%s/%s: %w", bucket, key, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("reading az://%s/%s: %w", bucket, key, err)
	}
	return nil
}

func (c *AzblobClient) Upload(ctx context.Context, bucket, key string, r io.Reader, meta *storage.ObjectMetadata) error {
	if _, err := c.client.UploadStream(ctx, bucket, key, r, uploadOptions(meta)); err != nil {
		return fmt.Errorf("uploading to az://%s/%s: %w", bucket, key, err)
	}
	return nil
}

func (c *AzblobClient) HeadObject(ctx context.Context, bucket, key string) (*storage.ObjectMetadata, error) {
	resp, err := c.blobClient(bucket, key).GetProperties(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("head az://%s/%s: %w", bucket, key, err)
	}

	meta := &storage.ObjectMetadata{Metadata: fromAzureMeta(resp.Metadata)}
	if resp.ContentType != nil {
		meta.ContentType = *resp.ContentType
	}
	if resp.CacheControl != nil {
		meta.CacheControl = *resp.CacheControl
	}
	if resp.ContentEncoding != nil {
		meta.ContentEncoding = *resp.ContentEncoding
	}
	if resp.ContentDisposition != nil {
		meta.ContentDisposition = *resp.ContentDisposition
	}
	return meta, nil
}

func (c *AzblobClient) UpdateMetadata(ctx context.Context, bucket, key string, meta *storage.ObjectMetadata) error {
	bc := c.blobClient(bucket, key)

	headers := blob.HTTPHeaders{}
	if meta.ContentType != "" {
		v := meta.ContentType
		headers.BlobContentType = &v
	}
	if meta.CacheControl != "" {
		v := meta.CacheControl
		headers.BlobCacheControl = &v
	}
	if meta.ContentEncoding != "" {
		v := meta.ContentEncoding
		headers.BlobContentEncoding = &v
	}
	if meta.ContentDisposition != "" {
		v := meta.ContentDisposition
		headers.BlobContentDisposition = &v
	}
	if _, err := bc.SetHTTPHeaders(ctx, headers, nil); err != nil {
		return fmt.Errorf("updating headers for az://%s/%s: %w", bucket, key, err)
	}

	if _, err := bc.SetMetadata(ctx, toAzureMeta(meta.Metadata), nil); err != nil {
		return fmt.Errorf("updating metadata for az://%s/%s: %w", bucket, key, err)
	}
	return nil
}

func (c *AzblobClient) List(ctx context.Context, bucket, prefix, delimiter string) ([]storage.ListEntry, error) {
	cc := c.containerClient(bucket)
	var entries []storage.ListEntry

	if delimiter == "" {
		opts := &container.ListBlobsFlatOptions{}
		if prefix != "" {
			opts.Prefix = &prefix
		}
		pager := cc.NewListBlobsFlatPager(opts)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("listing az://%s/%s: %w", bucket, prefix, err)
			}
			entries = appendBlobs(entries, page.Segment.BlobItems)
		}
		return entries, nil
	}

	opts := &container.ListBlobsHierarchyOptions{}
	if prefix != "" {
		opts.Prefix = &prefix
	}
	pager := cc.NewListBlobsHierarchyPager(delimiter, opts)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing az://%s/%s: %w", bucket, prefix, err)
		}
		for _, bp := range page.Segment.BlobPrefixes {
			if bp.Name != nil {
				entries = append(entries, storage.ListEntry{Key: *bp.Name, IsDir: true})
			}
		}
		entries = appendBlobs(entries, page.Segment.BlobItems)
	}
	return entries, nil
}

func appendBlobs(entries []storage.ListEntry, items []*container.BlobItem) []storage.ListEntry {
	for _, item := range items {
		if item.Name == nil {
			continue
		}
		e := storage.ListEntry{Key: *item.Name}
		if item.Properties != nil {
			if item.Properties.ContentLength != nil {
				e.Size = *item.Properties.ContentLength
			}
			if item.Properties.LastModified != nil {
				e.LastModified = *item.Properties.LastModified
			}
		}
		entries = append(entries, e)
	}
	return entries
}

func (c *AzblobClient) ListBuckets(ctx context.Context) ([]string, error) {
	var names []string
	pager := c.client.NewListContainersPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing containers: %w", err)
		}
		for _, item := range page.ContainerItems {
			if item.Name != nil {
				names = append(names, *item.Name)
			}
		}
	}
	return names, nil
}

func (c *AzblobClient) Delete(ctx context.Context, bucket, key string) error {
	if _, err := c.client.DeleteBlob(ctx, bucket, key, nil); err != nil {
		return fmt.Errorf("deleting az://%s/%s: %w", bucket, key, err)
	}
	return nil
}

// Copy copies a blob within the same container by streaming download → upload.
// This avoids the source-authentication constraints of the server-side Copy Blob
// operation, keeping it robust across all supported credential types.
func (c *AzblobClient) Copy(ctx context.Context, bucket, srcKey, dstKey string) error {
	srcMeta, err := c.HeadObject(ctx, bucket, srcKey)
	if err != nil {
		// Proceed without metadata preservation if HEAD fails.
		srcMeta = nil
	}

	resp, err := c.client.DownloadStream(ctx, bucket, srcKey, nil)
	if err != nil {
		return fmt.Errorf("copying az://%s/%s to az://%s/%s: %w", bucket, srcKey, bucket, dstKey, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if _, err := c.client.UploadStream(ctx, bucket, dstKey, resp.Body, uploadOptions(srcMeta)); err != nil {
		return fmt.Errorf("copying az://%s/%s to az://%s/%s: %w", bucket, srcKey, bucket, dstKey, err)
	}
	return nil
}

func (c *AzblobClient) ListVersions(ctx context.Context, bucket, key string) ([]storage.ObjectVersion, error) {
	cc := c.containerClient(bucket)
	pager := cc.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{
		Prefix: &key,
		Include: container.ListBlobsInclude{
			Versions: true,
			Deleted:  true,
		},
	})

	var versions []storage.ObjectVersion
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing versions for az://%s/%s: %w", bucket, key, err)
		}
		for _, item := range page.Segment.BlobItems {
			if item.Name == nil || *item.Name != key {
				continue
			}
			ov := storage.ObjectVersion{}
			if item.VersionID != nil {
				ov.VersionID = *item.VersionID
			}
			// Azure marks only the current version with IsCurrentVersion=true and
			// omits the field (nil) on non-current versions. Default to false and
			// derive solely from IsCurrentVersion to avoid marking every version latest.
			if item.IsCurrentVersion != nil {
				ov.IsLatest = *item.IsCurrentVersion
			}
			if item.Deleted != nil && *item.Deleted {
				ov.IsDeleteMarker = true
			}
			if item.Properties != nil {
				if item.Properties.ContentLength != nil {
					ov.Size = *item.Properties.ContentLength
				}
				if item.Properties.LastModified != nil {
					ov.LastModified = *item.Properties.LastModified
				}
			}
			versions = append(versions, ov)
		}
	}

	// Azure lists versions oldest-first; S3 (and the browser UI) expect
	// newest-first with the current version at the top.
	sortVersionsNewestFirst(versions)
	return versions, nil
}

// sortVersionsNewestFirst orders versions with the latest version first,
// then by most-recent modification time, matching the S3 backend's ordering.
func sortVersionsNewestFirst(versions []storage.ObjectVersion) {
	sort.SliceStable(versions, func(i, j int) bool {
		if versions[i].IsLatest != versions[j].IsLatest {
			return versions[i].IsLatest
		}
		return versions[i].LastModified.After(versions[j].LastModified)
	})
}

func (c *AzblobClient) DownloadVersion(ctx context.Context, bucket, key, versionID string, w io.Writer) error {
	bc, err := c.blobClient(bucket, key).WithVersionID(versionID)
	if err != nil {
		return fmt.Errorf("downloading az://%s/%s (version %s): %w", bucket, key, versionID, err)
	}
	resp, err := bc.DownloadStream(ctx, nil)
	if err != nil {
		return fmt.Errorf("downloading az://%s/%s (version %s): %w", bucket, key, versionID, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("reading az://%s/%s (version %s): %w", bucket, key, versionID, err)
	}
	return nil
}

func uploadOptions(meta *storage.ObjectMetadata) *azblob.UploadStreamOptions {
	opts := &azblob.UploadStreamOptions{}
	if meta == nil {
		return opts
	}

	headers := &blob.HTTPHeaders{}
	hasHeader := false
	if meta.ContentType != "" {
		v := meta.ContentType
		headers.BlobContentType = &v
		hasHeader = true
	}
	if meta.CacheControl != "" {
		v := meta.CacheControl
		headers.BlobCacheControl = &v
		hasHeader = true
	}
	if meta.ContentEncoding != "" {
		v := meta.ContentEncoding
		headers.BlobContentEncoding = &v
		hasHeader = true
	}
	if meta.ContentDisposition != "" {
		v := meta.ContentDisposition
		headers.BlobContentDisposition = &v
		hasHeader = true
	}
	if hasHeader {
		opts.HTTPHeaders = headers
	}
	opts.Metadata = toAzureMeta(meta.Metadata)
	return opts
}

func toAzureMeta(m map[string]string) map[string]*string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]*string, len(m))
	for k, v := range m {
		val := v
		out[k] = &val
	}
	return out
}

func fromAzureMeta(m map[string]*string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}
