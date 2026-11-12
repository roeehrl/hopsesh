package app

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// An owner record is how another program on this machine (a phone companion's host) says
// it runs a session: ~/.hopsesh/owners/<program>/<pid>.json, written by that program while
// the process runs. hopsesh only reads them: a move's plan names the owner, and the client
// API refuses to apply it until the owner stopped the session. docs/client-api.md is the
// contract.

// OwnerRecord is one record.
type OwnerRecord struct {
	Owner    string           `json:"owner"`
	PID      int              `json:"pid"`
	Started  string           `json:"started,omitempty"`
	Session  agent.SessionKey `json:"session"`
	Endpoint string           `json:"endpoint,omitempty"`
	// Stop is how to ask the owner to stop it, in its own words (shown, never run).
	Stop string `json:"stop,omitempty"`
}

var ownerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// maxOwnerRecord bounds one record; maxOwnerRecords, how many are read.
const (
	maxOwnerRecord  = 16 << 10
	maxOwnerRecords = 512
)

// OwnersDir is where other programs' owner records are.
func OwnersDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".hopsesh", "owners")
}

// ExternalOwners reads the owner records whose process still runs, by session.
func (a *App) ExternalOwners() map[agent.SessionKey][]OwnerRecord {
	return readOwnerRecords(OwnersDir(), host.ProcessExists)
}

func readOwnerRecords(dir string, alive func(int) bool) map[agent.SessionKey][]OwnerRecord {
	programs, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := map[agent.SessionKey][]OwnerRecord{}
	n := 0
	for _, p := range programs {
		if !p.IsDir() || !ownerName.MatchString(p.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, p.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if n >= maxOwnerRecords {
				return out
			}
			pid, err := strconv.Atoi(strings.TrimSuffix(f.Name(), ".json"))
			if err != nil || pid <= 0 || !strings.HasSuffix(f.Name(), ".json") || !f.Type().IsRegular() {
				continue
			}
			n++
			r, ok := readOwnerRecord(filepath.Join(dir, p.Name(), f.Name()))
			if !ok || r.Owner != p.Name() || r.PID != pid || r.Session.Agent == "" || r.Session.Session == "" || !alive(pid) {
				continue
			}
			out[r.Session] = append(out[r.Session], r)
		}
	}
	return out
}

func readOwnerRecord(path string) (OwnerRecord, bool) {
	f, err := os.Open(path)
	if err != nil {
		return OwnerRecord{}, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxOwnerRecord+1))
	if err != nil || len(b) > maxOwnerRecord {
		return OwnerRecord{}, false
	}
	var r OwnerRecord
	if json.Unmarshal(b, &r) != nil {
		return OwnerRecord{}, false
	}
	return r, true
}

// OwnerOf is the program running e, from its owner records: a record counts only while
// its process is one of those the agent says have the session open (when the agent says),
// so a reused process id never names an owner.
func OwnerOf(records map[agent.SessionKey][]OwnerRecord, e Entry) *OwnerRecord {
	rs := records[e.Session.Key]
	// A record that leaves the account folder out names the session by agent and id.
	if k := e.Session.Key; k.Profile != "" {
		rs = append(slices.Clone(rs), records[agent.SessionKey{Agent: k.Agent, Session: k.Session}]...)
	}
	if len(rs) == 0 || e.Live.State == agent.Ended {
		return nil
	}
	var pids []int
	if e.Live.PID > 0 {
		pids = append(pids, e.Live.PID)
	}
	for _, p := range e.Live.Procs {
		pids = append(pids, p.PID)
	}
	for _, r := range rs {
		if len(pids) == 0 || slices.Contains(pids, r.PID) {
			return &r
		}
	}
	return nil
}
