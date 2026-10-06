//go:build darwin

package presence

import (
	"bytes"
	"context"

	"golang.org/x/sys/unix"
)

// Snapshot reads this machine's process table from the kernel (sysctl kern.proc.all),
// without running a program.
func Snapshot(ctx context.Context) (Table, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	t := make(Table, len(kps))
	for i := range kps {
		k := &kps[i]
		pid := int(k.Proc.P_pid)
		comm := k.Proc.P_comm[:]
		if n := bytes.IndexByte(comm, 0); n >= 0 {
			comm = comm[:n]
		}
		t[pid] = Proc{PID: pid, PPID: int(k.Eproc.Ppid), Name: string(comm)}
	}
	return t, nil
}
