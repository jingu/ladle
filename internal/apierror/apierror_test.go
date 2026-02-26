package apierror

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/smithy-go"
)

// mockAPIError implements smithy.APIError for testing.
type mockAPIError struct {
	code    string
	message string
	fault   smithy.ErrorFault
}

func (e *mockAPIError) Error() string            { return fmt.Sprintf("%s: %s", e.code, e.message) }
func (e *mockAPIError) ErrorCode() string         { return e.code }
func (e *mockAPIError) ErrorMessage() string      { return e.message }
func (e *mockAPIError) ErrorFault() smithy.ErrorFault { return e.fault }

func TestClassify_APIErrors(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		message  string
		wantKind Kind
	}{
		// Auth errors
		{"expired token", "ExpiredToken", "token expired", KindAuth},
		{"signature mismatch", "SignatureDoesNotMatch", "bad sig", KindAuth},
		{"missing auth", "MissingAuthenticationToken", "no token", KindAuth},
		{"expired token exception", "ExpiredTokenException", "expired", KindAuth},

		// Permission errors
		{"access denied", "AccessDenied", "forbidden", KindPermission},
		{"invalid access key", "InvalidAccessKeyId", "bad key", KindPermission},
		{"access denied exception", "AccessDeniedException", "forbidden", KindPermission},

		// Not found errors
		{"no such bucket", "NoSuchBucket", "bucket gone", KindNotFound},
		{"no such key", "NoSuchKey", "key gone", KindNotFound},
		{"not found", "NotFound", "not found", KindNotFound},

		// Throttle errors
		{"slow down", "SlowDown", "too fast", KindThrottle},
		{"throttling", "Throttling", "throttled", KindThrottle},

		// Server errors
		{"internal error", "InternalError", "oops", KindServer},
		{"service unavailable", "ServiceUnavailable", "down", KindServer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := fmt.Errorf("downloading s3://bucket/key: %w", &mockAPIError{
				code:    tt.code,
				message: tt.message,
			})
			got := Classify(raw)

			var apiErr *Error
			if !errors.As(got, &apiErr) {
				t.Fatalf("Classify returned non-Error type: %T", got)
			}
			if apiErr.Kind != tt.wantKind {
				t.Errorf("Kind = %d, want %d", apiErr.Kind, tt.wantKind)
			}
			if apiErr.Code != tt.code {
				t.Errorf("Code = %q, want %q", apiErr.Code, tt.code)
			}
			if apiErr.Message != tt.message {
				t.Errorf("Message = %q, want %q", apiErr.Message, tt.message)
			}
			if apiErr.Hint == "" {
				t.Error("Hint is empty, expected a hint")
			}
		})
	}
}

func TestClassify_UnknownAPIError(t *testing.T) {
	raw := fmt.Errorf("downloading: %w", &mockAPIError{
		code:    "SomeUnknownCode",
		message: "something weird",
	})
	got := Classify(raw)

	// Unknown API codes should return the original error unchanged
	if got != raw {
		t.Errorf("expected original error returned for unknown code, got %T", got)
	}
}

func TestClassify_NilError(t *testing.T) {
	got := Classify(nil)
	if got != nil {
		t.Errorf("Classify(nil) = %v, want nil", got)
	}
}

func TestClassify_PlainError(t *testing.T) {
	raw := fmt.Errorf("some random error")
	got := Classify(raw)
	if got != raw {
		t.Errorf("expected original error for plain error, got %T", got)
	}
}

func TestClassify_NetworkErrors(t *testing.T) {
	tests := []struct {
		name string
		msg  string
	}{
		{"dial tcp", "dial tcp 1.2.3.4:443: connect: connection refused"},
		{"connection refused", "connection refused"},
		{"no such host", "dial tcp: lookup s3.amazonaws.com: no such host"},
		{"timeout", "i/o timeout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := fmt.Errorf("downloading s3://b/k: %s", tt.msg)
			got := Classify(raw)

			var apiErr *Error
			if !errors.As(got, &apiErr) {
				t.Fatalf("Classify returned non-Error type: %T", got)
			}
			if apiErr.Kind != KindNetwork {
				t.Errorf("Kind = %d, want KindNetwork (%d)", apiErr.Kind, KindNetwork)
			}
			if apiErr.Hint == "" {
				t.Error("Hint is empty, expected a network hint")
			}
		})
	}
}

func TestError_ErrorString(t *testing.T) {
	e := &Error{
		Kind:    KindAuth,
		Code:    "ExpiredToken",
		Message: "token has expired",
		Hint:    "Run aws configure",
	}

	s := e.Error()
	if !strings.Contains(s, "[ExpiredToken]") {
		t.Errorf("error string missing code: %s", s)
	}
	if !strings.Contains(s, "token has expired") {
		t.Errorf("error string missing message: %s", s)
	}
	if !strings.Contains(s, "Hint:") {
		t.Errorf("error string missing hint: %s", s)
	}
}

func TestError_ErrorStringNoCode(t *testing.T) {
	e := &Error{
		Kind:    KindNetwork,
		Message: "connection refused",
		Hint:    "Check network",
	}

	s := e.Error()
	if strings.Contains(s, "[]") {
		t.Errorf("error string should not have empty brackets: %s", s)
	}
	if !strings.Contains(s, "connection refused") {
		t.Errorf("error string missing message: %s", s)
	}
}

func TestError_Unwrap(t *testing.T) {
	cause := fmt.Errorf("root cause")
	e := &Error{
		Kind:  KindAuth,
		Cause: cause,
	}
	if !errors.Is(e, cause) {
		t.Error("Unwrap did not return the original cause")
	}
}
