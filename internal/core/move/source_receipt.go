package move

import (
	"context"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
)

func sourceReceipt(ctx context.Context, in Input) (host.FS, string, string, error) {
	if r := in.SourceReceipt; r != nil {
		return r.FS, r.Machine, r.NativePath, nil
	}
	f, err := in.Source.Machine.FS(ctx)
	return f, in.Source.Machine.Name, in.Session.Path, err
}

func writeSourceReceipt(j *journal.Journal, in Input, fsys host.FS, machine, path string, body []byte) error {
	if in.CheckpointIdentity && in.SourceReceipt != nil && in.Source.Machine.IsSnapshot() {
		return j.WriteCloudCheckpointReceipt(fsys, machine, path, body, in.Source.Install.BindingID())
	}
	return j.WriteReceipt(fsys, machine, path, body, false)
}
