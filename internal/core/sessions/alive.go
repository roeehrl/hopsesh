package sessions

import (
	"errors"
	"os"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/fsys"
)

// LocalAlive reports which pids are running on this machine.
func LocalAlive(pids []int) map[int]bool {
	out := make(map[int]bool, len(pids))
	for _, pid := range pids {
		out[pid] = processExists(pid)
	}
	return out
}

func processExists(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return signalZero(p)
}

// ErrStillRunning means a stopped session did not exit in time.
var ErrStillRunning = errors.New("the session did not exit in time; quit it yourself (type /exit in it), then try again")

// StopLocal asks a Claude Code process on this machine to exit (SIGTERM, which lets it
// finish writing its transcript) and waits for it. It first re-reads the live registry
// to confirm the pid still belongs to sessionID, so a recycled pid is never signalled.
func StopLocal(configDir, sessionID string, pid int, wait time.Duration) error {
	loc := Locator{FS: fsys.Local{}, ConfigDir: configDir}
	live, err := loc.LiveRegistry(LocalAlive)
	if err != nil {
		return err
	}
	owned := false
	for _, le := range live {
		if le.PID == pid && le.SessionID == sessionID {
			owned = true
		}
	}
	if !owned {
		return nil // already gone
	}
	if err := terminate(pid); err != nil {
		return err
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return ErrStillRunning
}
