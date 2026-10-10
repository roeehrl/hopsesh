package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/internal/localstate"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

type relayExportRecord struct {
	Peer        string       `json:"peer"`
	Endpoint    string       `json:"endpoint"`
	Operation   string       `json:"operation"`
	Package     peer.Package `json:"exportPackage"`
	AckDigest   string       `json:"ackDigest,omitempty"`
	Journal     string       `json:"journal,omitempty"`
	Completed   bool         `json:"completed,omitempty"`
	MarkSkipped bool         `json:"markSkipped,omitempty"`
}

type relayAckReply struct {
	Journal     string `json:"journal"`
	MarkSkipped bool   `json:"markSkipped,omitempty"`
}

type relayPullAck struct {
	Receipt []byte           `json:"receipt"`
	Mark    *agent.Mark      `json:"mark,omitempty"`
	Owed    *lineage.Pending `json:"owed,omitempty"`
}

func readRelayExport(path string) (*relayExportRecord, error) {
	b, err := localstate.ReadPrivateFile(path, relayStateLimit)
	if err != nil {
		return nil, err
	}
	var r relayExportRecord
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Operation == "" || r.Package.Session.Key.Session == "" {
		return nil, errors.New("not a relay export record")
	}
	return &r, nil
}

func saveRelayRecord(path string, value any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := localstate.PrivateDirectory(dir); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(b) > relayStateLimit {
		return errors.New("relay recovery record exceeds quota")
	}
	f, err := os.CreateTemp(dir, ".relay-record-*")
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
	closed := f.Close()
	if err != nil {
		return err
	}
	if closed != nil {
		return closed
	}
	return os.Rename(f.Name(), path)
}

