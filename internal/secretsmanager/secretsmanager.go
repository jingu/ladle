// Package secretsmanager implements a thin client for AWS Secrets Manager.
//
// Secrets Manager has no hierarchy: a secret name is an opaque string that
// merely conventionally uses "/" (e.g. "prod/myapp/db"). This client exposes
// only the operations ladle needs: reading the current or a past version,
// writing a new version, creating, listing by name prefix, and version history.
//
// Every value is a secret, so this client never masks anything itself — it
// returns what AWS returns and the CLI layer owns the redaction policy.
package secretsmanager

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	sm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// Secret is one version of a secret's value.
type Secret struct {
	Name      string
	Value     string // SecretString
	IsBinary  bool   // the version holds SecretBinary, which ladle does not edit
	VersionID string
}

// Metadata holds the attributes of a secret that ladle reports on.
type Metadata struct {
	Description      string
	RotationEnabled  bool
	CurrentVersionID string // the version carrying the AWSCURRENT stage
}

// Version is one entry in a secret's version history.
type Version struct {
	VersionID   string
	Stages      []string // e.g. AWSCURRENT, AWSPREVIOUS
	CreatedDate time.Time
}

// IsCurrent reports whether the version carries the AWSCURRENT stage.
func (v Version) IsCurrent() bool {
	for _, s := range v.Stages {
		if s == "AWSCURRENT" {
			return true
		}
	}
	return false
}

// Client is the set of Secrets Manager operations ladle uses.
type Client interface {
	// Get retrieves the current (AWSCURRENT) value of a secret.
	Get(ctx context.Context, name string) (*Secret, error)
	// GetVersion retrieves a specific version of a secret.
	GetVersion(ctx context.Context, name, versionID string) (*Secret, error)
	// Describe returns a secret's metadata without its value, including which
	// version is current.
	Describe(ctx context.Context, name string) (*Metadata, error)
	// Create creates a new secret with a string value.
	Create(ctx context.Context, name, value, description string) error
	// Put stores a new version of an existing secret and makes it current.
	Put(ctx context.Context, name, value string) error
	// List returns the names of all secrets starting with prefix, sorted.
	List(ctx context.Context, prefix string) ([]string, error)
	// History returns the secret's versions, newest first.
	History(ctx context.Context, name string) ([]Version, error)
}

// Options configures the AWS-backed client.
type Options struct {
	Profile string
	Region  string
}

type awsClient struct {
	client *sm.Client
}

// New creates an AWS-backed Secrets Manager client.
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
	return &awsClient{client: sm.NewFromConfig(cfg)}, nil
}

func (c *awsClient) Get(ctx context.Context, name string) (*Secret, error) {
	return c.get(ctx, name, "")
}

func (c *awsClient) GetVersion(ctx context.Context, name, versionID string) (*Secret, error) {
	return c.get(ctx, name, versionID)
}

func (c *awsClient) get(ctx context.Context, name, versionID string) (*Secret, error) {
	in := &sm.GetSecretValueInput{SecretId: aws.String(name)}
	if versionID != "" {
		in.VersionId = aws.String(versionID)
	}
	out, err := c.client.GetSecretValue(ctx, in)
	if err != nil {
		if IsNotFound(err) {
			return nil, &NotFoundError{Name: name}
		}
		return nil, fmt.Errorf("getting secret %s: %w", name, err)
	}
	return &Secret{
		Name:      name,
		Value:     aws.ToString(out.SecretString),
		IsBinary:  out.SecretString == nil && out.SecretBinary != nil,
		VersionID: aws.ToString(out.VersionId),
	}, nil
}

func (c *awsClient) Describe(ctx context.Context, name string) (*Metadata, error) {
	out, err := c.client.DescribeSecret(ctx, &sm.DescribeSecretInput{SecretId: aws.String(name)})
	if err != nil {
		if IsNotFound(err) {
			return nil, &NotFoundError{Name: name}
		}
		return nil, fmt.Errorf("describing secret %s: %w", name, err)
	}
	md := &Metadata{
		Description:     aws.ToString(out.Description),
		RotationEnabled: aws.ToBool(out.RotationEnabled),
	}
	for id, stages := range out.VersionIdsToStages {
		if (Version{Stages: stages}).IsCurrent() {
			md.CurrentVersionID = id
		}
	}
	return md, nil
}

func (c *awsClient) Create(ctx context.Context, name, value, description string) error {
	in := &sm.CreateSecretInput{
		Name:         aws.String(name),
		SecretString: aws.String(value),
	}
	if description != "" {
		in.Description = aws.String(description)
	}
	if _, err := c.client.CreateSecret(ctx, in); err != nil {
		return fmt.Errorf("creating secret %s: %w", name, err)
	}
	return nil
}

func (c *awsClient) Put(ctx context.Context, name, value string) error {
	if _, err := c.client.PutSecretValue(ctx, &sm.PutSecretValueInput{
		SecretId:     aws.String(name),
		SecretString: aws.String(value),
	}); err != nil {
		return fmt.Errorf("putting secret %s: %w", name, err)
	}
	return nil
}

func (c *awsClient) List(ctx context.Context, prefix string) ([]string, error) {
	in := &sm.ListSecretsInput{}
	if prefix != "" {
		in.Filters = []types.Filter{{Key: types.FilterNameStringTypeName, Values: []string{prefix}}}
	}
	var names []string
	paginator := sm.NewListSecretsPaginator(c.client, in)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing secrets: %w", err)
		}
		for _, s := range page.SecretList {
			// The name filter matches case-insensitively; keep only true prefixes.
			if n := aws.ToString(s.Name); strings.HasPrefix(n, prefix) {
				names = append(names, n)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

func (c *awsClient) History(ctx context.Context, name string) ([]Version, error) {
	var versions []Version
	paginator := sm.NewListSecretVersionIdsPaginator(c.client, &sm.ListSecretVersionIdsInput{
		SecretId:          aws.String(name),
		IncludeDeprecated: aws.Bool(true),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			if IsNotFound(err) {
				return nil, &NotFoundError{Name: name}
			}
			return nil, fmt.Errorf("getting history for %s: %w", name, err)
		}
		for _, v := range page.Versions {
			versions = append(versions, Version{
				VersionID:   aws.ToString(v.VersionId),
				Stages:      v.VersionStages,
				CreatedDate: aws.ToTime(v.CreatedDate),
			})
		}
	}
	sort.SliceStable(versions, func(i, j int) bool {
		return versions[i].CreatedDate.After(versions[j].CreatedDate)
	})
	return versions, nil
}

// NotFoundError indicates a secret does not exist.
type NotFoundError struct {
	Name string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("secret %s not found", e.Name)
}

// IsNotFound reports whether err represents a missing secret, whether it is a
// *NotFoundError from this package or the SDK's ResourceNotFoundException.
func IsNotFound(err error) bool {
	var nf *NotFoundError
	if errors.As(err, &nf) {
		return true
	}
	var rnf *types.ResourceNotFoundException
	return errors.As(err, &rnf)
}
