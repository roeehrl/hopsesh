package host

import (
	"os"
	"path/filepath"
)

// nearestDir is p's nearest existing directory (a file's parent folder may not exist yet).
func nearestDir(p string) string {
	d := p
	for {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return d
		}
		d = parent
	}
}

// FreeSpace is the space available to this user where p is (or would be) written.
func (localFS) FreeSpace(p string) (int64, error) { return freeSpace(nearestDir(p)) }
