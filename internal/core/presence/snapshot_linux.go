//go:build linux

package presence

import "context"

// Snapshot reads this machine's process table from /proc.
func Snapshot(ctx context.Context) (Table, error) { return readProc(ctx, procRoot) }