// Export is resolved from the source's own passive inventory. The requester can
// select a key, never supply a filesystem path, native root or substitute metadata.
func (a *App) relayExport(ctx context.Context, grant relay.Grant, op, method string, params json.RawMessage) (any, error) {
	path := a.relayTransferPath("export/"+grant.Peer.ID, op)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err := localstate.PrivateDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	lock, err := localstate.Lock(ctx, path+".lock")
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	r, err := readRelayExport(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if r != nil && (r.Peer != grant.Peer.ID || r.Endpoint != grant.Endpoint || r.Operation != op) {
		return nil, errors.New("export belongs to another peer or operation")
	}
	if method == "export" {
		var req struct {
			Key agent.SessionKey `json:"key"`
		}
		if err = json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		if req.Key.Session == "" {
			return nil, errors.New("an exact native session key is required")
		}
		if r != nil {
			if r.Package.Session.Key != req.Key {
				return nil, errors.New("export operation reused for another session")
			}
			if err = withinRelayRoots(r.Package.Session.CWD, grant.Roots); err != nil {
				return nil, err
			}
			return r.Package, nil
		}
		obs, err := a.ObserveLocal(ctx)
		if err != nil {
			return nil, err
		}
		inv := a.ObservationInventory(ctx, obs)
		defer inv.Close()
		for _, e := range inv.Entries {
			if e.Session.Key != req.Key {
				continue
			}
			if err = withinRelayRoots(e.Session.CWD, grant.Roots); err != nil {
				return nil, err
			}
			pkg, err := a.packageOf(ctx, inv.Local(), e)
			if err != nil {
				return nil, err
			}
			body, err := json.Marshal(pkg)
			if err != nil {
				return nil, err
			}
			if len(body) > relay.MaxObjectBytes {
				return nil, errors.New("session exceeds bounded relay object size")
			}
			r = &relayExportRecord{Peer: grant.Peer.ID, Endpoint: grant.Endpoint, Operation: op, Package: pkg}
			return pkg, saveRelayRecord(path, r)
		}
		return nil, agent.ErrNotFound
	}
	if r == nil {
		return nil, errors.New("export this session before acknowledging it")
	}
	if err = withinRelayRoots(r.Package.Session.CWD, grant.Roots); err != nil {
		return nil, err
	}
	var ack relayPullAck
	if err = json.Unmarshal(params, &ack); err != nil {
		return nil, err
	}
	if err = validateRelayAck(r, grant, ack); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(params)
	intent := hex.EncodeToString(digest[:])
	if r.AckDigest != "" && r.AckDigest != intent {
		return nil, errors.New("source acknowledgment changed under one operation ID")
	}
	var j *journal.Journal
	if r.Journal != "" {
		j, err = journal.Load(a.StateDir, r.Journal)
		if err != nil {
			return nil, err
		}
		if j.Undone || j.TransferID != op {
			return nil, errors.New("source acknowledgment was undone or rebound")
		}
		if r.Completed {
			return relayAckReply{Journal: j.ID, MarkSkipped: r.MarkSkipped}, nil
		}
	} else {
		j, err = journal.New(a.StateDir, journal.KindPush, "Acknowledge session brought to another machine")
		if err != nil {
			return nil, err
		}
		j.TransferID = op
		j.AddKey(r.Package.Session.Key)
		if err = j.Save(); err != nil {
			return nil, err
		}
		r.Journal, r.AckDigest = j.ID, intent
		if err = saveRelayRecord(path, r); err != nil {
			return nil, err
		}
	}
	if len(j.Receipts) == 0 {
		if err = j.WriteReceipt(host.LocalFS(), LocalName(), lineage.PathFor(r.Package.Session.Path), ack.Receipt, false); err != nil {
			return nil, err
		}
	} else if err = j.RecoverReceipts(func(string) (host.FS, error) { return host.LocalFS(), nil }); err != nil {
		return nil, err
	}
	if ack.Owed != nil {
		pending, e := lineage.LoadPending(a.StateDir)
		if e != nil {
			return nil, e
		}
		found := false
		for _, p := range pending {
			if p.Operation == ack.Owed.Operation && p.Key == ack.Owed.Key && p.Replica == ack.Owed.Replica {
				found = true
				break
			}
		}
		if !found {
			err = lineage.AddPending(a.StateDir, *ack.Owed)
		}
		if err != nil {
			return nil, err
		}
	}
	if ack.Mark != nil {
		// Re-observe before marking: a crashed acknowledgment may already have
		// appended the title. Never duplicate that native append on recovery.
		obs, err := a.ObserveLocal(ctx)
		if err != nil {
			return nil, err
		}
		inv := a.ObservationInventory(ctx, obs)
		defer inv.Close()
		found := false
		for _, e := range inv.Entries {
			if e.Session.Key != r.Package.Session.Key {
				continue
			}
			found = true
			if e.Live.State != agent.Ended {
				return nil, errors.New("source is active or its state is unknown; receipt is committed, retry marking when idle")
			}
			if e.Session.Mark != nil && *e.Session.Mark == *ack.Mark {
				break
			}
			// A copy that continued working after export must keep its title.
			// Compare the exact frozen main file, never an observation timestamp.
			var frozen []byte
			for _, f := range r.Package.Files {
				if f.Path == r.Package.Session.Path {
					frozen = f.Data
					break
				}
			}
			current, eRead := localstate.ReadOwnedFile(e.Session.Path, relay.MaxObjectBytes)
			if eRead != nil {
				return nil, eRead
			}
			if len(frozen) == 0 || !bytes.Equal(frozen, current) {
				r.MarkSkipped = true
				break
			}
			mod, ok := a.Module(e.Agent)
			if !ok {
				return nil, agent.ErrUnsupported
			}
			marker, ok := mod.(agent.Marker)
			if !ok {
				break
			}
			in, ok := inv.Local().InstallProfile(e.Agent, e.Session.Key.Profile)
			if !ok {
				return nil, agent.ErrNotInstalled
			}
			h, err := inv.Local().host.For(ctx, mod.Spec(), in, j)
			if err != nil {
				return nil, err
			}
			if err = marker.Mark(ctx, h, in, e.Session, *ack.Mark); err != nil {
				return nil, err
			}
			break
		}
		if !found {
			return nil, errors.New("source copy is no longer present; receipt is committed but its title could not be marked")
		}
	}
	if err = j.Seal(func(string) (host.FS, error) { return host.LocalFS(), nil }); err != nil {
		return nil, err
	}
	r.Completed = true
	if err = saveRelayRecord(path, r); err != nil {
		return nil, err
	}
	return relayAckReply{Journal: j.ID, MarkSkipped: r.MarkSkipped}, nil
}

func validateRelayAck(r *relayExportRecord, grant relay.Grant, ack relayPullAck) error {
	m, err := lineage.Parse(ack.Receipt)
	if err != nil {
		return err
	}
	if err = m.Validate(); err != nil {
		return err
	}
	var hop *lineage.Hop
	for i := range m.Hops {
		if m.Hops[i].ID == r.Operation {
			hop = &m.Hops[i]
			break
		}
	}
	if hop == nil {
		return errors.New("acknowledgment contains no matching transfer")
	}
	from, to := m.Replica(hop.From), m.Replica(hop.To)
	if from.Endpoint != r.Package.Facts.Endpoint || from.Key != r.Package.Session.Key || to.Endpoint != grant.Endpoint || to.Endpoint == "" {
		return errors.New("acknowledgment changed the approved source or destination binding")
	}
	checkMark := func(mark agent.Mark) error {
		if hop.Fork || mark.Location != to.Location || len(mark.AgentName) > 100 || strings.ContainsAny(mark.AgentName, "\x00\r\n") {
			return errors.New("invalid source mark scope")
		}
		switch mark.Kind {
		case agent.MarkPrepared, agent.MarkMoved:
			if mark.Agent != "" && mark.Agent != to.Key.Agent {
				return errors.New("source mark changed destination agent")
			}
		case agent.MarkContinued:
			if mark.Agent != to.Key.Agent {
				return errors.New("continuation mark changed destination agent")
			}
		default:
			return errors.New("unknown source mark kind")
		}
		return nil
	}
	if ack.Mark != nil {
		if err = checkMark(*ack.Mark); err != nil {
			return err
		}
	}
	if ack.Mark != nil && ack.Owed != nil {
		return errors.New("source mark must be immediate or pending, not both")
	}
	if ack.Owed != nil && (hop.Fork || ack.Owed.Operation != r.Operation || ack.Owed.Key != r.Package.Session.Key || ack.Owed.Path != r.Package.Session.Path || ack.Owed.Replica != from.ID || ack.Owed.Branch != from.Line || ack.Owed.Location != from.Location || ack.Owed.Title != r.Package.Session.Title) {
		return errors.New("pending mark widened the exported session")
	}
	if ack.Owed != nil {
		if err = checkMark(ack.Owed.Mark); err != nil {
			return err
		}
		if ack.Owed.Head != string(m.State(hop.Source).Head) {
			return errors.New("pending mark changed the exported conversation head")
		}
	}
	return nil
}

type relayPullRecord struct {
	Peer          string               `json:"peer"`
	Operation     string               `json:"operation"`
	Package       peer.Package         `json:"package"`
	Target        agent.ID             `json:"target"`
	Options       move.Options         `json:"options"`
	Prepared      json.RawMessage      `json:"prepared,omitempty"`
	Writes        []host.SnapshotWrite `json:"writes,omitempty"`
	Ack           *relayPullAck        `json:"ack,omitempty"`
	Result        *move.Result         `json:"result,omitempty"`
	SourceJournal string               `json:"sourceJournal,omitempty"`
}

func (a *App) planRelayPull(ctx context.Context, inv *Inventory, e Entry, target agent.ID, opt move.Options) (*move.Plan, move.Input, error) {
	to := a.Cfg.FindHost(e.Machine)
	if to == nil || to.RelayID == "" {
		return nil, move.Input{}, errors.New("relay source is not configured")
	}
	if opt.Push || opt.StopLocal || opt.RemoteControl || opt.Go {
		return nil, move.Input{}, errors.New("relay pulling cannot push source code, stop programs or start remote control")
	}
	if opt.OperationID == "" {
		var err error
		if opt.OperationID, err = relay.NewOperationID(); err != nil {
			return nil, move.Input{}, err
		}
	}
	path := a.relayTransferPath("pull/"+to.RelayID, opt.OperationID)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, move.Input{}, err
	}
	if err := localstate.PrivateDirectory(filepath.Dir(path)); err != nil {
		return nil, move.Input{}, err
	}
	lock, err := localstate.Lock(ctx, path+".lock")
	if err != nil {
		return nil, move.Input{}, err
	}
	defer lock.Close()
	var r relayPullRecord
	b, err := localstate.ReadPrivateFile(path, relayStateLimit)
	if err == nil {
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, move.Input{}, err
		}
		old, _ := json.Marshal(r.Options)
		current, _ := json.Marshal(opt)
		if r.Peer != to.RelayID || r.Operation != opt.OperationID || r.Package.Session.Key != e.Session.Key || r.Target != target || !bytes.Equal(old, current) {
			return nil, move.Input{}, errors.New("pull operation belongs to another session or options")
		}
	} else if !os.IsNotExist(err) {
		return nil, move.Input{}, err
	} else {
		params, _ := json.Marshal(struct {
			Key agent.SessionKey `json:"key"`
		}{e.Session.Key})
		if err = a.relayCall(ctx, *to, opt.OperationID, "export", params, &r.Package); err != nil {
			return nil, move.Input{}, err
		}
		if r.Package.Session.Key != e.Session.Key || r.Package.Facts.Endpoint == "" {
			return nil, move.Input{}, errors.New("relay source returned another session or no native endpoint")
		}
		r.Peer, r.Operation, r.Target, r.Options = to.RelayID, opt.OperationID, target, opt
		if err = saveRelayRecord(path, &r); err != nil {
			return nil, move.Input{}, err
		}
	}
	pkg := r.Package
	grant, err := (relay.Store{Directory: filepath.Join(a.StateDir, "relay")}).Grant(ctx, to.RelayID)
	if err != nil {
		return nil, move.Input{}, err
	}
	if grant.Kind != "device" || !grant.AllowsSend("export", time.Now()) || pkg.Facts.Endpoint != grant.Peer.Endpoint || pkg.Agent != e.Agent {
		return nil, move.Input{}, errors.New("relay export does not match the independently approved source endpoint")
	}
	snapshot := host.NewSnapshot(to.Name, pkg.Facts, pkg.Files)
	copyInv := *inv
	copyInv.Machines = append([]*Machine(nil), inv.Machines...)
	source := &Machine{Kind: agent.AtMachine, Name: to.Name, Status: StatusOK, OS: pkg.Facts.OS, Destination: "relay", host: snapshot, account: pkg.Account, Agents: []AgentState{{Agent: pkg.Agent, Install: pkg.Install}}}
	for i, m := range copyInv.Machines {
		if m.Name == to.Name {
			copyInv.Machines[i] = source
		}
	}
	e.Session, e.Live, e.Git, e.GitError, e.Lineage = pkg.Session, pkg.Live, pkg.Git, pkg.GitError, pkg.Lineage
	p, input, err := a.Plan(ctx, &copyInv, e, target, opt)
	if err != nil {
		return nil, input, err
	}
	if r.Prepared != nil {
		p, err = move.RestorePrepared(r.Prepared, p)
	} else {
		r.Prepared, err = p.FreezePrepared()
	}
	if err != nil {
		return nil, input, err
	}
	if err = restoreSnapshotWrites(snapshot, r.Writes); err != nil {
		return nil, input, err
	}
	if err = saveRelayRecord(path, &r); err != nil {
		return nil, input, err
	}
	snapshot.PersistWrites(func(writes []host.SnapshotWrite) error { r.Writes = writes; return saveRelayRecord(path, &r) })
	input.AcknowledgeSource = func(ctx context.Context, accepted *move.Plan, result *move.Result) error {
		l, err := localstate.Lock(ctx, path+".lock")
		if err != nil {
			return err
		}
		defer l.Close()
		if result == nil {
			return errors.New("destination produced no durable result")
		}
		if r.Ack == nil {
			ack := &relayPullAck{Owed: result.Owed}
			for _, w := range r.Writes {
				if w.Op == "write" && w.Path == lineage.PathFor(pkg.Session.Path) {
					ack.Receipt = w.Data
				}
			}
			if len(ack.Receipt) == 0 {
				return errors.New("destination committed without a source lineage receipt")
			}
			if accepted.Mark == move.MarkNow && !accepted.Options.Fork {
				mark := agent.Mark{Kind: agent.MarkMoved, Location: accepted.Target.Location}
				if accepted.Kind == move.KindContinue {
					mark.Kind, mark.AgentName = agent.MarkPrepared, accepted.Agent
				}
				ack.Mark = &mark
			}
			r.Ack, r.Result = ack, result
			if err = saveRelayRecord(path, &r); err != nil {
				return err
			}
		}
		j, err := journal.Load(a.StateDir, result.Journal)
		if err != nil {
			return err
		}
		if err = j.QueueAcknowledgment(journal.Acknowledgment{Machine: to.Name, Transport: "relay-pull", Peer: r.Peer, Operation: r.Operation}); err != nil {
			return err
		}
		if err = j.Forget(to.Name); err != nil {
			return err
		}
		if r.SourceJournal == "" {
			params, _ := json.Marshal(r.Ack)
			var reply relayAckReply
			if err = a.relayCall(ctx, *to, opt.OperationID, "ack", params, &reply); err != nil {
				return err
			}
			if reply.Journal == "" {
				return errors.New("source acknowledgment returned no journal")
			}
			r.SourceJournal = reply.Journal
			if reply.MarkSkipped {
				result.Mark = "off"
				result.Warnings = append(result.Warnings, "The source copy changed after export. Its lineage receipt was acknowledged and its title was left unchanged.")
			}
			if err = saveRelayRecord(path, &r); err != nil {
				return err
			}
		}
		if err = j.CompleteAcknowledgment(r.Peer, r.Operation, to.Name, r.SourceJournal); err != nil {
			return fmt.Errorf("record source undo: %w", err)
		}
		return nil
	}
	return p, input, nil
}

