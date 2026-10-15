package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// endpointKeeper keeps, in this machine's state, the identity of a machine reached over
// SSH that has no hopsesh: one file per configured machine name. Setting hopsesh up there
// writes the same identity to it, so lineage recorded before stays that machine's.
type endpointKeeper struct{ path string }

func (a *App) endpointKeeper(machine string) endpointKeeper {
	sum := sha256.Sum256([]byte(machine))
	return endpointKeeper{filepath.Join(a.StateDir, "machines", hex.EncodeToString(sum[:8]), "endpoint-id")}
}

func (k endpointKeeper) Kept() (string, error) {
	b, err := os.ReadFile(k.path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

func (k endpointKeeper) Keep(id string) error {
	if err := os.MkdirAll(filepath.Dir(k.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(k.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		kept, e := k.Kept()
		if e != nil {
			return e
		}
		if kept != id {
			return fmt.Errorf("endpoint initialized by another transfer; refresh the plan")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = f.WriteString(id + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
