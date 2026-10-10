package move

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

type operationRecord struct {
	Intent           string            `json:"intent"`
	Phase            string            `json:"phase"`
	Plan             *Plan             `json:"plan"`
	Manifest         *lineage.Manifest `json:"manifest"`
	SourceLine       string            `json:"sourceLine"`
	TargetLine       string            `json:"targetLine"`
	SourceReplica    lineage.ReplicaID `json:"sourceReplica"`
	SourceState      lineage.State     `json:"sourceState"`
	Journal          string            `json:"journal"`
	Request          *ir.WriteRequest  `json:"request,omitempty"`
	NativeBackup     string            `json:"nativeBackup,omitempty"`
	NativeBackupHead ir.Cursor         `json:"nativeBackupHead"`
	NativePath       string            `json:"nativePath,omitempty"`
	NativeCursor     ir.Cursor         `json:"nativeCursor"`
	Result           *Result           `json:"result,omitempty"`
}

func operationPath(env Env, id string) string {
	return filepath.Join(env.StateDir, "operations", id+".json")
}
func operationIntent(p *Plan) string {
	opt := p.Options
	opt.OperationID = ""
	body, _ := json.Marshal(struct {
		Source                                        agent.SessionKey
		Head                                          ir.NodeID
		Target, Endpoint, CWD, SourceEndpoint, Branch string
		TargetProfile, SourceBinding, TargetBinding   string
		Options                                       Options
	}{p.Key, p.sourceState.Head, string(p.Placement.Key.Agent), p.Target.ID, p.Target.CWD, p.Source.ID, p.sourceLine, p.Target.Profile, p.Source.Binding, p.Target.Binding, opt})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func operationLoad(env Env, id string) (*operationRecord, error) {
	body, err := host.LocalFS().ReadFile(operationPath(env, id), 64<<20)
	if err != nil {
		return nil, err
	}
	var r operationRecord
	if err = json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
func operationSave(env Env, p *Plan, r *operationRecord) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return host.LocalFS().WriteFile(operationPath(env, p.OperationID), body, 0o600)
}
func operationJournal(env Env, p *Plan, j *journal.Journal) error {
	if p.OperationID == "" {
		return nil
	}
	r, err := operationLoad(env, p.OperationID)
	if err != nil {
		return err
	}
	r.Journal = j.ID
	return operationSave(env, p, r)
}
func operationRequest(env Env, p *Plan, req ir.WriteRequest, native string, head ir.Cursor) error {
	r, err := operationLoad(env, p.OperationID)
	if err != nil {
		return err
	}
	r.Plan = p
	r.Manifest = p.manifest
	r.Request = &req
	r.NativeBackup = native
	r.NativeBackupHead = head
	return operationSave(env, p, r)
}
func operationNative(env Env, p *Plan, path string, cursor ir.Cursor, res *Result) error {
	if p.OperationID == "" {
		return nil
	}
	r, err := operationLoad(env, p.OperationID)
	if err != nil {
		return err
	}
	r.Phase = "native"
	r.NativePath = path
	r.NativeCursor = cursor
	r.Result = res
	return operationSave(env, p, r)
}

// Apply is idempotent for its stable operation ID. A restarted caller recovers durable
// receipts and verifies a native write through the module before committing its graph.
func Apply(ctx context.Context, p *Plan, in Input, env Env) (result *Result, failure error) {
	ctx = ir.WithLimits(ctx, p.Options.Limits)
	defer func() {
		if failure == nil && result != nil && p.Options.Notify && !p.NoWork && (p.Kind == KindMove || p.Kind == KindContinue) {
			result.Notice = fmt.Sprintf("Prepared in %s on %s. Work there has not yet been observed. The previous copy retains a movement notice; native-agent delivery requires its Hopsesh notice hooks.", p.Agent, p.Target.Location)
			if p.Options.Fork {
				result.Notice = fmt.Sprintf("A separate fork was prepared in %s on %s. The original branch remains available.", p.Agent, p.Target.Location)
			}
		}
	}()

	if err := ValidateProfiles(ctx, ProfileInput(p, in)); err != nil {
		return nil, err
	}
	if p.OperationID == "" {
		return applyPlan(ctx, p, in, env)
	}
	if !validOperation.MatchString(p.OperationID) {
		return nil, fmt.Errorf("invalid operation ID")
	}
	if p.Kind == KindHandoff || p.Kind == KindFetch {
		return applyCloudOperation(ctx, p, in, env)
	}
	if err := os.MkdirAll(filepath.Dir(operationPath(env, p.OperationID)), 0o700); err != nil {
		return nil, err
	}
	lockFile, err := os.OpenFile(operationPath(env, p.OperationID)+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer lockFile.Close()
	if err = lockOperationFile(lockFile); err != nil {
		return nil, fmt.Errorf("this transfer is already being applied: %w", err)
	}
	// Recovery must lock the committed destination, not a newly allocated plan ID.
	stored, loadErr := operationLoad(env, p.OperationID)
	destinationKey := p.Placement.Key
	if loadErr == nil {
		destinationKey = stored.Plan.Placement.Key
	}
	destinationSum := sha256.Sum256([]byte(p.Target.ID + ":" + destinationKey.String()))
	destinationPath := filepath.Join(in.Target.Machine.Facts.Home, ".hopsesh", "locks", hex.EncodeToString(destinationSum[:])+".lock")
	if err = os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
		return nil, err
	}
	destinationLock, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer destinationLock.Close()
	if err = lockOperationFile(destinationLock); err != nil {
		return nil, fmt.Errorf("destination is being changed by another transfer: %w", err)
	}
	intent := operationIntent(p)
	r, err := operationLoad(env, p.OperationID)
	if err == nil {
		if r.Intent != intent {
			return nil, fmt.Errorf("operation ID belongs to another transfer; use a new ID")
		}
		reach := func(name string) (host.FS, error) {
			if name == r.Plan.Source.Location {
				return in.Source.Machine.FS(ctx)
			}
			if name == r.Plan.Target.Location {
				return in.Target.Machine.FS(ctx)
			}
			return machinesOf(ctx, in)(name)
		}
		j, e := journal.Load(env.StateDir, r.Journal)
		if e != nil {
			return nil, fmt.Errorf("interrupted transfer journal: %w", e)
		}
		if j.Undone {
			return nil, fmt.Errorf("this transfer was undone; use a new operation ID")
		}
		if r.Phase == "complete" {
			if e = j.RecoverReceipts(reach); e != nil {
				return r.Result, e
			}
			p.Placement = r.Plan.Placement
			p.Resume = r.Plan.Resume
			return r.Result, nil
		}
		if r.Request == nil && r.NativePath == "" {
			return nil, fmt.Errorf("transfer interrupted before native commit; undo %s before retrying", j.ID)
		}
		original := r.Plan
		original.manifest = r.Manifest
		original.sourceLine = r.SourceLine
		original.targetLine = r.TargetLine
		original.sourceReplica = r.SourceReplica
		original.sourceState = r.SourceState
		original.native, original.nativeIn = p.native, p.nativeIn
		*p = *original
		res := r.Result
		if res == nil {
			res = &Result{Journal: j.ID}
		}
		if p.NoWork {
			if e = recordSync(ctx, p, in, j, res); e != nil {
				return res, e
			}
		} else if p.Kind == KindContinue {
			recoverer, ok := in.Target.Module.(agent.WriteRecoverer)
			if !ok {
				return res, fmt.Errorf("agent cannot verify interrupted writes; undo %s", j.ID)
			}
			h, e := in.Target.Machine.For(ctx, in.Target.Module.Spec(), in.Target.Install, j)
			if e != nil {
				return res, e
			}
			w, e := recoverer.RecoverWrite(ctx, h, in.Target.Install, *r.Request)
			if e != nil {
				return res, e
			}
			res.Files, res.Bytes = 1, w.To-w.From
			if e = recordContinuation(ctx, p, in, j, w, r.NativeBackup, r.NativeBackupHead, res); e != nil {
				return res, e
			}
		} else {
			if e = recordLineage(ctx, p, in, j, r.NativePath, ir.Cursor{Head: p.sourceState.Head, Offset: p.sourceState.Offset}, r.NativeCursor, res); e != nil {
				return res, e
			}
		}
		if e = j.RecoverReceipts(reach); e != nil {
			return res, e
		}
		if seg, e := readSegment(ctx, in.Source, in.Session); e == nil && seg.Cursor.Head == p.sourceState.Head {
			mark := agent.Mark{Kind: agent.MarkPrepared, Location: p.Target.Location, AgentName: p.Agent}
			if p.Kind == KindMove {
				mark = agent.Mark{Kind: agent.MarkMoved, Location: p.Target.Location}
			}
			markWith(ctx, p, in, j, env, seg.Cursor, mark, res)
		} else {
			res.Mark = "off"
		}
		res.Command, res.Run = launch.Shell(p.Resume, "", launch.DefaultShell()), p.Resume
		if e = j.Seal(reach); e != nil {
			return res, e
		}
		r.Phase = "complete"
		r.Result = res
		if e = operationSave(env, p, r); e != nil {
			return res, e
		}
		return res, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if len(p.Blockers) > 0 {
		return applyPlan(ctx, p, in, env)
	}
	r = &operationRecord{Intent: intent, Phase: "prepared", Plan: p, Manifest: p.manifest, SourceLine: p.sourceLine, TargetLine: p.targetLine, SourceReplica: p.sourceReplica, SourceState: p.sourceState}
	if err = operationSave(env, p, r); err != nil {
		return nil, err
	}
	res, err := applyPlan(ctx, p, in, env)
	latest, e := operationLoad(env, p.OperationID)
	if e != nil {
		return res, e
	}
	latest.Result = res
	if err == nil {
		latest.Phase = "complete"
	}
	if e = operationSave(env, p, latest); e != nil {
		return res, e
	}
	return res, err
}
