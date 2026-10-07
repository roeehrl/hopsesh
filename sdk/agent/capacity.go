package agent

import (
	"context"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

// ContextSizer measures the destination's active context using its native semantics.
// A nil session resolves capacity for a new conversation on this host/account.
type ContextSizer interface {
	ContextCapacity(context.Context, Host, Install, *Summary) (ir.Capacity, error)
}

func CapacityFor(ctx context.Context, m Module, h Host, in Install, s *Summary) (ir.Capacity, error) {
	if c, ok := m.(ContextSizer); ok {
		return c.ContextCapacity(ctx, h, in, s)
	}
	return ir.Capacity{Source: "unknown adapter; conservative fallback", Unknown: s != nil}, nil
}
