package agent

import (
	"context"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// WriteRecoverer verifies an already-written operation without writing native history. A
// module whose agent installs its own sessions (VendorInstaller) imports again here when
// the interrupted Write had not (doing nothing when it had), and finds the session Write
// made also when Write gave it an id of its own.
type WriteRecoverer interface {
	RecoverWrite(context.Context, Host, Install, ir.WriteRequest) (ir.WriteResult, error)
}
