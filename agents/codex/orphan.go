package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// NativeParentGone reports that a legacy fork's declared parent has no rollout left in
// sessions/ or archived_sessions/, as after Codex's thread/delete. A legacy fork copies the
// parent's records into its own rollout, so it is a complete conversation; a paginated
// fork (history_base) reads its parent's rollout and is never treated as orphaned.
func (m *Module) NativeParentGone(ctx context.Context, h agent.Host, in agent.Install, child agent.Summary) (bool, error) {
	if child.NativeParent == "" {
		return false, nil
	}
	f, err := h.FS().Open(child.Path)
	if err != nil {
		return false, err
	}
	head := make([]byte, headChunk)
	n, err := f.ReadAt(head, 0)
	f.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	mt, err := firstMeta(head[:n])
	if err != nil {
		return false, err
	}
	if mt.ForkedFromID != string(child.NativeParent) || mt.ID != string(child.Key.Session) {
		return false, fmt.Errorf("%w: native fork parent is not declared", agent.ErrDiverged)
	}
	if mt.HistoryBase != nil {
		return false, nil
	}
	suffix := "-" + string(child.NativeParent) + ".jsonl"
	root := in.Root(home)
	for _, dir := range []string{"sessions", "archived_sessions"} {
		found, err := hasRollout(ctx, h, h.Path().Join(root, dir), suffix, 4)
		if err != nil || found {
			return false, err
		}
	}
	return true, nil
}

// hasRollout looks for a rollout file ending in suffix under dir, depth directories deep
// (sessions/ nests YYYY/MM/DD; archived_sessions/ is flat). A missing dir holds none;
// any other read failure is returned, since absence is then unproven.
func hasRollout(ctx context.Context, h agent.Host, dir, suffix string, depth int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	entries, err := h.FS().ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		switch {
		case e.IsDir() && depth > 0:
			found, err := hasRollout(ctx, h, h.Path().Join(dir, e.Name()), suffix, depth-1)
			if err != nil || found {
				return found, err
			}
		case !e.IsDir() && isRollout(e.Name()) && strings.HasSuffix(e.Name(), suffix):
			return true, nil
		}
	}
	return false, nil
}
