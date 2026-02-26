// Package s3client implements the storage.Client interface for AWS S3.
package s3client

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/jingu/ladle/internal/storage"
)

// Options holds configuration for creating an S3 client.
type Options struct {
	Profile       string
	Region        string
	EndpointURL   string
	NoSignRequest bool
}

// S3Client implements storage.Client for AWS S3.
type S3Client struct {
	client *s3.Client
}

// New creates a new S3Client with the given options.
func New(ctx context.Context, opts Options) (*S3Client, error) {
	var cfgOpts []func(*config.LoadOptions) error

	if opts.Profile != "" {
		cfgOpts = append(cfgOpts, config.WithSharedConfigProfile(opts.Profile))
	}
	if opts.Region != "" {
		cfgOpts = append(cfgOpts, config.WithRegion(opts.Region))
	}

	cfg, err := config.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	var s3Opts []func(*s3.Options)
	if opts.EndpointURL != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(opts.EndpointURL)
			o.UsePathStyle = true
		})
	}
	if opts.NoSignRequest {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.Credentials = aws.AnonymousCredentials{}
		})
	}

	client := s3.NewFromConfig(cfg, s3Opts...)
	return &S3Client{client: client}, nil
}

func (c *S3Client) Download(ctx context.Context, bucket, key string, w io.Writer) error {
	out, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("downloading s3://%s/%s: %w", bucket, key, err)
	}
	defer out.Body.Close()

	if _, err := io.Copy(w, out.Body); err != nil {
		return fmt.Errorf("reading s3://%s/%s: %w", bucket, key, err)
	}
	return nil
}

func (c *S3Client) Upload(ctx context.Context, bucket, key string, r io.Reader, meta *storage.ObjectMetadata) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   r,
	}

	if meta != nil {
		if meta.ContentType != "" {
			input.ContentType = aws.String(meta.ContentType)
		}
		if meta.CacheControl != "" {
			input.CacheControl = aws.String(meta.CacheControl)
		}
		if meta.ContentEncoding != "" {
			input.ContentEncoding = aws.String(meta.ContentEncoding)
		}
		if meta.ContentDisposition != "" {
			input.ContentDisposition = aws.String(meta.ContentDisposition)
		}
		if len(meta.Metadata) > 0 {
			input.Metadata = meta.Metadata
		}
	}

	if _, err := c.client.PutObject(ctx, input); err != nil {
		return fmt.Errorf("uploading to s3://%s/%s: %w", bucket, key, err)
	}
	return nil
}

func (c *S3Client) HeadObject(ctx context.Context, bucket, key string) (*storage.ObjectMetadata, error) {
	out, err := c.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("head s3://%s/%s: %w", bucket, key, err)
	}

	meta := &storage.ObjectMetadata{
		Metadata: make(map[string]string),
	}
	if out.ContentType != nil {
		meta.ContentType = *out.ContentType
	}
	if out.CacheControl != nil {
		meta.CacheControl = *out.CacheControl
	}
	if out.ContentEncoding != nil {
		meta.ContentEncoding = *out.ContentEncoding
	}
	if out.ContentDisposition != nil {
		meta.ContentDisposition = *out.ContentDisposition
	}
	for k, v := range out.Metadata {
		meta.Metadata[k] = v
	}
	return meta, nil
}

func (c *S3Client) UpdateMetadata(ctx context.Context, bucket, key string, meta *storage.ObjectMetadata) error {
	encodedKey := encodeKeySegments(key)
	source := fmt.Sprintf("%s/%s", bucket, encodedKey)
	input := &s3.CopyObjectInput{
		Bucket:            aws.String(bucket),
		Key:               aws.String(key),
		CopySource:        aws.String(source),
		MetadataDirective: types.MetadataDirectiveReplace,
	}

	if meta.ContentType != "" {
		input.ContentType = aws.String(meta.ContentType)
	}
	if meta.CacheControl != "" {
		input.CacheControl = aws.String(meta.CacheControl)
	}
	if meta.ContentEncoding != "" {
		input.ContentEncoding = aws.String(meta.ContentEncoding)
	}
	if meta.ContentDisposition != "" {
		input.ContentDisposition = aws.String(meta.ContentDisposition)
	}
	if len(meta.Metadata) > 0 {
		input.Metadata = meta.Metadata
	}

	if _, err := c.client.CopyObject(ctx, input); err != nil {
		return fmt.Errorf("updating metadata for s3://%s/%s: %w", bucket, key, err)
	}
	return nil
}

func (c *S3Client) List(ctx context.Context, bucket, prefix, delimiter string) ([]storage.ListEntry, error) {
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	}
	if delimiter != "" {
		input.Delimiter = aws.String(delimiter)
	}

	var entries []storage.ListEntry
	paginator := s3.NewListObjectsV2Paginator(c.client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing s3://%s/%s: %w", bucket, prefix, err)
		}
		for _, cp := range page.CommonPrefixes {
			if cp.Prefix != nil {
				entries = append(entries, storage.ListEntry{
					Key:   *cp.Prefix,
					IsDir: true,
				})
			}
		}
		for _, obj := range page.Contents {
			if obj.Key != nil {
				var size int64
				if obj.Size != nil {
					size = *obj.Size
				}
				entries = append(entries, storage.ListEntry{
					Key:  *obj.Key,
					Size: size,
				})
			}
		}
	}
	return entries, nil
}

func (c *S3Client) ListBuckets(ctx context.Context) ([]string, error) {
	out, err := c.client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("listing buckets: %w", err)
	}
	var names []string
	for _, b := range out.Buckets {
		if b.Name != nil {
			names = append(names, *b.Name)
		}
	}
	return names, nil
}

// encodeKeySegments URL-encodes each path segment of an S3 key per RFC 3986.
func encodeKeySegments(key string) string {
	segments := strings.Split(key, "/")
	for i, seg := range segments {
		segments[i] = strings.ReplaceAll(url.QueryEscape(seg), "+", "%20")
	}
	return strings.Join(segments, "/")
}
