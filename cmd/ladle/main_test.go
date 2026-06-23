package main

import (
	"strings"
	"testing"
)

func TestSanitizeCacheKey(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "s3", "s3"},
		{"with profile", "s3_production", "s3_production"},
		{"with account", "az_myaccount", "az_myaccount"},
		{"slash", "az_my/account", "az_my_account"},
		{"parent traversal", "../../etc/passwd", "______etc_passwd"},
		{"backslash", "az_my\\account", "az_my_account"},
		{"dots", "..", "__"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeCacheKey(tt.in)
			if got != tt.want {
				t.Errorf("sanitizeCacheKey(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if strings.ContainsAny(got, `/\`) {
				t.Errorf("sanitized key still contains a path separator: %q", got)
			}
		})
	}
}
