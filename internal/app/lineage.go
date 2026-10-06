package app

import (
	"context"
	"fmt"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
)

// ArchiveLineage explicitly removes unsupported metadata from the active namespace.
// It retains the original sidecar beside the transcript; Activity can undo the rename.
func (a *App) ArchiveLineage(ctx context.Context, inv *Inventory, e Entry) (*journal.Journal, error) {
	machine := inv.Machine(e.Machine)
	if machine == nil || machine.host == nil || e.Location.IsCloud() {
		return nil, fmt.Errorf("session machine is unavailable")
	}
	fsys, err := machine.host.FS(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = lineage.Read(fsys, e.Session.Path); err == nil {
		return nil, fmt.Errorf("lineage is supported or absent; nothing to archive")
	}
	path := lineage.PathFor(e.Session.Path)
	if _, err = fsys.Stat(path); err != nil {
		return nil, err
	}
	j, err := journal.New(a.StateDir, "archive-lineage", "Archive unsupported lineage for "+e.Session.Title)
	if err != nil {
		return nil, err
	}
	j.AddKey(e.Session.Key)
	if err = j.Rename(fsys, e.Machine, path, path+".archived-"+j.ID); err != nil {
		return j, err
	}
	if err = j.WriteFile(fsys, e.Machine, path, lineage.New("reset/"+j.ID+"/"+e.Session.Key.String()).Encode(), 0600); err != nil {
		return j, err
	}
	return j, j.Seal(func(string) (host.FS, error) { return fsys, nil })
}
