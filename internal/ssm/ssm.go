// Package ssm implements a thin client for AWS SSM Parameter Store.
//
// Parameter Store has no bucket concept; a parameter name is a slash-separated
// path (e.g. "/myapp/prod/db-url"). This client exposes only the operations
// ladle needs: reading, writing, listing by path, and version history.
//
// SecureString handling is deliberately explicit: Get/GetVersion take a
// `decrypt` flag and the caller decides whether to request the plaintext.
// This client never masks values itself — it faithfully returns what AWS
// returns so that the CLI layer owns the redaction policy.
package ssm

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// Parameter is a parameter's value together with its metadata.
type Parameter struct {
	Name         string
	Value        string // plaintext when decrypt was requested; KMS ciphertext otherwise
	Type         string // String | StringList | SecureString
	Version      int64
	LastModified time.Time
	Metadata     Metadata
}

// IsSecure reports whether the parameter is a SecureString.
func (p *Parameter) IsSecure() bool {
	return p.Type == string(types.ParameterTypeSecureString)
}

// Metadata holds the editable attributes of a parameter (used by --meta).
type Metadata struct {
	Type        string // String | StringList | SecureString
	Tier        string // Standard | Advanced | Intelligent-Tiering
	KeyID       string // KMS key id/alias for SecureString
	Description string
	DataType    string // text | aws:ec2:image | aws:ssm:integration
}

// HistoryEntry is one version in a parameter's history.
type HistoryEntry struct {
	Version      int64
	LastModified time.Time
	ModifiedUser string // ARN of the principal that last modified this version
	Type         string
}

// ListEntry is one item under a path listing.
type ListEntry struct {
	Name  string // full parameter name, or the path prefix for a directory
	IsDir bool
	Type  string // parameter type ("" for directories)
}

// PutInput describes a parameter write.
type PutInput struct {
	Name  string
	Value string
	Meta  Metadata // Type is required; KeyID/Tier/Description/DataType optional
}

// Client is the set of Parameter Store operations ladle uses.
type Client interface {
	// Get retrieves a parameter. When decrypt is true and the parameter is a
	// SecureString, the plaintext value is returned (requires kms:Decrypt).
	Get(ctx context.Context, name string, decrypt bool) (*Parameter, error)
	// GetVersion retrieves a specific version of a parameter.
	GetVersion(ctx context.Context, name string, version int64, decrypt bool) (*Parameter, error)
	// Describe returns a parameter's metadata without its value.
	Describe(ctx context.Context, name string) (*Metadata, error)
	// Put creates or overwrites a parameter.
	Put(ctx context.Context, in PutInput) error
	// List lists parameters under a path. With recursive=false only the
	// immediate children are returned and deeper paths appear as directories.
	List(ctx context.Context, path string, recursive bool) ([]ListEntry, error)
	// History returns the version history of a parameter, newest first.
	History(ctx context.Context, name string) ([]HistoryEntry, error)
}

// Options configures the AWS-backed client.
type Options struct {
	Profile string
	Region  string
}

type awsClient struct {
	client *ssm.Client
}

// New creates an AWS-backed Parameter Store client.
func New(ctx context.Context, opts Options) (Client, error) {
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
	return &awsClient{client: ssm.NewFromConfig(cfg)}, nil
}

func (c *awsClient) Get(ctx context.Context, name string, decrypt bool) (*Parameter, error) {
	out, err := c.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(decrypt),
	})
	if err != nil {
		return nil, fmt.Errorf("getting parameter %s: %w", name, err)
	}
	return paramFromSDK(out.Parameter), nil
}

func (c *awsClient) GetVersion(ctx context.Context, name string, version int64, decrypt bool) (*Parameter, error) {
	// SSM addresses a specific version via the "name:version" selector.
	selector := name + ":" + strconv.FormatInt(version, 10)
	out, err := c.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(selector),
		WithDecryption: aws.Bool(decrypt),
	})
	if err != nil {
		return nil, fmt.Errorf("getting parameter %s version %d: %w", name, version, err)
	}
	p := paramFromSDK(out.Parameter)
	p.Name = name // strip the ":version" selector from the reported name
	return p, nil
}

