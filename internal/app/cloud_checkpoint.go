package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/internal/localstate"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

type cloudCheckpointRecord struct {
	HandoffProof string                  `json:"handoffProof,omitempty"`
	Peer         string                  `json:"peer"`
	Export       cloudintegration.Export `json:"export"`
	Prepared     json.RawMessage         `json:"prepared,omitempty"`
	Target       agent.ID                `json:"target"`
	Options      move.Options            `json:"options"`
}

// PlanCloudCheckpoint imports a sealed conversation into an explicitly selected
// local directory. The source is a read-only incarnation, not a remote machine.
// Its receipt belongs to the local private ledger; vendor files remain untouched.
func (a *App) PlanCloudCheckpoint(ctx context.Context, inv *Inventory, id string, target agent.ID, opt move.Options) (*move.Plan, move.Input, cloudintegration.Export, error) {
	var empty cloudintegration.Export
	fail := func(err error) (*move.Plan, move.Input, cloudintegration.Export, error) {
		return nil, move.Input{}, empty, err
	}
	if opt.TargetDir == "" || opt.Push || opt.SyncCode || opt.Clone || opt.StopLocal || opt.RemoteControl || opt.Go || opt.Native || opt.Via == move.ViaImport || opt.TargetSession != "" || opt.CarryRules || len(opt.RuleFiles) > 0 {
		return fail(errors.New("cloud checkpoint import requires a local directory and portable conversation; code, native restore, source instructions and process control are unavailable"))
	}
	dir, err := filepath.Abs(opt.TargetDir)
	if err != nil {
		return fail(err)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return fail(err)
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return fail(errors.New("choose an existing local working directory"))
	}
	opt.TargetDir, opt.NewReplica, opt.Mark, opt.Notify = dir, true, false, false
	if opt.OperationID == "" {
		opt.OperationID, err = relay.NewOperationID()
		if err != nil {
			return fail(err)
		}
	}
	if !checkpointOperationValid(opt.OperationID) {
		return fail(errors.New("invalid checkpoint operation ID"))
	}
	grant, err := a.checkCloudCheckpointAccess(ctx, id)
	if err != nil {
		return fail(err)
	}
	root := filepath.Join(a.StateDir, "cloud-checkpoints")
	if err = os.MkdirAll(root, 0700); err != nil {
		return fail(err)
	}
	if err = localstate.PrivateDirectory(root); err != nil {
		return fail(err)
	}
	lock, err := localstate.Lock(ctx, filepath.Join(root, ".admission.lock"))
	if err != nil {
		return fail(err)
	}
	defer lock.Close()
	if err = checkpointRetired(root, opt.OperationID); err != nil {
		return fail(err)
	}
	recordPath := filepath.Join(root, opt.OperationID+".json")
	var record cloudCheckpointRecord
	b, err := localstate.ReadPrivateFile(recordPath, relayStateLimit)
	if err == nil {
		if json.Unmarshal(b, &record) != nil || record.Peer != id {
			return fail(errors.New("checkpoint operation belongs to another source"))
		}
		// A frozen checkpoint remains a snapshot. Revalidate its byte structure at
		// the original observation time, while current grants are checked separately.
		if err = record.Export.Check(grant.Peer, record.Export.ObservedAt); err != nil {
			return fail(err)
		}
	} else if !os.IsNotExist(err) {
		return fail(err)
	} else {
		entries, err := os.ReadDir(root)
		if err != nil {
			return fail(err)
		}
		count := 0
		var bytes int64
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				info, err := e.Info()
				if err != nil || !info.Mode().IsRegular() {
					return fail(errors.New("invalid checkpoint ledger"))
				}
				count++
				bytes += info.Size()
			}
		}
		if count >= 32 || bytes > 256<<20-relayStateLimit {
			return fail(errors.New("private cloud checkpoint retention limit reached; retained operations require explicit cleanup"))
		}
		if _, err = a.cloudConnectorCall(ctx, id, "export", &record.Export); err != nil {
			return fail(err)
		}
		if err = record.Export.Check(grant.Peer, time.Now()); err != nil {
			return fail(err)
		}
		record.Peer = id
	}
	if !record.Export.LeaseExpires.After(time.Now()) {
		return fail(errors.New("cloud incarnation lease expired; approve a fresh connector"))
	}
	sourceID := agent.ID("claude")
	if record.Export.Provider == "codex-legacy" {
		sourceID = "codex"
	}
	if target == "" {
		target = sourceID
	}
	sm, ok := a.Module(sourceID)
	if !ok {
		return fail(agent.ErrNotInstalled)
	}
	tm, ok := a.Module(target)
	if !ok {
		return fail(agent.ErrNotInstalled)
	}
	if inv == nil || inv.Local() == nil || inv.Local().host == nil {
		return fail(errors.New("this machine must be scanned before importing a checkpoint"))
	}
	here := inv.Local()
	tin, ok := here.InstallProfile(target, opt.TargetProfile)
	if !ok {
		return fail(agent.ErrNotInstalled)
	}
	sourceIdentity := id
	if record.Export.Task != nil {
		if err = a.checkCloudTask(ctx, grant, record.Export.Observation); err != nil {
			return fail(err)
		}
		sourceIdentity = record.Export.Task.ID
	}
	endpoint := "cloud:" + record.Export.Provider + ":" + sourceIdentity
	home := "/hopsesh-cloud-checkpoint"
	native := path.Join(home, record.Export.Session+".jsonl")
	snapshot := host.NewSnapshot(record.Export.Provider, host.Facts{OS: "linux", Home: home, Endpoint: endpoint}, []host.SnapshotFile{{Path: native, Data: record.Export.Data, Mode: 0600}})
	sin := agent.Install{Agent: sourceID, Present: true, OS: "linux", Roots: map[string]string{"home": home}, Profile: &agent.RuntimeProfile{ID: sourceIdentity, Endpoint: endpoint, Agent: sourceID, Root: home, Name: "Approved cloud task", Binding: sourceIdentity}}
	ledgerDir := filepath.Join(root, sourceIdentity)
	if err = os.MkdirAll(ledgerDir, 0700); err != nil {
		return fail(err)
	}
	if err = localstate.PrivateDirectory(ledgerDir); err != nil {
		return fail(err)
	}
	input := move.Input{CheckpointIdentity: record.Export.Task != nil, Source: move.Side{Machine: snapshot, Module: sm, Install: sin}, Target: move.Side{Machine: here.host, Module: tm, Install: tin}, Session: agent.Summary{Key: agent.SessionKey{Agent: sourceID, Profile: sourceIdentity, Session: agent.SessionID(record.Export.Session)}, Path: native, CWD: record.Export.Workspace, Title: "Cloud conversation checkpoint"}, Live: agent.LiveInfo{State: agent.Unknown}, SourceReceipt: &move.ReceiptOwner{FS: host.LocalFS(), Machine: here.Name, NativePath: filepath.Join(ledgerDir, "source.jsonl")}}
	handoffProof := ""
	if record.Export.Task != nil {
		link, graph, err := a.cloudHandoffLink(*record.Export.Task)
		if err != nil {
			return fail(err)
		}
		if link != nil {
			input.CheckpointHandoff, input.Lineage = &link.Origin, graph
			handoffProof = link.review()
		}
	}
	if len(record.Prepared) > 0 && record.HandoffProof != handoffProof {
		return fail(errors.New("cloud handoff association changed since this operation was reviewed; prepare a new checkpoint operation"))
	}
	reviewExpires := time.Now().Add(10 * time.Minute)
	input.CheckSource = func(ctx context.Context) error {
		if !reviewExpires.After(time.Now()) {
			return errors.New("cloud checkpoint review expired; prepare it again")
		}
		currentGrant, err := a.checkCloudCheckpointAccess(ctx, id)
		if err != nil {
			return err
		}
		if record.Export.Task != nil {
			if err = a.checkCloudTask(ctx, currentGrant, record.Export.Observation); err != nil {
				return err
			}
			link, _, err := a.cloudHandoffLink(*record.Export.Task)
			if err != nil {
				return err
			}
			currentProof := ""
			if link != nil {
				currentProof = link.review()
			}
			if currentProof != handoffProof {
				return errors.New("cloud handoff association changed since review")
			}
		}
		if !record.Export.LeaseExpires.After(time.Now()) {
			return errors.New("cloud incarnation lease expired since review")
		}
		lock, err := localstate.Lock(ctx, filepath.Join(root, ".admission.lock"))
		if err != nil {
			return err
		}
		defer lock.Close()
		return checkpointRetired(root, opt.OperationID)
	}
	p, err := move.Build(ctx, input, opt)
	if err != nil {
		return fail(err)
	}
	oldOptions, _ := json.Marshal(record.Options)
	newOptions, _ := json.Marshal(opt)
	if len(record.Prepared) > 0 && record.Target == target && string(oldOptions) == string(newOptions) {
		p, err = move.RestorePrepared(record.Prepared, p)
	} else {
		if _, e := os.Stat(filepath.Join(a.StateDir, "operations", opt.OperationID+".json")); e == nil {
			return fail(errors.New("a committed checkpoint operation cannot change its destination or options"))
		} else if !os.IsNotExist(e) {
			return fail(e)
		}
		record.Prepared, err = p.FreezePrepared()
		record.Target, record.Options = target, opt
	}
	if err != nil {
		return fail(err)
	}
	p.Warnings = append(p.Warnings, fmt.Sprintf("Read-only cloud checkpoint: %d complete records; %d unfinished bytes excluded. Code and provider-private state are not included. Fresh incarnations require independent approval. Explicit owner-issued task continuity preserves verified checkpoint lineage; cloud handoff ancestry requires its separate saved provenance.", record.Export.Checkpoint.Records, record.Export.Checkpoint.OmittedTail))
	if record.Export.Task != nil {
		p.Warnings = append(p.Warnings, fmt.Sprintf("Verified owner-issued logical task %s, generation %d. This checkpoint preserves exact native-prefix ancestry across approved rebuilds; it does not restore provider-private state.", record.Export.Task.ID, record.Export.Generation))
	}
	if input.CheckpointHandoff != nil {
		p.Warnings = append(p.Warnings, "Linked to verified saved handoff "+input.CheckpointHandoff.Operation+". The task identity association preserves ancestry without counting another transfer; handoff briefing and conversion losses remain recorded.")
	}
	record.HandoffProof = handoffProof
	if err = saveRelayRecord(recordPath, &record); err != nil {
		return fail(err)
	}
	return p, input, record.Export, nil
}

func (a *App) checkCloudCheckpointAccess(ctx context.Context, id string) (relay.Grant, error) {
	cfg, err := config.Load()
	if err != nil {
		return relay.Grant{}, err
	}
	if !cfg.Relay.Enabled {
		return relay.Grant{}, errors.New("internet delivery is disabled")
	}
	g, err := (relay.Store{Directory: filepath.Join(a.StateDir, "relay")}).Grant(ctx, id)
	if err != nil {
		return g, err
	}
	if g.Kind != "cloud-session" || !g.AllowsSend("export", time.Now()) {
		return g, errors.New("this cloud incarnation is not approved for conversation export")
	}
	return g, nil
}
