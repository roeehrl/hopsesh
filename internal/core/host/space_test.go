package host

import (
	"path/filepath"
	"testing"
)

func TestLocalFreeSpaceForNotYetCreatedFile(t *testing.T) {
	free, err := localFS{}.FreeSpace(filepath.Join(t.TempDir(), "hopsesh", "archives", "new.jsonl"))
	if err != nil || free <= 0 {
		t.Fatalf("free space: %d %v", free, err)
	}
}
