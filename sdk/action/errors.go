package action

import (
	"errors"
	"fmt"
)

var (
	ErrEmptyData     = errors.New("data cannot be empty")
	ErrEmptyActionID = errors.New("action ID cannot be empty")
	ErrEmptyFileName = errors.New("file name cannot be empty")
	ErrNoValidAction = errors.New("no action found with the specified ID")
	ErrInvalidAction = errors.New("action is not in a valid state")
	ErrNoSupernodes  = errors.New("no valid supernodes available")
	ErrTaskCreation  = errors.New("failed to create task")
	ErrCommunication = errors.New("communication with supernode failed")
)

// SupernodeError represents an error related to supernode operations
type SupernodeError struct {
	NodeID  string
	Message string
	Err     error
}

// Error returns the error message
func (e *SupernodeError) Error() string {
	return fmt.Sprintf("supernode error (ID: %s): %s: %v", e.NodeID, e.Message, e.Err)
}

// Unwrap returns the underlying error
func (e *SupernodeError) Unwrap() error {
	return e.Err
}

// ActionError represents an error related to action operations
type ActionError struct {
	ActionID string
	Message  string
	Err      error
}

// Error returns the error message
func (e *ActionError) Error() string {
	return fmt.Sprintf("action error (ID: %s): %s: %v", e.ActionID, e.Message, e.Err)
}

// Unwrap returns the underlying error
func (e *ActionError) Unwrap() error {
	return e.Err
}
