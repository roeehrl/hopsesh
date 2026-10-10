package relay

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

// AdmissionRecord is safe for UI and diagnostics. It deliberately omits the
// one-use secret and signature; routing admission never implies peer approval.
type AdmissionRecord struct {
	ID               string `json:"id"`
	TaskID           string `json:"taskId"`
	Generation       int64  `json:"generation"`
	Path             string `json:"path"`
	Provider         string `json:"provider"`
	Session          string `json:"session"`
	Origin           string `json:"origin"`
	OwnerFingerprint string `json:"ownerFingerprint"`
	Expires          int64  `json:"expires"`
	LeaseSeconds     int    `json:"leaseSeconds"`
	Revoked          bool   `json:"revoked"`
}

type AdmissionStore struct{ Directory string }

func (s AdmissionStore) Check(ctx context.Context, c Connection, id string) (AdmissionStatus, error) {
	path, err := s.path(id)
	if err != nil {
		return AdmissionStatus{}, err
	}
	if err = localstate.PrivateDirectory(s.Directory); err != nil {
		return AdmissionStatus{}, err
	}
	body, err := localstate.ReadPrivateFile(path, 8192)
	if err != nil {
		return AdmissionStatus{}, err
	}
	var ticket AdmissionTicket
	if json.Unmarshal(body, &ticket) != nil {
		return AdmissionStatus{}, errors.New("invalid saved cloud invitation")
	}
	state, err := CheckAdmission(ctx, c, ticket)
	if err != nil {
		return state, err
	}
	current, err := s.readTask(ticket.Task.ID)
	if err != nil {
		return AdmissionStatus{}, err
	}
	if current.Generation < ticket.Generation || current.Task.Verify(c.Device) != nil {
		return AdmissionStatus{}, errors.New("cloud task generation history is invalid")
	}
	state.Superseded = current.Generation > ticket.Generation
	return state, nil
}

func (s AdmissionStore) path(id string) (string, error) {
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		return "", errors.New("invalid local admission ID")
	}
	return filepath.Join(s.Directory, id+".json"), nil
}

func (s AdmissionStore) List() ([]AdmissionRecord, error) {
	rows := []AdmissionRecord{}
	entries, err := os.ReadDir(s.Directory)
	if os.IsNotExist(err) {
		return rows, nil
	}
	if err != nil {
		return nil, err
	}
	if err = localstate.PrivateDirectory(s.Directory); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		path, err := s.path(id)
		if err != nil {
			return nil, err
		}
		body, err := localstate.ReadPrivateFile(path, 8192)
		if err != nil {
			return nil, err
		}
		var ticket AdmissionTicket
		if json.Unmarshal(body, &ticket) != nil || ticket.ID != id || ticket.Verify(ticket.Owner.ID) != nil {
			return nil, errors.New("saved cloud invitation is invalid")
		}
		row := AdmissionRecord{ID: id, TaskID: ticket.Task.ID, Generation: ticket.Generation, Path: path, Provider: ticket.Provider, Session: ticket.Session, Origin: ticket.Origin, OwnerFingerprint: ticket.Owner.ID, Expires: ticket.Expires, LeaseSeconds: ticket.LeaseSeconds}
		if b, err := localstate.ReadPrivateFile(path+".revoked", 16); err == nil {
			if string(b) != "revoked\n" {
				return nil, errors.New("invalid cloud revocation receipt")
			}
			row.Revoked = true
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		rows = append(rows, row)
		if len(rows) > 128 {
			return nil, errors.New("cloud invitation history exceeds its local limit")
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Expires > rows[j].Expires })
	return rows, nil
}

