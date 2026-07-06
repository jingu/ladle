package ssm

import (
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// NotFoundError indicates a parameter does not exist.
type NotFoundError struct {
	Name string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("parameter %s not found", e.Name)
}

// IsNotFound reports whether err represents a missing parameter, whether it is
// a *NotFoundError from this package or the SDK's ParameterNotFound fault.
func IsNotFound(err error) bool {
	var nf *NotFoundError
	if errors.As(err, &nf) {
		return true
	}
	var pnf *types.ParameterNotFound
	return errors.As(err, &pnf)
}
