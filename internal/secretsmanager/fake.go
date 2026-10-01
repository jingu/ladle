package secretsmanager

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// FakeClient is an in-memory Client for tests. Each secret keeps its full
// version list so history and version reads can be exercised.
type FakeClient struct {
	// Secrets maps a secret name to its versions, oldest first. The last one is
	// the current version.
	Secrets map[string][]*Secret
	// Meta maps a secret name to its metadata.
	Meta map[string]*Metadata
	// Puts records every value written (Create and Put) in order.
	Puts []string
}

// NewFake returns an empty FakeClient.
func NewFake() *FakeClient {
	return &FakeClient{Secrets: map[string][]*Secret{}, Meta: map[string]*Metadata{}}
}

// Set stores value as a new current version of name, creating it if needed.
func (f *FakeClient) Set(name, value string) {
	versions := f.Secrets[name]
	f.Secrets[name] = append(versions, &Secret{
		Name:      name,
		Value:     value,
		VersionID: fmt.Sprintf("v%d", len(versions)+1),
	})
	if f.Meta[name] == nil {
		f.Meta[name] = &Metadata{}
	}
}

// Current returns the current value of name ("" when absent).
func (f *FakeClient) Current(name string) string {
	versions := f.Secrets[name]
	if len(versions) == 0 {
		return ""
	}
	return versions[len(versions)-1].Value
}

func (f *FakeClient) Get(_ context.Context, name string) (*Secret, error) {
	versions := f.Secrets[name]
	if len(versions) == 0 {
		return nil, &NotFoundError{Name: name}
	}
	s := *versions[len(versions)-1]
	return &s, nil
}

func (f *FakeClient) GetVersion(_ context.Context, name, versionID string) (*Secret, error) {
	for _, v := range f.Secrets[name] {
		if v.VersionID == versionID {
			s := *v
			return &s, nil
		}
	}
	return nil, &NotFoundError{Name: name}
}

func (f *FakeClient) Describe(_ context.Context, name string) (*Metadata, error) {
	m, ok := f.Meta[name]
	if !ok {
		return nil, &NotFoundError{Name: name}
	}
	cp := *m
	if versions := f.Secrets[name]; len(versions) > 0 {
		cp.CurrentVersionID = versions[len(versions)-1].VersionID
	}
	return &cp, nil
}

func (f *FakeClient) Create(_ context.Context, name, value, description string) error {
	if _, ok := f.Secrets[name]; ok {
		return fmt.Errorf("secret %s already exists", name)
	}
	f.Puts = append(f.Puts, value)
	f.Set(name, value)
	f.Meta[name].Description = description
	return nil
}

func (f *FakeClient) Put(_ context.Context, name, value string) error {
	if _, ok := f.Secrets[name]; !ok {
		return &NotFoundError{Name: name}
	}
	f.Puts = append(f.Puts, value)
	f.Set(name, value)
	return nil
}

func (f *FakeClient) List(_ context.Context, prefix string) ([]string, error) {
	var names []string
	for name := range f.Secrets {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func (f *FakeClient) History(_ context.Context, name string) ([]Version, error) {
	versions := f.Secrets[name]
	if len(versions) == 0 {
		return nil, &NotFoundError{Name: name}
	}
	out := make([]Version, 0, len(versions))
	for i := len(versions) - 1; i >= 0; i-- {
		v := Version{VersionID: versions[i].VersionID}
		switch i {
		case len(versions) - 1:
			v.Stages = []string{"AWSCURRENT"}
		case len(versions) - 2:
			v.Stages = []string{"AWSPREVIOUS"}
		}
		out = append(out, v)
	}
	return out, nil
}
