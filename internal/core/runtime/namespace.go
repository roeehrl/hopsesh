// Package runtime owns shared local observation and private per-user IPC. It has
// no desktop dependency and never exposes local administration over the relay.
package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

const Protocol = 1

var ErrOwned = errors.New("a runtime already owns this settings/state namespace")

type Namespace struct{ ID, Config, State, Directory, Address string }

// canonical also resolves existing parents of directories that do not exist yet.
func canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	tail := []string{}
	for {
		resolved, e := filepath.EvalSymlinks(abs)
		if e == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(e, os.ErrNotExist) {
			return "", e
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", e
		}
		tail = append(tail, filepath.Base(abs))
		abs = parent
	}
}
func NewNamespace(config, state string) (Namespace, error) {
	var n Namespace
	var err error
	n.Config, err = canonical(config)
	if err != nil {
		return n, err
	}
	n.State, err = canonical(state)
	if err != nil {
		return n, err
	}
	u, err := user.Current()
	if err != nil {
		return n, err
	}
	h := sha256.Sum256([]byte(strings.Join([]string{u.Uid, n.Config, n.State}, "\x00")))
	n.ID = hex.EncodeToString(h[:16])
	// A short private directory avoids Unix socket path limits for deep state roots.
	// HOME/XDG/LOCALAPPDATA may differ between a login service, cloud hook and
	// desktop launch. Ownership is scoped to the actual OS user and namespace,
	// so those process overrides must never create a second lock directory.
	base := filepath.Join(u.HomeDir, ".cache")
	switch runtime.GOOS {
	case "darwin":
		base = filepath.Join(u.HomeDir, "Library", "Caches")
	case "windows":
		base = filepath.Join(u.HomeDir, "AppData", "Local")
	}
	if !filepath.IsAbs(base) {
		return n, errors.New("OS user has no absolute home directory")
	}
	n.Directory = filepath.Join(base, "hopsesh", "runtime", n.ID)
	n.Address = address(n.Directory, n.ID)
	return n, nil
}
func (n Namespace) prepare() error {
	if err := os.MkdirAll(n.Directory, 0700); err != nil {
		return err
	}
	return privateDirectory(n.Directory)
}
