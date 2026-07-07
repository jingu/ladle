package ssm

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// metaDoc is the YAML representation of a parameter's editable attributes.
type metaDoc struct {
	Type        string `yaml:"type"`
	Tier        string `yaml:"tier,omitempty"`
	KeyID       string `yaml:"keyId,omitempty"`
	Description string `yaml:"description,omitempty"`
	DataType    string `yaml:"dataType,omitempty"`
}

// MarshalMeta renders a parameter's metadata as YAML, prefixed with a comment
// naming the parameter URI.
func MarshalMeta(uri string, m *Metadata) ([]byte, error) {
	body, err := yaml.Marshal(metaDoc{
		Type:        m.Type,
		Tier:        m.Tier,
		KeyID:       m.KeyID,
		Description: m.Description,
		DataType:    m.DataType,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling metadata: %w", err)
	}
	return append([]byte("# "+uri+"\n"), body...), nil
}

// UnmarshalMeta parses YAML metadata. Type is required.
func UnmarshalMeta(data []byte) (*Metadata, error) {
	var doc metaDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing metadata YAML: %w", err)
	}
	if doc.Type == "" {
		return nil, fmt.Errorf("metadata: 'type' is required (String, StringList, or SecureString)")
	}
	return &Metadata{
		Type:        doc.Type,
		Tier:        doc.Tier,
		KeyID:       doc.KeyID,
		Description: doc.Description,
		DataType:    doc.DataType,
	}, nil
}
