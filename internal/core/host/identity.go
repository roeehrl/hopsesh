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
	return m.RefreshIdentity(ctx)
}

// RefreshIdentity rereads the persisted endpoint for observation boundaries.
// Unlike ReadIdentity, it must detect replacement or removal after an earlier
// read. A received snapshot has only its supplied immutable identity.
func (m *Machine) RefreshIdentity(ctx context.Context) (string, error) {
	if m.IsSnapshot() {
		return m.Facts.Endpoint, nil
	}
	fsys, err := m.FS(ctx)
	if err != nil {
		return "", err
	}
	b, err := fsys.ReadFile(m.identityPath(), 128)
	if errors.Is(err, fs.ErrNotExist) {
		m.Facts.Endpoint = ""
		return "", nil
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
