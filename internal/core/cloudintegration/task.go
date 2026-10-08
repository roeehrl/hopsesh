package cloudintegration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

type taskAssociation struct {
	Task        relay.CloudTask `json:"task"`
	Admission   string          `json:"admission"`
	Incarnation string          `json:"incarnation"`
	Session     string          `json:"session"`
	Generation  int64           `json:"generation"`
}

// AssociateTask records public provenance after a successful routing claim. It
// grants no permission. The issuer verifies the actual claim again before using
// this identity for lineage, independently of the connector's assertion.
func (s Incarnation) AssociateTask(ctx context.Context, ticket relay.AdmissionTicket) error {
	if ticket.Verify(ticket.Owner.ID) != nil || ticket.Provider != s.Provider || ticket.Session != s.Session {
		return errors.New("cloud task invitation does not match this incarnation")
	}
	if _, err := LoadForClaim(ctx, s.Directory); err != nil {
		return err
	}
	lock, err := localstate.Lock(ctx, filepath.Join(filepath.Dir(s.Directory), "tasks.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	a := taskAssociation{ticket.Task, ticket.ID, s.ID, s.Session, ticket.Generation}
	if err = s.checkTaskPointer(a, true); err != nil {
		return err
	}
	body, err := json.Marshal(a)
	if err != nil {
		return err
	}
	p := filepath.Join(s.Directory, "task.json")
	old, err := localstate.ReadPrivateFile(p, 8192)
	if err == nil {
		if string(old) != string(body) {
			return errors.New("this incarnation already belongs to another task invitation")
		}
	} else {
		if !os.IsNotExist(err) {
			return err
		}
		if err = writeTaskMetadata(s.Directory, p, body); err != nil {
			return err
		}
	}
	pointer, err := json.Marshal(taskPointer{a.Incarnation, a.Generation})
	if err != nil {
		return err
	}
	return writeTaskMetadata(filepath.Dir(s.Directory), s.taskActivePath(a.Task), pointer)
}

func (s Incarnation) task(owner string) (*relay.CloudTask, string, int64, error) {
	b, err := localstate.ReadPrivateFile(filepath.Join(s.Directory, "task.json"), 8192)
	if os.IsNotExist(err) {
		return nil, "", 0, nil
	}
	if err != nil {
		return nil, "", 0, err
	}
	var a taskAssociation
	if json.Unmarshal(b, &a) != nil || a.Incarnation != s.ID || a.Session != s.Session || a.Task.Provider != s.Provider || a.Task.Verify(owner) != nil || a.Generation < 1 || a.Generation >= 1<<53 || len(a.Admission) != 32 || strings.Trim(a.Admission, "0123456789abcdef") != "" {
		return nil, "", 0, errors.New("cloud task association is invalid for this incarnation or issuer")
	}
	return &a.Task, a.Admission, a.Generation, nil
}

type taskPointer struct {
	Incarnation string `json:"incarnation"`
	Generation  int64  `json:"generation"`
}

func (s Incarnation) taskActivePath(task relay.CloudTask) string {
	return filepath.Join(filepath.Dir(s.Directory), "active-task-"+task.ID)
}

func (s Incarnation) checkTaskPointer(a taskAssociation, forClaim bool) error {
	b, err := localstate.ReadPrivateFile(s.taskActivePath(a.Task), 256)
	if os.IsNotExist(err) && forClaim {
		return nil
	}
	if err != nil {
		return err
	}
	var current taskPointer
	if json.Unmarshal(b, &current) != nil || current.Generation < 1 || current.Generation >= 1<<53 || len(current.Incarnation) != 32 || strings.Trim(current.Incarnation, "0123456789abcdef") != "" {
		return errors.New("invalid cloud task incarnation pointer")
	}
	if current.Generation == a.Generation && current.Incarnation == s.ID {
		return nil
	}
	if forClaim && current.Generation < a.Generation {
		return nil
	}
	return errors.New("cloud task connector was superseded by a newer incarnation")
}

func (s Incarnation) taskCurrent(forClaim bool) error {
	b, err := localstate.ReadPrivateFile(filepath.Join(s.Directory, "task.json"), 8192)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var a taskAssociation
	if json.Unmarshal(b, &a) != nil || a.Task.Verify(a.Task.Owner.ID) != nil || a.Incarnation != s.ID || a.Session != s.Session || a.Task.Provider != s.Provider || a.Generation < 1 || a.Generation >= 1<<53 {
		return errors.New("invalid cloud task incarnation association")
	}
	return s.checkTaskPointer(a, forClaim)
}

func writeTaskMetadata(dir, p string, body []byte) error {
	f, err := os.CreateTemp(dir, ".task-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err == nil {
		err = localstate.PrivateFile(f.Name())
	}
	if err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	if err == nil {
		err = os.Rename(f.Name(), p)
	}
	return err
}
