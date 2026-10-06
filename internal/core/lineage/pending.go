package lineage

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Pending is a mark this machine owes a copy left behind: the copy was still open when
// it was moved, so it is marked on a later scan, once it has ended.
type Pending struct {
	Operation string           `json:"operation"`
	Branch    string           `json:"branch"`
	Replica   ReplicaID        `json:"replica"`
	Time      time.Time        `json:"time"`
	Location  string           `json:"location"` // where the copy is
	Key       agent.SessionKey `json:"key"`
	Path      string           `json:"path"`
	Title     string           `json:"title"`
	Mark      agent.Mark       `json:"mark"`
	// Head is the copy's last conversation node when it was moved; a copy that grew
	// afterwards was kept working on and is not marked.
	Head string `json:"head,omitempty"`
}

func pendingFile(stateDir string) string { return filepath.Join(stateDir, "pending-marks.jsonl") }

// LoadPending returns the owed marks.
func LoadPending(stateDir string) ([]Pending, error) {
	f, err := os.Open(pendingFile(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Pending
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var p Pending
		if json.Unmarshal(sc.Bytes(), &p) == nil {
			out = append(out, p)
		}
	}
	return out, sc.Err()
}

// SavePending replaces the owed marks.
func SavePending(stateDir string, ps []Pending) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	tmp := pendingFile(stateDir) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, p := range ps {
		if err := enc.Encode(p); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, pendingFile(stateDir))
}

// AddPending records an owed mark.
func AddPending(stateDir string, p Pending) error {
	ps, err := LoadPending(stateDir)
	if err != nil {
		return err
	}
	return SavePending(stateDir, append(ps, p))
}
