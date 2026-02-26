package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

// MockClient is an in-memory implementation of Client for testing.
type MockClient struct {
	mu       sync.Mutex
	objects  map[string]mockObject
	buckets  []string
}

type mockObject struct {
	data []byte
	meta ObjectMetadata
}

// NewMockClient creates a new MockClient.
func NewMockClient() *MockClient {
	return &MockClient{
		objects: make(map[string]mockObject),
	}
}

func (m *MockClient) key(bucket, key string) string {
	return bucket + "/" + key
}

// PutObject adds an object to the mock store.
func (m *MockClient) PutObject(bucket, key string, data []byte, meta *ObjectMetadata) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj := mockObject{data: data}
	if meta != nil {
		obj.meta = *meta
	}
	m.objects[m.key(bucket, key)] = obj
}

// SetBuckets sets the list of buckets.
func (m *MockClient) SetBuckets(names []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.buckets = names
}

func (m *MockClient) Download(_ context.Context, bucket, key string, w io.Writer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[m.key(bucket, key)]
	if !ok {
		return fmt.Errorf("object not found: %s/%s", bucket, key)
	}
	_, err := io.Copy(w, bytes.NewReader(obj.data))
	return err
}

func (m *MockClient) Upload(_ context.Context, bucket, key string, r io.Reader, meta *ObjectMetadata) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	obj := mockObject{data: data}
	if meta != nil {
		obj.meta = *meta
	}
	m.objects[m.key(bucket, key)] = obj
	return nil
}

func (m *MockClient) HeadObject(_ context.Context, bucket, key string) (*ObjectMetadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[m.key(bucket, key)]
	if !ok {
		return nil, fmt.Errorf("object not found: %s/%s", bucket, key)
	}
	meta := obj.meta
	if meta.Metadata == nil {
		meta.Metadata = make(map[string]string)
	}
	return &meta, nil
}

func (m *MockClient) UpdateMetadata(_ context.Context, bucket, key string, meta *ObjectMetadata) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(bucket, key)
	obj, ok := m.objects[k]
	if !ok {
		return fmt.Errorf("object not found: %s/%s", bucket, key)
	}
	if meta != nil {
		obj.meta = *meta
	}
	m.objects[k] = obj
	return nil
}

func (m *MockClient) List(_ context.Context, bucket, prefix, delimiter string) ([]ListEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var entries []ListEntry
	seen := make(map[string]bool)

	for fullKey, obj := range m.objects {
		parts := strings.SplitN(fullKey, "/", 2)
		if parts[0] != bucket || len(parts) < 2 {
			continue
		}
		objKey := parts[1]
		if !strings.HasPrefix(objKey, prefix) {
			continue
		}

		rest := objKey[len(prefix):]
		if delimiter != "" {
			if idx := strings.Index(rest, delimiter); idx >= 0 {
				dirKey := prefix + rest[:idx+1]
				if !seen[dirKey] {
					seen[dirKey] = true
					entries = append(entries, ListEntry{Key: dirKey, IsDir: true})
				}
				continue
			}
		}

		entries = append(entries, ListEntry{
			Key:  objKey,
			Size: int64(len(obj.data)),
		})
	}
	return entries, nil
}

func (m *MockClient) ListBuckets(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buckets, nil
}
