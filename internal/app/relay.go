package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func (a *App) relayCall(ctx context.Context, to config.Host, op, method string, params json.RawMessage, out any) error {
	if !to.Allowed || !a.Cfg.Relay.Enabled {
		return errors.New("relay machine access is disabled")
	}
	n, err := localruntime.NewNamespace(config.Dir(), a.StateDir)
	if err != nil {
		return err
	}
	return (localruntime.Client{Namespace: n}).Call(ctx, "relay.call", struct {
		Peer      string          `json:"peer"`
		Operation string          `json:"operation"`
		Method    string          `json:"method"`
		Params    json.RawMessage `json:"params"`
	}{to.RelayID, op, method, params}, out)
}

func (a *App) scanRelay(ctx context.Context, to config.Host) (*Machine, []Entry) {
	m, entries, _ := a.scanRelaySnapshot(ctx, to)
	return m, entries
}

func (a *App) scanRelaySnapshot(ctx context.Context, to config.Host) (*Machine, []Entry, observe.Snapshot) {
	var snapshot observe.Snapshot
	m := &Machine{Kind: agent.AtMachine, Name: to.Name, Destination: "relay", Status: StatusError}
	op, err := relay.NewOperationID()
	if err != nil {
		m.Error = err.Error()
		return m, nil, snapshot
	}
	if err = a.relayCall(ctx, to, op, "observe", nil, &snapshot); err != nil {
		m.Error = err.Error()
		return m, nil, snapshot
	}
	if !snapshot.Fresh(time.Now()) {
		m.Error = "Remote observations are stale or unavailable; reconnect and refresh."
		return m, nil, snapshot
	}
	var obs Observation
	if err = json.Unmarshal(snapshot.Data, &obs); err != nil {
		m.Error = err.Error()
		return m, nil, snapshot
	}
	m.OS, m.Agents = obs.OS, obs.Agents
	m.Hopsesh = obs.Version
	receive := false
	grant, e := (relay.Store{Directory: filepath.Join(a.StateDir, "relay")}).Grant(ctx, to.RelayID)
	if e == nil {
		receive = obs.Receive && grant.AllowsSend("plan", time.Now()) && grant.AllowsSend("apply", time.Now())
	}
	m.Receive = &receive
	if !obs.InventoryComplete {
		m.Error = "Remote observation is incomplete"
	} else {
		m.Status = StatusOK
	}
	for i := range obs.Entries {
		e := &obs.Entries[i]
		e.Machine, e.Location = to.Name, agent.MachineLocation(to.Name)
	}
	return m, obs.Entries, snapshot
}

