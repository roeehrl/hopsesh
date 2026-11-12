package move

import (
	"context"
	"fmt"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

// SourceCursor is the native source snapshot captured by the reviewed plan.
func (p *Plan) SourceCursor() ir.Cursor {
	return ir.Cursor{Head: p.sourceState.Head, Offset: p.sourceState.Offset}
}

// ValidateSource rejects a changed source before a caller closes a destination.
// Apply also checks this snapshot immediately before applying a new operation.
func ValidateSource(ctx context.Context, p *Plan, in Input) error {
	ctx = ir.WithLimits(ctx, p.Options.Limits)
	seg, err := readSegment(ctx, in.Source, in.Session)
	if err != nil {
		return err
	}
	if seg.Cursor != p.SourceCursor() {
		return fmt.Errorf("source changed since planning; refresh the plan")
	}
	return nil
}
