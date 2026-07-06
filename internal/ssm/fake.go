package ssm

import (
	"context"
	"sort"
)

// FakeClient is an in-memory Client for tests.
type FakeClient struct {
	// Params maps a parameter name to its stored state.
	Params map[string]*Parameter
	// Puts records every Put call in order.
	Puts []PutInput
}

// NewFake returns an empty FakeClient.
func NewFake() *FakeClient {
	return &FakeClient{Params: map[string]*Parameter{}}
}

// Set stores a parameter, deriving metadata from the value's type.
func (f *FakeClient) Set(name, value, ptype, keyID string) {
	f.Params[name] = &Parameter{
		Name:    name,
		Value:   value,
		Type:    ptype,
		Version: 1,
		Metadata: Metadata{
			Type:  ptype,
			KeyID: keyID,
		},
	}
}

func (f *FakeClient) Get(_ context.Context, name string, _ bool) (*Parameter, error) {
	p, ok := f.Params[name]
	if !ok {
		return nil, &NotFoundError{Name: name}
	}
	cp := *p
	return &cp, nil
}

func (f *FakeClient) GetVersion(ctx context.Context, name string, _ int64, decrypt bool) (*Parameter, error) {
	return f.Get(ctx, name, decrypt)
}

func (f *FakeClient) Describe(_ context.Context, name string) (*Metadata, error) {
	p, ok := f.Params[name]
	if !ok {
		return nil, &NotFoundError{Name: name}
	}
	m := p.Metadata
	return &m, nil
}

func (f *FakeClient) Put(_ context.Context, in PutInput) error {
	f.Puts = append(f.Puts, in)
	f.Params[in.Name] = &Parameter{
		Name:     in.Name,
		Value:    in.Value,
		Type:     in.Meta.Type,
		Metadata: in.Meta,
	}
	return nil
}

func (f *FakeClient) List(_ context.Context, path string, recursive bool) ([]ListEntry, error) {
	var leaves []ListEntry
	for name, p := range f.Params {
		leaves = append(leaves, ListEntry{Name: name, Type: p.Type})
	}
	sort.Slice(leaves, func(i, j int) bool { return leaves[i].Name < leaves[j].Name })
	return collapseListing(path, leaves, recursive), nil
}

func (f *FakeClient) History(_ context.Context, name string) ([]HistoryEntry, error) {
	p, ok := f.Params[name]
	if !ok {
		return nil, &NotFoundError{Name: name}
	}
	return []HistoryEntry{{Version: p.Version, Type: p.Type}}, nil
}
