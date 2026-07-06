package ssm

import "testing"

func TestMarshalUnmarshalMetaRoundTrip(t *testing.T) {
	in := &Metadata{
		Type:        "SecureString",
		Tier:        "Standard",
		KeyID:       "alias/my-key",
		Description: "prod db password",
		DataType:    "text",
	}
	data, err := MarshalMeta("ssm:///myapp/db-password", in)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data[:2]); got != "# " {
		t.Errorf("expected leading URI comment, got %q", got)
	}
	out, err := UnmarshalMeta(data)
	if err != nil {
		t.Fatal(err)
	}
	if *out != *in {
		t.Errorf("round trip mismatch:\n got %#v\nwant %#v", *out, *in)
	}
}

func TestUnmarshalMetaRequiresType(t *testing.T) {
	_, err := UnmarshalMeta([]byte("tier: Standard\n"))
	if err == nil {
		t.Fatal("expected error when type is missing")
	}
}
