package agent

import (
	"errors"
	"fmt"
)

// Errors a module returns, wrapped with detail. The user interfaces map each to a state;
// modules never invent other top-level kinds.
var (
	ErrNotInstalled    = errors.New("the agent is not installed there")
	ErrNotFound        = errors.New("session not found")
	ErrLive            = errors.New("the session is open")
	ErrUnsupported     = errors.New("not supported")
	ErrAccountMismatch = errors.New("signed in to a different account")
	ErrDiverged        = errors.New("both copies changed")
	ErrDenied          = errors.New("denied by the host")
)

// FormatError is native data a module could not read.
type FormatError struct {
	Path string
	Line int // 1-based, 0 when unknown
	Err  error
}

func (e *FormatError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d: %v", e.Path, e.Line, e.Err)
	}
	return e.Path + ": " + e.Err.Error()
}

func (e *FormatError) Unwrap() error { return e.Err }

// VersionError is an agent version the module cannot handle (Blocking) or has not been
// tested with.
type VersionError struct {
	Agent    ID
	Have     string
	Tested   []string
	Blocking bool
}

func (e *VersionError) Error() string {
	if e.Blocking {
		return fmt.Sprintf("%s %s is not supported", e.Agent, e.Have)
	}
	return fmt.Sprintf("%s %s has not been tested with hopsesh", e.Agent, e.Have)
}
