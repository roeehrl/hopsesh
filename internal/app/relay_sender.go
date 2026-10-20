package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

// The source request is frozen before delivery. Retrying after its native copy
// was marked must not rebuild a different request under the same operation ID.
type relayOutgoing struct {
	Peer      string           `json:"peer"`
	Operation string           `json:"operation"`
	Request   peer.PlanRequest `json:"request"`
	Phase     string           `json:"phase"`
	Journal   string           `json:"journal,omitempty"`
	Result    *PushResult      `json:"result,omitempty"`
}

func (p *Push) outgoingPath() string {
	return p.a.relayTransferPath("outgoing/"+p.to.RelayID, p.operation)
}
func readRelayOutgoing(path string) (*relayOutgoing, error) {
	b, err := localstate.ReadPrivateFile(path, relayStateLimit)
	if err != nil {
		return nil, err
	}
	var r relayOutgoing
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
func saveRelayOutgoing(path string, r *relayOutgoing) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(b) > relayStateLimit {
		return errors.New("relay outgoing recovery record exceeds quota")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".outgoing-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func (p *Push) outgoingLock(ctx context.Context) (*os.File, error) {
	dir := filepath.Dir(p.outgoingPath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := localstate.PrivateDirectory(dir); err != nil {
		return nil, err
	}
	return localstate.Lock(ctx, p.outgoingPath()+".lock")
}
func (p *Push) frozenRequest(ctx context.Context, request peer.PlanRequest) (peer.PlanRequest, error) {
	lock, err := p.outgoingLock(ctx)
	if err != nil {
		return request, err
	}
	defer lock.Close()
	r, err := readRelayOutgoing(p.outgoingPath())
	if os.IsNotExist(err) {
		r = &relayOutgoing{Peer: p.to.RelayID, Operation: p.operation, Request: request, Phase: "planned"}
		return request, saveRelayOutgoing(p.outgoingPath(), r)
	}
	if err != nil {
		return request, err
	}
	oldOptions, _ := json.Marshal(r.Request.Options)
	newOptions, _ := json.Marshal(request.Options)
	if r.Peer != p.to.RelayID || r.Operation != p.operation || r.Request.Target != request.Target || r.Request.Package.Session.Key != request.Package.Session.Key || r.Request.Package.Session.Path != request.Package.Session.Path || r.Request.Package.Facts.Endpoint != request.Package.Facts.Endpoint || !bytes.Equal(oldOptions, newOptions) {
		return request, errors.New("relay operation ID belongs to another source, destination or plan options")
	}
	return r.Request, nil
}
func (p *Push) beginSourceCommit(ctx context.Context) (*relayOutgoing, *os.File, error) {
	lock, err := p.outgoingLock(ctx)
	if err != nil {
		return nil, nil, err
	}
	r, err := readRelayOutgoing(p.outgoingPath())
	if err == nil && (r.Peer != p.to.RelayID || r.Operation != p.operation) {
		err = errors.New("relay outgoing commit belongs to another operation")
	}
	if err == nil && r.Phase == "source-started" {
		err = fmt.Errorf("%w; source journal %s", relay.ErrUncertain, r.Journal)
	}
	if err == nil && r.Phase == "completed" {
		j, e := journal.Load(p.a.StateDir, r.Journal)
		if e != nil {
			err = e
		} else if j.Undone {
			err = errors.New("relay operation was already undone; use a new operation ID for a new transfer")
		} else if j.TransferID != p.operation {
			err = errors.New("relay source journal belongs to another operation")
		}
	}
	if err != nil {
		lock.Close()
		return nil, nil, err
	}
	return r, lock, nil
}
