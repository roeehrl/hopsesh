package agent

import (
	"context"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// WriteRecoverer verifies an already-written operation without writing native history.
type WriteRecoverer interface {
	RecoverWrite(context.Context, Host, Install, ir.WriteRequest) (ir.WriteResult, error)
}
