package journal

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
)

// Receipt is a durable commit intention after native installation. Recovery replays
// sidecars only; it never writes the conversation again.
type Receipt struct {
	Machine string `json:"machine"`
	Path    string `json:"path"`
	Body    []byte `json:"body"`
	Native  *State `json:"native,omitempty"`
	Applied bool   `json:"applied"`
}

func (j *Journal) WriteReceipt(fsys host.FS, machine, path string, body []byte, guardNative bool) error {
	receipt := Receipt{Machine: machine, Path: path, Body: body}
	if guardNative {
		st, err := fileState(fsys, machine, strings.TrimSuffix(path, lineage.Suffix))
		if err != nil {
			return err
		}
		receipt.Native = &st
	}
	j.mu.Lock()
	index := len(j.Receipts)
	j.Receipts = append(j.Receipts, receipt)
	err := j.saveLocked()
	j.mu.Unlock()
	if err != nil {
		return err
	}
	if err = j.applyReceipt(fsys, index); err != nil {
		return err
	}
	j.mu.Lock()
	j.Receipts[index].Applied = true
	err = j.saveLocked()
	j.mu.Unlock()
	return err
}
func (j *Journal) RecoverReceipts(fsFor func(string) (host.FS, error)) error {
	for i, receipt := range j.Receipts {
		if receipt.Applied {
			continue
		}
		fsys, err := fsFor(receipt.Machine)
		if err != nil {
			return err
		}
		if receipt.Native != nil {
			now, err := fileState(fsys, receipt.Machine, receipt.Native.Path)
			if err != nil {
				return err
			}
			if now.Sum != receipt.Native.Sum {
				return fmt.Errorf("%w: destination used before receipt recovery", ErrChanged)
			}
		}
		if err = j.applyReceipt(fsys, i); err != nil {
			return err
		}
		j.mu.Lock()
		j.Receipts[i].Applied = true
		err = j.saveLocked()
		j.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}
func (j *Journal) PendingReceipts() bool {
	for _, r := range j.Receipts {
		if !r.Applied {
			return true
		}
	}
	return false
}

// The source may have acknowledged another destination since planning. Serialize
// sidecar union on real filesystems and merge fresh metadata before writing it.
// Snapshot filesystems are private to one peer request; their returned writes are
// merged again by the sender against its actual filesystem.
func (j *Journal) applyReceipt(fsys host.FS, index int) error {
	receipt := j.Receipts[index]
	unlock, err := receiptLock(fsys, receipt.Path, j.ID)
	if err != nil {
		return err
	}
	defer unlock()
	m, err := lineage.Parse(receipt.Body)
	if err != nil {
		return err
	}
	if current, e := lineage.Read(fsys, strings.TrimSuffix(receipt.Path, lineage.Suffix)); e != nil {
		return e
	} else if current != nil {
		if current.Family != m.Family || current.Branch != m.Branch {
			return fmt.Errorf("receipt would replace another branch")
		}
		if err = m.Merge(current); err != nil {
			return err
		}
	}
	if err = m.Validate(); err != nil {
		return err
	}
	body := m.Encode()
	j.mu.Lock()
	j.Receipts[index].Body = body
	err = j.saveLocked()
	j.mu.Unlock()
	if err != nil {
		return err
	}
	if err = j.WriteFile(fsys, receipt.Machine, receipt.Path, body, 0600); err != nil {
		return err
	}
	// Refresh only this metadata checksum. Sealing the whole journal here would
	// incorrectly bless native work written after the original transfer.
	after, err := fileState(fsys, receipt.Machine, receipt.Path)
	if err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	found := false
	for i, s := range j.After {
		if s.Machine == after.Machine && s.Path == after.Path {
			j.After[i] = after
			found = true
			break
		}
	}
	if !found {
		j.After = append(j.After, after)
	}
	return j.saveLocked()
}

func receiptLock(fsys host.FS, path, owner string) (func(), error) {
	exclusive, ok := fsys.(interface {
		CreateExclusive(string, []byte, fs.FileMode) error
	})
	if !ok {
		return func() {}, nil
	}
	lock := path + ".receipt-lock"
	if err := exclusive.CreateExclusive(lock, []byte(owner), 0600); err != nil {
		existing, e := fsys.ReadFile(lock, 128)
		if e != nil || string(existing) != owner {
			return nil, fmt.Errorf("lineage receipt is locked by another operation; acknowledgement remains pending: %w", err)
		}
		// Only the same durable journal may recover its lock after a process exits.
	}
	return func() { _ = fsys.Remove(lock) }, nil
}
