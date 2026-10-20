package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

const checkpointTombstoneLimit = 10000

type checkpointTombstone struct {
	Operation string    `json:"operation"`
	Removed   time.Time `json:"removed"`
}

type CloudCheckpointCacheEntry struct {
	Operation string    `json:"operation"`
	Bytes     int64     `json:"bytes"`
	Reviewed  time.Time `json:"reviewed"`
	Started   bool      `json:"started"`
}

func checkpointOperationValid(id string) bool {
	return len(id) > 0 && len(id) <= 100 && strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") == ""
}

func checkpointRetired(root, id string) error {
	_, err := readCheckpointTombstone(root, id)
	if err == nil {
		return errors.New("cached cloud checkpoint was removed; prepare a new operation ID")
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func readCheckpointTombstone(root, id string) (checkpointTombstone, error) {
	var marker checkpointTombstone
	parent := filepath.Join(root, "retired")
	if err := localstate.PrivateDirectory(parent); err != nil {
		return marker, err
	}
	b, err := localstate.ReadPrivateFile(filepath.Join(parent, id+".json"), 1024)
	if err != nil {
		return marker, err
	}
	if json.Unmarshal(b, &marker) != nil || marker.Operation != id || marker.Removed.IsZero() {
		return marker, errors.New("invalid checkpoint retirement record")
	}
	return marker, nil
}

// CloudCheckpointCache lists metadata only. It neither decodes conversation
// payloads nor contacts a provider, and works after relay access is revoked.
func (a *App) CloudCheckpointCache(ctx context.Context) ([]CloudCheckpointCacheEntry, error) {
	root := filepath.Join(a.StateDir, "cloud-checkpoints")
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return []CloudCheckpointCacheEntry{}, nil
	} else if err != nil {
		return nil, err
	}
	if err := localstate.PrivateDirectory(root); err != nil {
		return nil, err
	}
	lock, err := localstate.Lock(ctx, filepath.Join(root, ".admission.lock"))
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	result := []CloudCheckpointCacheEntry{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if !checkpointOperationValid(id) {
			return nil, errors.New("invalid checkpoint cache entry")
		}
		st, err := e.Info()
		if err != nil || !st.Mode().IsRegular() {
			return nil, errors.New("checkpoint cache entry must be a regular file")
		}
		_, err = os.Lstat(filepath.Join(a.StateDir, "operations", id+".json"))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		result = append(result, CloudCheckpointCacheEntry{Operation: id, Bytes: st.Size(), Reviewed: st.ModTime(), Started: err == nil})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Reviewed.After(result[j].Reviewed) })
	return result, nil
}

// RemoveCloudCheckpoint retires a reviewed operation, preserving the source
// ledger and every native file, operation record and undo journal. A small
// durable tombstone is published before deletion so a lost response or crash
// cannot cause the same ID to export a newer conversation or install twice.
func (a *App) RemoveCloudCheckpoint(ctx context.Context, id string) error {
	if !checkpointOperationValid(id) {
		return errors.New("invalid checkpoint operation ID")
	}
	root := filepath.Join(a.StateDir, "cloud-checkpoints")
	if err := localstate.PrivateDirectory(root); err != nil {
		return err
	}
	// Unknown IDs must not allocate persistent operation lock files.
	if _, err := os.Lstat(filepath.Join(root, id+".json")); os.IsNotExist(err) {
		if _, err := readCheckpointTombstone(root, id); err == nil || !os.IsNotExist(err) {
			return err
		}
		return errors.New("cloud checkpoint operation was not found")
	} else if err != nil {
		return err
	}
	operation, err := move.LockCheckpointCleanup(a.StateDir, id)
	if err != nil {
		return err
	}
	defer operation.Close()
	lock, err := localstate.Lock(ctx, filepath.Join(root, ".admission.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	p := filepath.Join(root, id+".json")
	if st, err := os.Lstat(p); err != nil && !os.IsNotExist(err) {
		return err
	} else if err == nil && !st.Mode().IsRegular() {
		return errors.New("checkpoint cache entry must be a regular file")
	} else if os.IsNotExist(err) {
		// A private, existing marker makes cleanup an idempotent retry.
		if _, e := readCheckpointTombstone(root, id); e == nil || !os.IsNotExist(e) {
			return e
		}
		return errors.New("cloud checkpoint operation was not found")
	}
	retired := filepath.Join(root, "retired")
	if err := os.MkdirAll(retired, 0700); err != nil {
		return err
	}
	if err := localstate.PrivateDirectory(retired); err != nil {
		return err
	}
	marker := filepath.Join(retired, id+".json")
	if _, err := readCheckpointTombstone(root, id); os.IsNotExist(err) {
		entries, err := os.ReadDir(retired)
		if err != nil {
			return err
		}
		if len(entries) >= checkpointTombstoneLimit {
			return errors.New("checkpoint cleanup history reached its private metadata limit")
		}
		if err = saveRelayRecord(marker, checkpointTombstone{id, time.Now().UTC()}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return os.Remove(p)
}
