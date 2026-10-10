package agent

import (
	"fmt"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

// SpaceReporter is a filesystem that can tell how many bytes are free for p (its nearest
// existing parent directory). Filesystems that cannot tell do not implement it.
type SpaceReporter interface {
	FreeSpace(p string) (int64, error)
}

// SpaceReserve is kept free besides the bytes written, so a transfer never fills a disk.
const SpaceReserve = 256 << 20

// CheckSpace refuses a write of need bytes at p when the filesystem reports too little
// free space (a LimitError naming the archive setting); unknown free space passes, and
// the atomic write still leaves existing files unchanged if it fails.
func CheckSpace(fsys FS, p string, need int64) error {
	sr, ok := fsys.(SpaceReporter)
	if !ok {
		return nil
	}
	free, err := sr.FreeSpace(p)
	if err != nil || free <= 0 {
		return nil
	}
	if want := need + SpaceReserve; free < want {
		return &ir.LimitError{Stage: ir.StageDisk, Limit: free, Size: want, Detail: fmt.Sprintf("only %d MiB free where hopsesh writes %s; free some disk space (nothing was written)", free>>20, p)}
	}
	return nil
}
