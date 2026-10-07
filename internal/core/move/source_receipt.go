package move

import (
	"context"
	"github.com/roeehrl/hopsesh/internal/core/host"
)

func sourceReceipt(ctx context.Context, in Input) (host.FS, string, string, error) {
	if r := in.SourceReceipt; r != nil {
		return r.FS, r.Machine, r.NativePath, nil
	}
	f, err := in.Source.Machine.FS(ctx)
	return f, in.Source.Machine.Name, in.Session.Path, err
}
