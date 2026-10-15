package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// PrepareIdentity reserves a persistent endpoint identifier, independent of SSH aliases and display
// names. A plan reserves a new identifier in memory; only Apply persists it.
func (m *Machine) PrepareIdentity(ctx context.Context) (string, error) {
	fsys, err := m.FS(ctx)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Facts.Endpoint != "" {
		return m.Facts.Endpoint, nil
	}
	path := m.identityPath()
	b, err := fsys.ReadFile(path, 128)
	if err == nil {
		value := strings.TrimSpace(string(b))
		raw, e := hex.DecodeString(value)
		if e != nil || len(raw) != 32 {
			return "", fmt.Errorf("invalid endpoint identity in %s", path)
		}
		m.Facts.Endpoint = value
		return value, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if m.snap != nil {
		return "", fmt.Errorf("sender did not supply an endpoint identity")
	}
	if kept, err := m.kept(); err != nil || kept != "" {
		m.Facts.Endpoint = kept
		return kept, err
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", err
	}
	m.Facts.Endpoint = hex.EncodeToString(raw)
	return m.Facts.Endpoint, nil
}
func (m *Machine) CommitIdentity(ctx context.Context) error {
	id, err := m.PrepareIdentity(ctx)
	if err != nil {
		return err
	}
	if m.IsSnapshot() {
		return nil
	}
	fsys, err := m.FS(ctx)
	if err != nil {
		return err
	}
	path := m.identityPath()
	b, err := fsys.ReadFile(path, 128)
	if err == nil {
		if strings.TrimSpace(string(b)) != id {
			return fmt.Errorf("endpoint identity changed since planning; refresh the plan")
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !m.HopseshThere() {
		// Reaching a machine over SSH never writes hopsesh's files there: this machine
		// keeps its identity until hopsesh is set up on it.
		if m.Keeper == nil {
			return fmt.Errorf("%s has no hopsesh, and nothing here keeps its identity", m.Name)
		}
		return m.Keeper.Keep(id)
	}
	exclusive, ok := fsys.(interface {
		CreateExclusive(string, []byte, fs.FileMode) error
	})
	if !ok {
		return fmt.Errorf("filesystem cannot create a persistent endpoint atomically")
	}
	if err = exclusive.CreateExclusive(path, []byte(id+"\n"), 0o600); errors.Is(err, fs.ErrExist) {
		b, e := fsys.ReadFile(path, 128)
		if e != nil {
			return e
		}
		if strings.TrimSpace(string(b)) == id {
			return nil
		}
		return fmt.Errorf("endpoint initialized by another transfer; refresh the plan")
	}
	return err
}

// Explicit configuration folders are independent installations, even under one OS
// account. This also keeps isolated test homes from sharing one native replica ID.
func (m *Machine) identityPath() string {
	if dir := m.Facts.Env["HOPSESH_CONFIG_DIR"]; dir != "" {
		return m.Path().Join(dir, "endpoint-id")
	}
	return m.Path().Join(m.Facts.Home, ".hopsesh", "endpoint-id")
}

// ReadIdentity reads an already initialized endpoint without reserving or writing one.
func (m *Machine) ReadIdentity(ctx context.Context) (string, error) {
	if m.Facts.Endpoint != "" {
		return m.Facts.Endpoint, nil
	}
	fsys, err := m.FS(ctx)
	if err != nil {
		return "", err
	}
	b, err := fsys.ReadFile(m.identityPath(), 128)
	if errors.Is(err, fs.ErrNotExist) {
		id, err := m.kept()
		if id != "" {
			m.Facts.Endpoint = id
		}
		return id, err
	}
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	raw, err := hex.DecodeString(id)
	if err != nil || len(raw) != 32 {
		return "", fmt.Errorf("invalid endpoint identity")
	}
	m.Facts.Endpoint = id
	return id, nil
}

// Keeper remembers, on this machine, the identity of a machine hopsesh is not installed
// on (Machine.HopseshThere), so that reaching it over SSH writes nothing there.
type Keeper interface {
	Kept() (string, error) // "" when none was kept
	// Keep records id, unless another one is kept already (an error then).
	Keep(id string) error
}

// HopseshThere reports whether hopsesh runs on the machine, so its own files may be
// written there: this machine, a sender's snapshot, or a probe that found hopsesh.
func (m *Machine) HopseshThere() bool {
	return m.Local || m.snap != nil || m.Facts.Binaries[Hopsesh.Name].Path != ""
}

// kept is the identity this machine keeps for one without hopsesh ("" when none).
func (m *Machine) kept() (string, error) {
	if m.HopseshThere() || m.Keeper == nil {
		return "", nil
	}
	id, err := m.Keeper.Kept()
	if err != nil || id == "" {
		return "", err
	}
	if raw, e := hex.DecodeString(id); e != nil || len(raw) != 32 {
		return "", fmt.Errorf("invalid kept endpoint identity for %s", m.Name)
	}
	return id, nil
}
