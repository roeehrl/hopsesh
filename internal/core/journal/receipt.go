package journal

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

// Receipt is a durable commit intention after native installation. Recovery replays
// sidecars only; it never writes the conversation again.
type Receipt struct {
	CheckpointTask string `json:"checkpointTask,omitempty"`
	Machine        string `json:"machine"`
	Path           string `json:"path"`
	Body           []byte `json:"body"`
	Native         *State `json:"native,omitempty"`
	Applied        bool   `json:"applied"`
}

func (j *Journal) WriteReceipt(fsys host.FS, machine, path string, body []byte, guardNative bool) error {
	return j.writeReceipt(fsys, Receipt{Machine: machine, Path: path, Body: body}, guardNative)
}

// WriteCloudCheckpointReceipt is reserved for a native-owned read-only task
// ledger. It can advance a reviewed rewrite to its direct child branch without
// allowing ordinary native or peer receipts to replace branch ownership.
func (j *Journal) WriteCloudCheckpointReceipt(fsys host.FS, machine, path string, body []byte, task string) error {
	if task == "" {
		return errors.New("missing checkpoint task identity")
	}
	return j.writeReceipt(fsys, Receipt{Machine: machine, Path: path, Body: body, CheckpointTask: task}, false)
}

func (j *Journal) writeReceipt(fsys host.FS, receipt Receipt, guardNative bool) error {
	if guardNative {
		if fsys == nil {
			return fmt.Errorf("cannot guard a receipt without a filesystem")
		}
		st, err := fileState(fsys, receipt.Machine, strings.TrimSuffix(receipt.Path, lineage.Suffix))
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
	if fsys == nil {
		return fmt.Errorf("source receipt queued until its machine is reachable")
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
	if j.PendingAcknowledgments() {
		return true
	}
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
	if j.ReceiptOwner == "" || j.dir == "" {
		return errors.New("journal has no durable receipt ownership token")
	}
	// Recovering a durable remote lock with our own token is safe only when no
	// other local caller is applying this journal. Hold an OS lock across the
	// remote union/write/unlock, including calls from separately loaded journals.
	local, err := localstate.TryLock(filepath.Join(j.dir, "receipt-apply.lock"))
	if err != nil {
		return fmt.Errorf("journal receipt is already being applied: %w", err)
	}
	defer local.Close()
	receipt := j.Receipts[index]
	unlock, err := receiptLock(fsys, receipt.Path, j.ReceiptOwner)
	if err != nil {
		return err
	}
	defer unlock()
	m, err := lineage.Parse(receipt.Body)
	if err != nil {
		return err
	}
	if receipt.CheckpointTask != "" {
		if _, err := fsys.Stat(strings.TrimSuffix(receipt.Path, lineage.Suffix)); !errors.Is(err, fs.ErrNotExist) {
			return errors.New("checkpoint branch transitions require a private ledger without a native transcript")
		}
	}
	if current, e := lineage.Read(fsys, strings.TrimSuffix(receipt.Path, lineage.Suffix)); e != nil {
		return e
	} else if current != nil {
		if current.Family != m.Family || current.Branch != m.Branch && !checkpointBranchTransition(current, m, receipt.CheckpointTask) {
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

func checkpointBranchTransition(current, next *lineage.Manifest, task string) bool {
	if task == "" {
		return false
	}
	for _, branch := range next.Branches {
		if branch.ID != next.Branch || branch.Parent != current.Branch {
			continue
		}
		origin := next.Replica(branch.Origin)
		if origin.Binding != task || origin.Key.Profile != task || !strings.HasPrefix(origin.Endpoint, "cloud:") {
			return false
		}
		for _, previous := range current.Replicas {
			if previous.Line == current.Branch && previous.Endpoint == origin.Endpoint && previous.Binding == task && previous.Key.Profile == task && previous.Key.Agent == origin.Key.Agent {
				return true
			}
		}
	}
	return false
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
		// Only the same durable journal token may recover its lock after a
		// process exits. Local timestamp IDs can collide across machines.
	}
	return func() { _ = fsys.Remove(lock) }, nil
}