func (a *App) recoverRelayPullAcknowledgment(ctx context.Context, j *journal.Journal, ack journal.Acknowledgment) error {
	if ack.Transport != "relay-pull" {
		return errors.New("unsupported remote acknowledgment transport")
	}
	to := a.Cfg.FindHost(ack.Machine)
	if to == nil || !to.Allowed || to.RelayID != ack.Peer {
		return errors.New("source machine was removed, disabled or paired with another endpoint")
	}
	path := a.relayTransferPath("pull/"+ack.Peer, ack.Operation)
	lock, err := localstate.Lock(ctx, path+".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	var r relayPullRecord
	b, err := localstate.ReadPrivateFile(path, relayStateLimit)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return err
	}
	if r.Peer != ack.Peer || r.Operation != ack.Operation || r.Result == nil || r.Result.Journal != j.ID || r.Ack == nil {
		return errors.New("source acknowledgment recovery record does not match its journal")
	}
	if r.SourceJournal == "" {
		body, _ := json.Marshal(r.Ack)
		var reply struct {
			Journal string `json:"journal"`
		}
		if err = a.relayCall(ctx, *to, r.Operation, "ack", body, &reply); err != nil {
			return err
		}
		if reply.Journal == "" {
			return errors.New("source acknowledgment returned no journal")
		}
		r.SourceJournal = reply.Journal
		if err = saveRelayRecord(path, &r); err != nil {
			return err
		}
	}
	if err = j.Forget(to.Name); err != nil {
		return err
	}
	return j.CompleteAcknowledgment(r.Peer, r.Operation, to.Name, r.SourceJournal)
}
