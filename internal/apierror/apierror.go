// Package apierror classifies cloud API errors and provides user-friendly messages.
package apierror

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/smithy-go"
)

// Kind represents the category of an API error.
type Kind int

const (
	KindUnknown Kind = iota
	KindAuth
	KindPermission
	KindNotFound
	KindThrottle
	KindServer
	KindNetwork
)

// Classify inspects an error and returns a user-friendly message.
// If the error is not a recognized API error, it returns the original error unchanged.
func Classify(err error) error {
	if err == nil {
		return nil
	}

	kind, code, msg := inspect(err)
	if kind == KindUnknown {
		return err
	}

	return &Error{
		Kind:    kind,
		Code:    code,
		Message: msg,
		Hint:    hintFor(kind),
		Cause:   err,
	}
}

// Error is a classified API error with a user-friendly hint.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Hint    string
	Cause   error
}

func (e *Error) Error() string {
	var b strings.Builder
	if e.Code != "" {
		fmt.Fprintf(&b, "[%s] ", e.Code)
	}
	b.WriteString(e.Message)
	if e.Hint != "" {
		fmt.Fprintf(&b, "\nHint: %s", e.Hint)
	}
	return b.String()
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func inspect(err error) (Kind, string, string) {
	// Check smithy API errors (covers all AWS SDK v2 errors)
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		msg := apiErr.ErrorMessage()
		kind := classifyAPICode(code)
		if kind != KindUnknown {
			return kind, code, msg
		}
	}

	// Check HTTP response errors for status-code-based classification
	var httpErr *smithy.OperationError
	if errors.As(err, &httpErr) {
		var respErr interface{ HTTPStatusCode() int }
		if errors.As(httpErr.Err, &respErr) {
			kind := classifyHTTPStatus(respErr.HTTPStatusCode())
			if kind != KindUnknown {
				return kind, fmt.Sprintf("HTTP %d", respErr.HTTPStatusCode()), httpErr.Error()
			}
		}
	}

	// Check for generic network-level errors
	if isNetworkError(err) {
		return KindNetwork, "", err.Error()
	}

	return KindUnknown, "", ""
}

func classifyAPICode(code string) Kind {
	switch code {
	// Authentication
	case "InvalidClientTokenId",
		"AuthFailure",
		"SignatureDoesNotMatch",
		"IncompleteSignature",
		"MissingAuthenticationToken",
		"ExpiredToken",
		"ExpiredTokenException",
		"InvalidIdentityToken":
		return KindAuth

	// Permission
	case "AccessDenied",
		"AccessDeniedException",
		"AccountProblem",
		"AllAccessDisabled",
		"InvalidAccessKeyId",
		"UnauthorizedAccess":
		return KindPermission

	// Not found
	case "NoSuchBucket",
		"NoSuchKey",
		"NotFound",
		"NoSuchVersion",
		"404":
		return KindNotFound

	// Throttling
	case "SlowDown",
		"Throttling",
		"ThrottlingException",
		"RequestLimitExceeded",
		"TooManyRequestsException":
		return KindThrottle

	// Server errors
	case "InternalError",
		"InternalFailure",
		"ServiceUnavailable",
		"ServiceException":
		return KindServer
	}
	return KindUnknown
}

func classifyHTTPStatus(status int) Kind {
	switch {
	case status == http.StatusUnauthorized:
		return KindAuth
	case status == http.StatusForbidden:
		return KindPermission
	case status == http.StatusNotFound:
		return KindNotFound
	case status == http.StatusTooManyRequests:
		return KindThrottle
	case status >= 500:
		return KindServer
	}
	return KindUnknown
}

func isNetworkError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "dial tcp") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout")
}

func hintFor(kind Kind) string {
	switch kind {
	case KindAuth:
		return "Check your credentials. Run 'aws configure' or verify AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY environment variables. You can also try --profile to use a named profile."
	case KindPermission:
		return "You don't have permission for this operation. Check the IAM policy attached to your credentials, or verify the bucket policy allows access."
	case KindNotFound:
		return "The specified bucket or object was not found. Check the URI for typos, and verify the --region flag if the bucket is in a different region."
	case KindThrottle:
		return "Request was throttled by the service. Please wait a moment and try again."
	case KindServer:
		return "The cloud service returned a server error. This is usually temporary — please try again shortly."
	case KindNetwork:
		return "Could not connect to the service. Check your network connection, proxy settings, and --endpoint-url if using a custom endpoint."
	}
	return ""
}
