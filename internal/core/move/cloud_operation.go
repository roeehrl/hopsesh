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
)

func assignCloudOperation(p *Plan, opt Options) error {
	p.OperationID = opt.OperationID
	if p.OperationID == "" {
		p.OperationID = newID()
	}
	if !validOperation.MatchString(p.OperationID) {
		return fmt.Errorf("invalid operation ID")
	}
	return nil
}

// Vendors without request-key support cannot safely be called again after an uncertain
// send. The durable journal remains recoverable through adoption/undo; repeated requests
// return the committed result or the explicit pending operation, never send twice.
func applyCloudOperation(ctx context.Context, p *Plan, in Input, env Env) (*Result, error) {
	if len(p.Blockers) > 0 {
		return applyPlan(ctx, p, in, env)
	}
	opt := p.Options
	opt.OperationID = ""
	version, account, target := "", "", p.Target.Location
	source := p.Source.ID
	if source == "" {
		source = p.Source.Location
	}
	if p.Handoff != nil {
		version = string(p.Handoff.head.Head)
	}
	if p.fetchIn != nil {
		version = p.fetchIn.Session.Updated.String()
		account = p.fetchIn.Session.Account
		target += "/" + string(p.Fetch.ContinueIn)
	}
	body, _ := json.Marshal(struct {
		Kind, Key, Source, Target, Version, Account string
		Options                                     Options
	}{p.Kind, p.Key.String(), source, target, version, account, opt})
	sum := sha256.Sum256(body)
	intent := hex.EncodeToString(sum[:])
	if err := os.MkdirAll(filepath.Dir(operationPath(env, p.OperationID)), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(operationPath(env, p.OperationID)+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err = lockOperationFile(lock); err != nil {
		return nil, fmt.Errorf("cloud operation is already being applied: %w", err)
	}
	stored, err := operationLoad(env, p.OperationID)
	if err == nil {
		if stored.Intent != intent {
			return nil, fmt.Errorf("operation ID belongs to another cloud request; use a new ID")
		}
		if stored.Journal != "" {
			j, e := journal.Load(env.StateDir, stored.Journal)
			if e != nil {
				return stored.Result, e
			}
			if j.Undone {
				return nil, fmt.Errorf("this operation was undone; use a new ID")
			}
		}
		if stored.Phase == "complete" {
			j, e := journal.Load(env.StateDir, stored.Journal)
			if e != nil {
				return stored.Result, e
			}
			if p.handoffIn != nil {
				e = j.RecoverReceipts(func(string) (host.FS, error) { return p.handoffIn.Source.Machine.FS(ctx) })
			}
			if e != nil {
				return stored.Result, e
			}
			return stored.Result, nil
		}
		return stored.Result, fmt.Errorf("cloud operation %s is pending in Activity (%s); finish its adoption or undo it before starting another request", p.OperationID, stored.Journal)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	record := &operationRecord{Intent: intent, Phase: "prepared", Plan: p}
	if err = operationSave(env, p, record); err != nil {
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
