package agent

import (
	"errors"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

type spaceFS struct {
	FS
	free int64
}

func (f spaceFS) FreeSpace(string) (int64, error) { return f.free, nil }

func TestCheckSpace(t *testing.T) {
	if err := CheckSpace(spaceFS{free: 1 << 30}, "/x", 100<<20); err != nil {
		t.Fatal(err)
	}
	err := CheckSpace(spaceFS{free: 300 << 20}, "/x", 100<<20)
	if le, ok := ir.AsLimit(err); !ok || le.Stage != ir.StageDisk || !errors.Is(err, ir.ErrLimit) {
		t.Fatalf("want a disk LimitError, got %v", err)
	}
	if err := CheckSpace(struct{ FS }{}, "/x", 1<<40); err != nil {
		t.Fatal("unknown free space must not block")
	}
}
