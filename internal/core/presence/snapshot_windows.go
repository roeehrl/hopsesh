//go:build windows

package presence

import (
	"context"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Snapshot reads this machine's process table with a ToolHelp snapshot. Windows keeps a
// process's parent id after the parent exits, and ids are reused, so a parent can be a
// stranger (Classify is safe against the cycles that makes).
func Snapshot(ctx context.Context) (Table, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	t := Table{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid := int(e.ProcessID)
		t[pid] = Proc{PID: pid, PPID: int(e.ParentProcessID), Name: windows.UTF16ToString(e.ExeFile[:])}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return t, nil
}
