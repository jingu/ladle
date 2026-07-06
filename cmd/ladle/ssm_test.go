package main

import (
	"context"
	"strings"
	"testing"

	"github.com/jingu/ladle/internal/ssm"
)

func TestResolveForEditSecureStringGate(t *testing.T) {
	ctx := context.Background()
	c := ssm.NewFake()
	c.Set("/app/db-password", "s3cret", "SecureString", "alias/k")
	c.Set("/app/db-url", "postgres://h/db", "String", "")

	t.Run("SecureString without reveal is refused", func(t *testing.T) {
		_, _, err := resolveForEdit(ctx, c, "/app/db-password", false)
		if err == nil {
			t.Fatal("expected refusal for SecureString without --reveal")
		}
		if !strings.Contains(err.Error(), "--reveal") {
			t.Errorf("error should mention --reveal, got: %v", err)
		}
	})

	t.Run("SecureString with reveal returns plaintext and metadata", func(t *testing.T) {
		md, val, err := resolveForEdit(ctx, c, "/app/db-password", true)
		if err != nil {
			t.Fatal(err)
		}
		if val != "s3cret" {
			t.Errorf("value: got %q, want %q", val, "s3cret")
		}
		if md.KeyID != "alias/k" {
			t.Errorf("KeyID should be preserved for re-put, got %q", md.KeyID)
		}
	})

	t.Run("String does not require reveal", func(t *testing.T) {
		_, val, err := resolveForEdit(ctx, c, "/app/db-url", false)
		if err != nil {
			t.Fatal(err)
		}
		if val != "postgres://h/db" {
			t.Errorf("value: got %q", val)
		}
	})

	t.Run("missing parameter reports not found", func(t *testing.T) {
		_, _, err := resolveForEdit(ctx, c, "/app/missing", true)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected not-found error, got: %v", err)
		}
	})
}

func TestNewParamType(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "String", false},
		{"String", "String", false},
		{"StringList", "StringList", false},
		{"SecureString", "SecureString", false},
		{"securestring", "", true}, // AWS types are case-sensitive
		{"Secret", "", true},
	}
	for _, tt := range tests {
		got, err := newParamType(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("newParamType(%q): expected error", tt.in)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("newParamType(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}
