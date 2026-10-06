package journal

import (
	"fmt"
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
	if err = j.WriteFile(fsys, machine, path, body, 0o600); err != nil {
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
		m, err := lineage.Parse(receipt.Body)
		if err != nil {
			return err
		}
		native := strings.TrimSuffix(receipt.Path, lineage.Suffix)
		if current, e := lineage.Read(fsys, native); e != nil {
			return e
		} else if current != nil {
			if current.Family != m.Family || current.Branch != m.Branch {
				return fmt.Errorf("receipt recovery would replace another branch")
			}
			if err = m.Merge(current); err != nil {
				return err
			}
		}
		if err = j.WriteFile(fsys, receipt.Machine, receipt.Path, m.Encode(), 0o600); err != nil {
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