// RelayReceiver reuses the existing plan/apply machinery. Peer approval is
// independent of mailbox login; a scoped cloud connector cannot become a receiver.
func (a *App) RelayReceiver(snapshots ...func() observe.Snapshot) relay.Handler {
	type session struct {
		peer *peerSession
		used time.Time
	}
	var mu sync.Mutex
	sessions := map[string]*session{}
	return func(ctx context.Context, grant relay.Grant, operation, method string, params json.RawMessage) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		from := grant.Peer.ID
		var err error
		if !grant.Allows(method, time.Now()) {
			return nil, relay.ErrRevoked
		}

		if grant.Kind != "device" {
			return nil, errors.New("cloud connector has no machine receive permission")
		}
		cfg, loadErr := config.Load()
		if loadErr != nil {
			return nil, loadErr
		}
		source := *a
		source.Cfg = cfg
		if !cfg.Relay.Enabled {
			return nil, errors.New("relay receiving is disabled")
		}
		if method == "observe" {
			if len(snapshots) == 0 {
				return nil, errors.New("shared observation source is unavailable")
			}
			snap := snapshots[0]()
			var obs Observation
			if err := json.Unmarshal(snap.Data, &obs); err != nil {
				return nil, err
			}
			obs = relayObservation(obs, grant)
			obs.Receive = cfg.Peer.Receive && grant.Allows("plan", time.Now()) && grant.Allows("apply", time.Now()) && len(grant.Roots) > 0
			snap.Data, err = json.Marshal(obs)
			return snap, err
		}
		if method == "export" || method == "ack" {
			return source.relayExport(ctx, grant, operation, method, params)
		}
		if method == "preview" {
			return source.relayPreview(ctx, grant, params)
		}
		if !cfg.Peer.Receive && method != peer.MethodHello && method != peer.MethodUndo {
			return nil, peer.Refused(LocalName())
		}
		for key, s := range sessions {
			if time.Since(s.used) > 15*time.Minute {
				if s.peer.snap != nil {
					s.peer.snap.Close()
				}
				delete(sessions, key)
			}
		}
		key := from + ":" + operation
		s := sessions[key]
		if s == nil {
			if len(sessions) >= 32 {
				return nil, errors.New("relay plan quota reached")
			}
			s = &session{peer: &peerSession{a: &source}}
			sessions[key] = s
		}
		s.peer.a = &source
		s.used = time.Now()
		restore := func() (*relayTransfer, error) {
			r, err := source.loadRelayTransfer(from, operation)
			if err != nil {
				return nil, err
			}
			if r.Endpoint != grant.Endpoint {
				return nil, errors.New("relay transfer's endpoint binding changed")
			}
			if r.Reply != nil {
				return r, nil
			}
			if _, err = s.peer.planReceive(ctx, r.Request); err != nil {
				return nil, err
			}
			s.peer.plan, err = move.RestorePrepared(r.Prepared, s.peer.plan)
			if err != nil {
				return nil, err
			}
			if err = restoreSnapshotWrites(s.peer.snap, r.Writes); err != nil {
				return nil, err
			}
			s.peer.snap.PersistWrites(func(writes []host.SnapshotWrite) error { r.Writes = writes; return source.saveRelayTransfer(r) })
			return r, nil
		}
		switch method {
		case peer.MethodHello:
			var hi peer.Hello
			if err = json.Unmarshal(params, &hi); err != nil {
				return nil, err
			}
			if hi.Protocol != peer.Protocol {
				return nil, peer.ErrProtocol
			}
			return s.peer.handle(ctx, method, params)
		case peer.MethodPlan:
			var req peer.PlanRequest
			if err = json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
			if grant.Endpoint == "" || req.Package.Facts.Endpoint != grant.Endpoint {
				return nil, errors.New("source native endpoint does not match approved relay peer")
			}
			if req.Options.OperationID != "" && req.Options.OperationID != operation {
				return nil, errors.New("transfer operation does not match encrypted request")
			}
			if req.Options.Go || req.Options.StopLocal || req.Options.RemoteControl {
				return nil, errors.New("relay receiving cannot launch or terminate programs")
			}
			req.Options.OperationID = operation
			if old, e := source.loadRelayTransfer(from, operation); e == nil {
				left, _ := json.Marshal(old.Request)
				right, _ := json.Marshal(req)
				if string(left) != string(right) {
					return nil, errors.New("relay operation reused for a different accepted plan")
				}
				var accepted struct {
					Plan *move.Plan `json:"plan"`
				}
				if err = json.Unmarshal(old.Prepared, &accepted); err != nil {
					return nil, err
				}
				return &peer.PlanReply{Plan: accepted.Plan}, nil
			} else if !os.IsNotExist(e) {
				return nil, e
			}
			if req.Options.TargetDir != "" {
				if err = withinRelayRoots(req.Options.TargetDir, grant.Roots); err != nil {
					return nil, err
				}
			}
			reply, err := s.peer.planReceive(ctx, req)
			if err != nil {
				return nil, err
			}
			if err = withinRelayRoots(reply.Plan.Target.CWD, grant.Roots); err != nil {
				s.peer.plan = nil
				return nil, err
			}
			if reply.Plan.StopHere {
				s.peer.plan = nil
				return nil, errors.New("relay cannot stop an active destination session; stop it on that machine first")
			}
			prepared, err := reply.Plan.FreezePrepared()
			if err != nil {
				return nil, err
			}
			r := &relayTransfer{Peer: from, Endpoint: grant.Endpoint, Operation: operation, Request: req, Prepared: prepared}
			if err = source.saveRelayTransfer(r); err != nil {
				s.peer.plan = nil
				return nil, err
			}
			s.peer.snap.PersistWrites(func(writes []host.SnapshotWrite) error { r.Writes = writes; return source.saveRelayTransfer(r) })
			return reply, nil
		case peer.MethodApply:
			r, err := restore()
			if err != nil {
				return nil, err
			}
			if r.Reply != nil {
				return r.Reply, nil
			}
			if err = withinRelayRoots(s.peer.plan.Target.CWD, grant.Roots); err != nil {
				return nil, err
			}
			reply, err := s.peer.applyReceive(ctx)
			if err != nil {
				return nil, err
			}
			r.Reply = reply
			if err = source.saveRelayTransfer(r); err != nil {
				return nil, err
			}
			return reply, nil
		case peer.MethodUndo:
			var req peer.UndoRequest
			if err = json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
			paths, err := filepath.Glob(filepath.Join(source.StateDir, "relay-transfers", "*.json"))
			if err != nil {
				return nil, err
			}
			for _, path := range paths {
				if export, e := readRelayExport(path); e == nil && export.Peer == from && export.Endpoint == grant.Endpoint && export.Journal == req.Journal && export.Journal != "" {
					if e = withinRelayRoots(export.Package.Session.CWD, grant.Roots); e != nil {
						return nil, e
					}
					j, e := journal.Load(source.StateDir, req.Journal)
					if e != nil {
						return nil, e
					}
					if j.TransferID != export.Operation {
						return nil, errors.New("source journal belongs to another transfer")
					}
					if j.Undone {
						return struct{}{}, nil
					}
					_, e = source.Undo(ctx, req.Journal, req.Force)
					return struct{}{}, e
				}
				r, e := readRelayTransfer(path)
				if e != nil || r.Peer != from || r.Endpoint != grant.Endpoint || r.Reply == nil || r.Reply.Journal != req.Journal {
					continue
				}
				j, e := journal.Load(source.StateDir, req.Journal)
				if e != nil {
					return nil, e
				}
				if j.TransferID != r.Operation {
					return nil, errors.New("relay journal does not belong to the authenticated transfer")
				}
				if j.Undone {
					return struct{}{}, nil
				}
				var accepted struct {
					Plan *move.Plan `json:"plan"`
				}
				if err = json.Unmarshal(r.Prepared, &accepted); err != nil {
					return nil, err
				}
				if accepted.Plan == nil {
					return nil, errors.New("invalid relay receipt")
				}
				if err = withinRelayRoots(accepted.Plan.Target.CWD, grant.Roots); err != nil {
					return nil, err
				}
				_, err = source.Undo(ctx, req.Journal, req.Force)
				return struct{}{}, err
			}
			return nil, errors.New("journal is not owned by this authenticated relay peer")
		default:
			return nil, fmt.Errorf("unsupported relay method %q", method)
		}
	}
}

func canonicalRelayPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("relay target path must be absolute")
	}
	path = filepath.Clean(path)
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		missing = append(missing, filepath.Base(path))
		path = parent
	}
}
func withinRelayRoots(path string, roots []string) error {
	actual, err := canonicalRelayPath(path)
	if err != nil {
		return err
	}
	for _, root := range roots {
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil || !filepath.IsAbs(resolved) {
			continue
		}
		rel, err := filepath.Rel(resolved, actual)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
		}
	}
	return errors.New("relay target is outside the locally approved repository roots")
}
