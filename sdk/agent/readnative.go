package agent

import (
	"context"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

// ReadNative reads a whole native file (fork verification, write recovery, staged
// verification) within the operation's native file budget. A file over the budget is
// a LimitError naming the setting; nothing is read partially.
func ReadNative(ctx context.Context, fsys FS, p string) ([]byte, error) {
	limit := ir.LimitsFrom(ctx).NativeFileBytes
	if fi, err := fsys.Stat(p); err == nil && fi.Size() > limit {
		return nil, &ir.LimitError{Stage: ir.StageNative, Limit: limit, Size: fi.Size(), Detail: p}
	}
	b, err := fsys.ReadFile(p, limit)
	if err != nil {
		// The file may have grown between Stat and ReadFile.
		if fi, e := fsys.Stat(p); e == nil && fi.Size() > limit {
			return nil, &ir.LimitError{Stage: ir.StageNative, Limit: limit, Size: fi.Size(), Detail: p}
		}
	}
	return b, err
}
