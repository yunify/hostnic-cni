package errors

import (
	"fmt"
	"strings"
	"syscall"

	"github.com/yunify/hostnic-cni/pkg/types"
)

type ErrorType string

const (
	ResourceNotFound ErrorType = "ResourceNotFound"
	ServerError      ErrorType = "CommonServerError"
)

// Error is an implementation of the 'error' interface, which represents an
// error of server.
type Error struct {
	Type    ErrorType
	Message string
	types.ResourceType
	Action      string
	ResouceName string
}

//Error is method of error interface
func (e *Error) Error() string {
	return fmt.Sprintf("[%s] happened when [%s] type: [%s] name: [%s], msg: [%s]", e.Type, e.Action, e.ResourceType, e.ResouceName, e.Message)
}

func NewResourceNotFoundError(resource types.ResourceType, name string, message ...string) error {
	e := &Error{
		Type:         ResourceNotFound,
		ResourceType: resource,
		Action:       "GetResource",
	}
	if len(message) > 0 {
		e.Message = message[0]
	}
	return e
}

func IsResourceNotFound(e error) bool {
	er, ok := e.(*Error)
	if ok && er.Type == ResourceNotFound {
		return true
	}
	return false
}

func NewCommonServerError(resource types.ResourceType, name, action, message string) error {
	return &Error{
		Type:         ServerError,
		ResourceType: resource,
		Message:      message,
		Action:       action,
	}
}

func IsCommonServerError(e error) bool {
	er, ok := e.(*Error)
	if ok && er.Type == ServerError {
		return true
	}
	return false
}


// ContainsNoSuchRule report whether the rule is not exist
func ContainsNoSuchRule(err error) bool {
	if errno, ok := err.(syscall.Errno); ok {
		return errno == syscall.ENOENT
	}
	return false
}

// IsRuleExistsError report whether the rule is exist
func IsRuleExistsError(err error) bool {
	if errno, ok := err.(syscall.Errno); ok {
		return errno == syscall.EEXIST
	}
	return false
}

func ContainChainExistErr(err error) bool {
	return strings.Contains(err.Error(), "Chain already exists")
}