// IssueTask preserves logical identity only after explicit selection of an owner-issued task.
func (s AdmissionStore) IssueTask(ctx context.Context, owner Identity, c Connection, provider, session string, lease time.Duration, resume string) (AdmissionRecord, error) {
	if err := validateAdmissionRequest(owner, c, provider, session, lease); err != nil {
		return AdmissionRecord{}, err
	}
	if err := os.MkdirAll(s.Directory, 0700); err != nil {
		return AdmissionRecord{}, err
	}
	if err := localstate.PrivateDirectory(s.Directory); err != nil {
		return AdmissionRecord{}, err
	}
	lock, err := localstate.Lock(ctx, filepath.Join(s.Directory, "admissions.lock"))
	if err != nil {
		return AdmissionRecord{}, err
	}
	defer lock.Close()
	task, generation, err := s.selectTask(owner, provider, resume)
	if err != nil {
		return AdmissionRecord{}, err
	}
	rows, err := s.List()
	if err != nil {
		return AdmissionRecord{}, err
	}
	retained := 0
	for _, row := range rows {
		// Even a late claim cannot outlive this boundary. Keep revocation files
		// until then; deleting an expired invitation earlier can strand a lease.
		if row.Expires+int64(row.LeaseSeconds) <= time.Now().Unix() {
			if err = os.Remove(row.Path); err != nil {
				return AdmissionRecord{}, err
			}
			if err = os.Remove(row.Path + ".revoked"); err != nil && !os.IsNotExist(err) {
				return AdmissionRecord{}, err
			}
		} else {
			retained++
		}
	}
	if retained >= 128 {
		return AdmissionRecord{}, errors.New("cloud invitation history is full; wait for retained leases to expire")
	}
	id, err := NewOperationID()
	if err != nil {
		return AdmissionRecord{}, err
	}
	path, err := s.path(id)
	if err != nil {
		return AdmissionRecord{}, err
	}
	staged := filepath.Join(s.Directory, ".issuing-"+id)
	file, err := os.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return AdmissionRecord{}, err
	}
	good := false
	defer func() {
		_ = file.Close()
		if !good {
			_ = os.Remove(staged)
		}
	}()
	if err = localstate.PrivateFile(staged); err != nil {
		return AdmissionRecord{}, err
	}
	// A generation is reserved durably before issuing any authority. Failed
	// issuance cannot reuse an old generation or restore its superseded access.
	if err = s.saveTask(task, generation, session); err != nil {
		return AdmissionRecord{}, err
	}
	ticket, err := issueAdmission(ctx, owner, c, provider, session, lease, id, task, generation)
	if err != nil {
		return AdmissionRecord{}, err
	}
	if err = json.NewEncoder(file).Encode(ticket); err == nil {
		err = file.Sync()
	}
	if err != nil {
		return AdmissionRecord{}, errors.New("invitation issued but its private file was not durable; no cloud peer was approved")
	}
	if err = file.Close(); err != nil {
		return AdmissionRecord{}, err
	}
	if err = os.Rename(staged, path); err != nil {
		return AdmissionRecord{}, errors.New("invitation issued but its private file could not be committed; no peer was approved")
	}
	good = true
	return AdmissionRecord{ID: id, TaskID: task.ID, Generation: generation, Path: path, Provider: provider, Session: session, Origin: ticket.Origin, OwnerFingerprint: owner.Public.ID, Expires: ticket.Expires, LeaseSeconds: ticket.LeaseSeconds}, nil
}

func (s AdmissionStore) Revoke(ctx context.Context, c Connection, id string) error {
	path, err := s.path(id)
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
	body, err := localstate.ReadPrivateFile(path, 8192)
	if err != nil {
		return err
	}
	var ticket AdmissionTicket
	if json.Unmarshal(body, &ticket) != nil {
		return errors.New("invalid saved cloud invitation")
	}
	if err = RevokeAdmission(ctx, c, ticket); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Directory, ".revocation-*")
	if err != nil {
		return errors.New("server revoked cloud delivery; local receipt could not be saved")
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err == nil {
		err = localstate.PrivateFile(f.Name())
	}
	if err == nil {
		_, err = f.WriteString("revoked\n")
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	if err == nil {
		err = os.Rename(f.Name(), path+".revoked")
	}
	if err != nil {
		return errors.New("server revoked cloud delivery; local receipt could not be saved; refresh or retry")
	}
	return nil
}
