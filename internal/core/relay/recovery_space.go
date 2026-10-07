package relay

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

// MaxRecoveryBytes bounds recovery ciphertext separately from chunk staging. An
// native action's digest/phase tombstone is never evicted to admit another action.
const MaxRecoveryBytes int64 = 256 << 20

var ErrRecoveryQuota = errors.New("relay recovery storage quota reached; wait for retained ciphertext to expire before accepting new work")

func transientOperation(method string) bool { return method == "observe" || method == "preview" }

func retirePassive(record Record, prefix string, now time.Time) bool {
	if !record.Transient || !transientOperation(record.Method) || record.Expires <= 0 || record.Expires > now.Unix() || record.Response.Expires > now.Unix() || record.Outcome != nil && record.Outcome.Expires > now.Unix() {
		return false
	}
	return prefix == "operation-" && record.Phase == "completed" || prefix == "outgoing-" && record.Phase == "submitted" && record.Authorization == record.Method
}

// Caller holds state.lock. Passive read records may retire only after their wire
// lease expires. Native, incomplete and uncertain action bindings never retire.
func (s Store) recoveryRecordSlot(prefix string, now time.Time) error {
	paths, err := filepath.Glob(filepath.Join(s.Directory, prefix+"*.json"))
	if err != nil || len(paths) < MaxOperationRecords {
		return err
	}
	if len(paths) > MaxOperationRecords {
		return errors.New("relay recovery record count exceeds bound")
	}
	for _, path := range paths {
		body, err := localstate.ReadPrivateFile(path, MaxWireBytes)
		if err != nil {
			return err
		}
		var record Record
		if err = json.Unmarshal(body, &record); err != nil {
			return err
		}
		if retirePassive(record, prefix, now) {
			if err = os.Remove(path); err != nil {
				return err
			}
		}
	}
	paths, err = filepath.Glob(filepath.Join(s.Directory, prefix+"*.json"))
	if err != nil {
		return err
	}
	if len(paths) >= MaxOperationRecords {
		return errors.New("relay operation record quota reached; live or native action bindings were preserved")
	}
	return nil
}

// Caller holds state.lock. Reserve an entire bounded result before starting a
// native action; a result cannot lose its recovery slot to another request.
func (s Store) recoverySpace(path string, reserve int64, now time.Time) error {
	return s.recoverySpaceLimit(path, reserve, now, MaxRecoveryBytes)
}

func (s Store) recoverySpaceLimit(path string, reserve int64, now time.Time, limit int64) error {
	if reserve < 0 || reserve > MaxWireBytes {
		return errors.New("relay recovery reservation exceeds record bound")
	}
	paths := []string{}
	for _, prefix := range []string{"operation-", "outgoing-", "large-intent-", "reply-"} {
		matches, err := filepath.Glob(filepath.Join(s.Directory, prefix+"*.json"))
		if err != nil {
			return err
		}
		if len(matches) > MaxOperationRecords {
			return errors.New("relay recovery record count exceeds bound")
		}
		paths = append(paths, matches...)
	}
	used, err := recoveryBytes(paths, path)
	if err != nil || used+reserve <= limit {
		return err
	}
	// Reclaim on admission pressure, not with an idle timer. Never evict a live
	// result, an uncertain intent or a native journal to make room.
	for _, existing := range paths {
		if strings.HasPrefix(filepath.Base(existing), "large-intent-") {
			continue // metadata-only durable request bindings
		}
		body, err := localstate.ReadPrivateFile(existing, MaxWireBytes)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.Base(existing), "reply-") {
			var envelope Envelope
			if err = json.Unmarshal(body, &envelope); err != nil {
				return err
			}
			if envelope.Expires > 0 && envelope.Expires <= now.Unix() {
				if err = os.Remove(existing); err != nil {
					return err
				}
			}
			continue
		}
		var record Record
		if err = json.Unmarshal(body, &record); err != nil {
			return err
		}
		prefix := "operation-"
		if strings.HasPrefix(filepath.Base(existing), "outgoing-") {
			prefix = "outgoing-"
		}
		if retirePassive(record, prefix, now) {
			if err = os.Remove(existing); err != nil {
				return err
			}
			continue
		}
		if prefix == "outgoing-" {
			continue
		}
		if record.Phase != "completed" {
			continue
		}
		changed := false
		if record.Response.Expires > 0 && record.Response.Expires <= now.Unix() {
			record.Response = Envelope{}
			changed = true
		}
		if record.Outcome != nil && record.Outcome.Expires <= now.Unix() {
			record.Outcome = nil
			changed = true
		}
		if changed {
			if err = writeJSON(existing, record); err != nil {
				return err
			}
		}
	}
	// Deleted expired replies disappear from the second accounting pass.
	used, err = recoveryBytes(paths, path)
	if err != nil {
		return err
	}
	if used+reserve > limit {
		return ErrRecoveryQuota
	}
	return nil
}

func recoveryBytes(paths []string, replacing string) (int64, error) {
	var used int64
	for _, path := range paths {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if !info.Mode().IsRegular() || info.Size() > MaxWireBytes {
			return 0, errors.New("relay recovery record is not a bounded regular file")
		}
		if path != replacing {
			used += info.Size()
		}
	}
	return used, nil
}

func writeRecoveryRecord(path string, record Record) error {
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(body) > MaxWireBytes {
		return errors.New("relay recovery record exceeds bound; inspect the native journal")
	}
	return writeJSON(path, record)
}
