package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

const relayStateLimit = 64 << 20

type relayTransfer struct {
	Peer      string               `json:"peer"`
	Endpoint  string               `json:"endpoint"`
	Operation string               `json:"operation"`
	Request   peer.PlanRequest     `json:"request"`
	Prepared  json.RawMessage      `json:"prepared"`
	Writes    []host.SnapshotWrite `json:"writes,omitempty"`
	Reply     *peer.ApplyReply     `json:"reply,omitempty"`
}

func (a *App) relayTransferPath(peer, op string) string {
	sum := sha256.Sum256([]byte(peer + "\x00" + op))
	return filepath.Join(a.StateDir, "relay-transfers", hex.EncodeToString(sum[:])+".json")
}
func (a *App) saveRelayTransfer(r *relayTransfer) error {
	path := a.relayTransferPath(r.Peer, r.Operation)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := localstate.PrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(data) > relayStateLimit {
		return errors.New("relay transfer recovery record exceeds quota")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".transfer-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func (a *App) loadRelayTransfer(from, op string) (*relayTransfer, error) {
	r, err := readRelayTransfer(a.relayTransferPath(from, op))
	if err != nil {
		return nil, err
	}
	if r.Peer != from || r.Operation != op {
		return nil, errors.New("relay transfer belongs to another peer or operation")
	}
	return r, nil
}
func readRelayTransfer(path string) (*relayTransfer, error) {
	if err := localstate.PrivateFile(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, relayStateLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > relayStateLimit {
		return nil, errors.New("relay recovery record exceeds limit")
	}
	var r relayTransfer
	if err = json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func restoreSnapshotWrites(m *host.Machine, writes []host.SnapshotWrite) error {
	f, err := m.FS(context.Background())
	if err != nil {
		return err
	}
	for _, w := range writes {
		switch w.Op {
		case "write":
			err = f.WriteFile(w.Path, w.Data, w.Perm)
		case "append":
			err = f.Append(w.Path, w.Data, w.Append)
		case "rename":
			err = f.Rename(w.From, w.Path)
		default:
			return errors.New("invalid persisted snapshot write")
		}
		if err != nil {
			return err
		}
	}
	return nil
}
