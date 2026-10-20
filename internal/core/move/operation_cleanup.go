package move

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

func lockOperation(state, id string) (*os.File, error) {
	if !validOperation.MatchString(id) {
		return nil, errors.New("invalid operation ID")
	}
	p := operationPath(Env{StateDir: state}, id)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return nil, err
	}
	if err := localstate.PrivateDirectory(filepath.Dir(p)); err != nil {
		return nil, err
	}
	return localstate.TryLock(p + ".lock")
}

// LockCheckpointCleanup excludes an active native apply. Uncertain operations
// and outstanding receipts retain their source for recovery. The caller may
// release only the cached checkpoint, never this operation or its undo journal.
func LockCheckpointCleanup(state, id string) (*os.File, error) {
	lock, err := lockOperation(state, id)
	if err != nil {
		return nil, err
	}
	r, err := operationLoad(Env{StateDir: state}, id)
	if os.IsNotExist(err) {
		return lock, nil
	}
	if err == nil && r.Journal != "" {
		var j *journal.Journal
		j, err = journal.Load(state, r.Journal)
		if err == nil && (j.Undone || r.Phase == "complete" && !j.PendingReceipts() && !j.PendingAcknowledgments()) {
			return lock, nil
		}
	}
	lock.Close()
	if err != nil {
		return nil, err
	}
	return nil, errors.New("checkpoint belongs to an interrupted import or pending receipt; recover or undo it before cleanup")
}