func (c *awsClient) Describe(ctx context.Context, name string) (*Metadata, error) {
	out, err := c.client.DescribeParameters(ctx, &ssm.DescribeParametersInput{
		ParameterFilters: []types.ParameterStringFilter{{
			Key:    aws.String("Name"),
			Option: aws.String("Equals"),
			Values: []string{name},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("describing parameter %s: %w", name, err)
	}
	if len(out.Parameters) == 0 {
		return nil, &NotFoundError{Name: name}
	}
	m := out.Parameters[0]
	return &Metadata{
		Type:        string(m.Type),
		Tier:        string(m.Tier),
		KeyID:       aws.ToString(m.KeyId),
		Description: aws.ToString(m.Description),
		DataType:    aws.ToString(m.DataType),
	}, nil
}

func (c *awsClient) Put(ctx context.Context, in PutInput) error {
	input := &ssm.PutParameterInput{
		Name:      aws.String(in.Name),
		Value:     aws.String(in.Value),
		Type:      types.ParameterType(in.Meta.Type),
		Overwrite: aws.Bool(true),
	}
	if in.Meta.KeyID != "" {
		input.KeyId = aws.String(in.Meta.KeyID)
	}
	if in.Meta.Tier != "" {
		input.Tier = types.ParameterTier(in.Meta.Tier)
	}
	if in.Meta.Description != "" {
		input.Description = aws.String(in.Meta.Description)
	}
	if in.Meta.DataType != "" {
		input.DataType = aws.String(in.Meta.DataType)
	}
	if _, err := c.client.PutParameter(ctx, input); err != nil {
		return fmt.Errorf("putting parameter %s: %w", in.Name, err)
	}
	return nil
}

func (c *awsClient) List(ctx context.Context, path string, recursive bool) ([]ListEntry, error) {
	// GetParametersByPath requires a leading slash and no trailing slash
	// (except the root "/").
	queryPath := path
	if queryPath != "/" {
		queryPath = strings.TrimRight(queryPath, "/")
	}

	// Always fetch recursively: SSM's non-recursive listing does not surface
	// deeper namespaces as directories, so we derive the requested level
	// ourselves (mirroring S3's delimiter-based CommonPrefixes behavior).
	var leaves []ListEntry
	paginator := ssm.NewGetParametersByPathPaginator(c.client, &ssm.GetParametersByPathInput{
		Path:      aws.String(queryPath),
		Recursive: aws.Bool(true),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing parameters under %s: %w", path, err)
		}
		for _, p := range page.Parameters {
			leaves = append(leaves, ListEntry{Name: aws.ToString(p.Name), Type: string(p.Type)})
		}
	}

	return collapseListing(queryPath, leaves, recursive), nil
}

// collapseListing reduces a recursive set of leaf parameters to the level
// requested. When recursive is false, names that live deeper than the
// immediate level under queryPath are folded into a single directory entry
// (name ending in "/") mirroring S3's CommonPrefixes.
func collapseListing(queryPath string, leaves []ListEntry, recursive bool) []ListEntry {
	if recursive {
		return leaves
	}
	prefix := strings.TrimRight(queryPath, "/") + "/"
	var entries []ListEntry
	seenDir := map[string]bool{}
	for _, l := range leaves {
		rest := strings.TrimPrefix(l.Name, prefix)
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			dir := prefix + rest[:i] + "/"
			if !seenDir[dir] {
				seenDir[dir] = true
				entries = append(entries, ListEntry{Name: dir, IsDir: true})
			}
			continue
		}
		entries = append(entries, ListEntry{Name: l.Name, Type: l.Type})
	}
	return entries
}

func (c *awsClient) History(ctx context.Context, name string) ([]HistoryEntry, error) {
	var entries []HistoryEntry
	paginator := ssm.NewGetParameterHistoryPaginator(c.client, &ssm.GetParameterHistoryInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(false),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("getting history for %s: %w", name, err)
		}
		for _, h := range page.Parameters {
			entries = append(entries, HistoryEntry{
				Version:      h.Version,
				LastModified: aws.ToTime(h.LastModifiedDate),
				ModifiedUser: aws.ToString(h.LastModifiedUser),
				Type:         string(h.Type),
			})
		}
	}
	// AWS returns oldest-first; reverse to newest-first.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries, nil
}

func paramFromSDK(p *types.Parameter) *Parameter {
	return &Parameter{
		Name:         aws.ToString(p.Name),
		Value:        aws.ToString(p.Value),
		Type:         string(p.Type),
		Version:      p.Version,
		LastModified: aws.ToTime(p.LastModifiedDate),
		Metadata: Metadata{
			Type:     string(p.Type),
			DataType: aws.ToString(p.DataType),
		},
	}
}
