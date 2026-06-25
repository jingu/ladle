// Package apierror classifies cloud API errors and provides user-friendly messages.
package apierror

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/aws/smithy-go"
	"google.golang.org/api/googleapi"
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

	// Check Azure SDK response errors
	var azErr *azcore.ResponseError
	if errors.As(err, &azErr) {
		if kind := classifyAPICode(azErr.ErrorCode); kind != KindUnknown {
			return kind, azErr.ErrorCode, err.Error()
		}
		if kind := classifyHTTPStatus(azErr.StatusCode); kind != KindUnknown {
			code := azErr.ErrorCode
			if code == "" {
				code = fmt.Sprintf("HTTP %d", azErr.StatusCode)
			}
			return kind, code, err.Error()
		}
	}

	// Check GCS sentinel errors, which the SDK returns instead of a googleapi.Error.
	if errors.Is(err, storage.ErrObjectNotExist) || errors.Is(err, storage.ErrBucketNotExist) {
		return KindNotFound, "NotFound", err.Error()
	}

	// Check Google API errors (GCS). The Code field is the HTTP status code.
	var gErr *googleapi.Error
	if errors.As(err, &gErr) {
		if kind := classifyHTTPStatus(gErr.Code); kind != KindUnknown {
			return kind, fmt.Sprintf("HTTP %d", gErr.Code), err.Error()
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
		"InvalidIdentityToken",
		// Azure
		"AuthenticationFailed",
		"InvalidAuthenticationInfo":
		return KindAuth

	// Permission
	case "AccessDenied",
		"AccessDeniedException",
		"AccountProblem",
		"AllAccessDisabled",
		"InvalidAccessKeyId",
		"UnauthorizedAccess",
		// Azure
		"AuthorizationFailure",
		"AuthorizationPermissionMismatch",
		"InsufficientAccountPermissions",
		"AccountIsDisabled":
		return KindPermission

	// Not found
	case "NoSuchBucket",
		"NoSuchKey",
		"NotFound",
		"NoSuchVersion",
		"404",
		// Azure
		"BlobNotFound",
		"ContainerNotFound":
		return KindNotFound

	// Throttling
	case "SlowDown",
		"Throttling",
		"ThrottlingException",
		"RequestLimitExceeded",
		"TooManyRequestsException",
		// Azure
		"ServerBusy":
		return KindThrottle

	// Server errors
	case "InternalError",
		"InternalFailure",
		"ServiceUnavailable",
		"ServiceException",
		// Azure
		"OperationTimedOut":
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
		return "Check your credentials. For AWS, run 'aws configure' or verify AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY (or try --profile). For GCS, run 'gcloud auth application-default login' or set GOOGLE_APPLICATION_CREDENTIALS. For Azure, run 'az login' or verify AZURE_STORAGE_* environment variables."
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
