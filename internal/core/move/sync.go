package move

import (
	"context"
	"fmt"

	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
)

// A workless visit synchronizes knowledge, never native transcript bytes or counters.
func applyNoWork(ctx context.Context, p *Plan, in Input, env Env) (*Result, error) {
	j, err := journal.New(env.StateDir, "lineage-sync", "Sync receipts for "+p.Title)
	if err != nil {
		return nil, err
	}
	if err = operationJournal(env, p, j); err != nil {
		return nil, err
	}
	j.AddKey(p.Placement.Key)
	res := &Result{Journal: j.ID, Mark: MarkOff}
	if err = applyRepo(ctx, p, in, env, res, func(string) {}); err != nil {
		return res, err
	}
	if err = operationNative(env, p, p.SyncTo.Path, p.ExpectedDestination[p.SyncTo.Path], res); err != nil {
		return res, err
	}
	if err = recordSync(ctx, p, in, j, res); err != nil {
		return res, err
	}
	res.Command, res.Run = launch.Shell(p.Resume, "", launch.DefaultShell()), p.Resume
	return res, j.Seal(machinesOf(ctx, in))
}
func recordSync(ctx context.Context, p *Plan, in Input, j *journal.Journal, res *Result) error {
	seg, err := readSegment(ctx, in.Target, *p.SyncTo)
	if err != nil {
		return err
	}
	if expected, ok := p.ExpectedDestination[p.SyncTo.Path]; ok && seg.Cursor != expected {
		return fmt.Errorf("destination changed since planning")
	}
	m := p.manifest.Clone()
	fsys, err := in.Target.Machine.FS(ctx)
	if err != nil {
		return err
	}
	if actual, e := lineage.Read(fsys, p.SyncTo.Path); e != nil {
		return e
	} else if actual != nil {
		if e = m.Merge(actual); e != nil {
			return e
		}
	}
	if err = m.Validate(); err != nil {
		return err
	}
	if err = j.WriteReceipt(fsys, p.Target.Location, lineage.PathFor(p.SyncTo.Path), m.ForBranch(p.targetLine).Encode(), true); err != nil {
		return err
	}
	fsys, receiptMachine, receiptPath, err := sourceReceipt(ctx, in)
	if err == nil {
		err = writeSourceReceipt(j, in, fsys, receiptMachine, lineage.PathFor(receiptPath), m.ForBranch(p.sourceLine).Encode())
	}
	if err != nil {
		res.Warnings = append(res.Warnings, "source receipt acknowledgement pending: "+err.Error())
	}
	return nil
}
