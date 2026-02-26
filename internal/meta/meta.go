// Package meta handles metadata serialization to/from YAML.
package meta

import (
	"fmt"

	"github.com/jingu/ladle/internal/storage"
	"gopkg.in/yaml.v3"
)

// MetadataYAML is the YAML representation of object metadata.
type MetadataYAML struct {
	ContentType        string            `yaml:"ContentType"`
	CacheControl       string            `yaml:"CacheControl"`
	ContentEncoding    string            `yaml:"ContentEncoding"`
	ContentDisposition string            `yaml:"ContentDisposition"`
	Metadata           map[string]string `yaml:"Metadata,omitempty"`
}

// Marshal converts storage metadata to YAML bytes with a comment header.
func Marshal(uri string, meta *storage.ObjectMetadata) ([]byte, error) {
	y := MetadataYAML{
		ContentType:        meta.ContentType,
		CacheControl:       meta.CacheControl,
		ContentEncoding:    meta.ContentEncoding,
		ContentDisposition: meta.ContentDisposition,
		Metadata:           meta.Metadata,
	}
	if y.Metadata == nil {
		y.Metadata = make(map[string]string)
	}

	data, err := yaml.Marshal(&y)
	if err != nil {
		return nil, fmt.Errorf("marshaling metadata: %w", err)
	}

	header := fmt.Sprintf("# %s\n", uri)
	return append([]byte(header), data...), nil
}

// Unmarshal parses YAML bytes into storage metadata.
func Unmarshal(data []byte) (*storage.ObjectMetadata, error) {
	var y MetadataYAML
	if err := yaml.Unmarshal(data, &y); err != nil {
		return nil, fmt.Errorf("parsing metadata YAML: %w", err)
	}

	meta := &storage.ObjectMetadata{
		ContentType:        y.ContentType,
		CacheControl:       y.CacheControl,
		ContentEncoding:    y.ContentEncoding,
		ContentDisposition: y.ContentDisposition,
		Metadata:           y.Metadata,
	}
	if meta.Metadata == nil {
		meta.Metadata = make(map[string]string)
	}
	return meta, nil
}
