//go:build !darwin && !linux && !windows

package presence

import (
	"context"
	"errors"
	"runtime"
)

// Snapshot is not available on this system.
func Snapshot(context.Context) (Table, error) {
	return nil, errors.New("reading the process table is not supported on " + runtime.GOOS)
}
