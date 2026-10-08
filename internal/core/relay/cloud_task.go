package relay

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

const cloudTaskSize = 2048

// CloudTask is historical identity, not access authority. A fresh invitation,
// claim, lease and independent peer approval are required for each incarnation.
// Native session IDs are deliberately absent: only an explicit owner selection
// can associate a changed native ID with a previously issued logical task.
type CloudTask struct {
	Schema    int            `json:"schema"`
	ID        string         `json:"id"`
	Provider  string         `json:"provider"`
	Created   int64          `json:"created"`
	Owner     PublicIdentity `json:"owner"`
	Signature []byte         `json:"signature"`
}

func (t CloudTask) signed() []byte {
	t.Signature = nil
	body, _ := json.Marshal(t)
	return append([]byte("hopsesh-cloud-task-v1\x00"), body...)
}

func taskID(id string) bool { return len(id) == 32 && strings.Trim(id, "0123456789abcdef") == "" }

func (t CloudTask) Verify(owner string) error {
	encoded, err := json.Marshal(t)
	if err != nil || len(encoded) > cloudTaskSize {
		return errors.New("cloud task exceeds its metadata limit")
	}
	if t.Schema != 1 || !taskID(t.ID) || !admissionProvider(t.Provider) || t.Created <= 0 || t.Created > time.Now().Add(time.Minute).Unix() || t.Owner.Check() != nil || t.Owner.ID != owner || t.Owner.Endpoint == "" || strings.HasPrefix(t.Owner.Endpoint, "cloud/") || len(t.Signature) != ed25519.SignatureSize || !ed25519.Verify(t.Owner.Signing, t.signed(), t.Signature) {
		return errors.New("cloud task identity or native owner's signature is invalid")
	}
	return nil
}

func newCloudTask(owner Identity, provider string) (CloudTask, error) {
	if owner.Check() != nil || !admissionProvider(provider) {
		return CloudTask{}, errors.New("invalid cloud task issuer or provider")
	}
	id, err := NewOperationID()
	if err != nil {
		return CloudTask{}, err
	}
	t := CloudTask{Schema: 1, ID: id, Provider: provider, Created: time.Now().Unix(), Owner: owner.Public}
	t.Signature = ed25519.Sign(owner.Signing, t.signed())
	return t, t.Verify(owner.Public.ID)
}

type cloudTaskRegistry struct {
	Task       CloudTask `json:"task"`
	Generation int64     `json:"generation"`
	Session    string    `json:"session"`
}

type CloudTaskRecord struct {
	ID               string `json:"id"`
	Provider         string `json:"provider"`
	Created          int64  `json:"created"`
	OwnerFingerprint string `json:"ownerFingerprint"`
	Generation       int64  `json:"generation"`
	Session          string `json:"session"`
}

func (s AdmissionStore) taskPath(id string) (string, error) {
	if !taskID(id) {
		return "", errors.New("invalid logical cloud task ID")
	}
	return filepath.Join(s.Directory, "tasks", id+".json"), nil
}

func (s AdmissionStore) readTask(id string) (cloudTaskRegistry, error) {
	var task cloudTaskRegistry
	p, err := s.taskPath(id)
	if err != nil {
		return task, err
	}
	if err = localstate.PrivateDirectory(filepath.Dir(p)); err != nil {
		return task, err
	}
	body, err := localstate.ReadPrivateFile(p, cloudTaskSize+256)
	if err != nil {
		return task, err
	}
	if json.Unmarshal(body, &task) != nil || task.Task.ID != id || task.Task.Verify(task.Task.Owner.ID) != nil || task.Generation < 1 || task.Generation >= 1<<53 || !admissionName.MatchString(task.Session) {
		return cloudTaskRegistry{}, errors.New("invalid saved cloud task")
	}
	return task, nil
}

// Tasks contains public historical identifiers only. Expiring an invitation
// never erases a task's lineage identity or silently combines independent tasks.
func (s AdmissionStore) Tasks() ([]CloudTaskRecord, error) {
	rows := []CloudTaskRecord{}
	dir := filepath.Join(s.Directory, "tasks")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return rows, nil
	}
	if err != nil {
		return nil, err
	}
	if err = localstate.PrivateDirectory(dir); err != nil {
		return nil, err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".task-") {
			continue
		}
		if len(rows) >= 10000 {
			return nil, errors.New("cloud task history exceeds its local limit")
		}
		if !strings.HasSuffix(e.Name(), ".json") {
			return nil, errors.New("unexpected cloud task history entry")
		}
		task, err := s.readTask(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		rows = append(rows, CloudTaskRecord{task.Task.ID, task.Task.Provider, task.Task.Created, task.Task.Owner.ID, task.Generation, task.Session})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Created == rows[j].Created {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].Created > rows[j].Created
	})
	return rows, nil
}

// selectTask is called under the admission writer lock before network activity.
func (s AdmissionStore) selectTask(owner Identity, provider, resume string) (CloudTask, int64, error) {
	if resume != "" {
		record, err := s.readTask(resume)
		if err == nil && (record.Task.Verify(owner.Public.ID) != nil || record.Task.Provider != provider || record.Generation >= (1<<53)-1) {
			err = errors.New("resumed cloud task belongs to another issuer/provider or exhausted its generations")
		}
		return record.Task, record.Generation + 1, err
	}
	rows, err := s.Tasks()
	if err != nil {
		return CloudTask{}, 0, err
	}
	if len(rows) >= 10000 {
		return CloudTask{}, 0, errors.New("cloud task history is full; existing tasks can still resume")
	}
	task, err := newCloudTask(owner, provider)
	return task, 1, err
}

func (s AdmissionStore) saveTask(task CloudTask, generation int64, session string) error {
	p, err := s.taskPath(task.ID)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err = localstate.PrivateDirectory(dir); err != nil {
		return err
	}
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
		err = json.NewEncoder(f).Encode(cloudTaskRegistry{task, generation, session})
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

// ResolveTask establishes that the independently approved fresh key actually
// claimed this owner's particular invitation. A connector-supplied task label,
// signed historical task alone or matching native session ID is insufficient.
func (s AdmissionStore) ResolveTask(ctx context.Context, c Connection, id string, task CloudTask, peer PublicIdentity, provider, session string, generation int64) error {
	p, err := s.path(id)
	if err != nil {
		return err
	}
	if err = localstate.PrivateDirectory(s.Directory); err != nil {
		return err
	}
	lock, err := localstate.Lock(ctx, filepath.Join(s.Directory, "admissions.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	body, err := localstate.ReadPrivateFile(p, 8192)
	if err != nil {
		return err
	}
	var ticket AdmissionTicket
	if json.Unmarshal(body, &ticket) != nil || ticket.Verify(c.Device) != nil || ticket.ID != id || ticket.Generation != generation || ticket.Task.ID != task.ID || !bytes.Equal(ticket.Task.Signature, task.Signature) || task.Verify(c.Device) != nil || ticket.Provider != provider || ticket.Session != session || task.Provider != provider {
		return errors.New("checkpoint has no matching owner-issued cloud task invitation")
	}
	current, err := s.readTask(task.ID)
	if err != nil {
		return err
	}
	if current.Generation != ticket.Generation || !bytes.Equal(current.Task.Signature, task.Signature) {
		return errors.New("cloud task invitation was superseded by a newer owner-issued generation")
	}
	status, err := CheckAdmission(ctx, c, ticket)
	if err != nil {
		return err
	}
	if status.Status != "claimed" || status.Public == nil || status.Public.Fingerprint() != peer.Fingerprint() {
		return errors.New("cloud task invitation was not claimed by this approved incarnation")
	}
	return nil
}
