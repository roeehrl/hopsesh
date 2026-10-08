package cloudintegration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

// Two private directory watches retire superseded connectors without another
// polling timer. No vendor transcript or sibling relay delivery wakes this path.
// A failed scope watch stops access rather than extending a stale incarnation.
func (s Incarnation) watchScope(ctx context.Context, cancel context.CancelCauseFunc) (func(), error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	relevant := map[string]bool{s.activePath(): true, filepath.Join(s.Directory, "task.json"): true}
	b, err := localstate.ReadPrivateFile(filepath.Join(s.Directory, "task.json"), 8192)
	if err == nil {
		var a taskAssociation
		if json.Unmarshal(b, &a) != nil || a.Task.Verify(a.Task.Owner.ID) != nil {
			_ = w.Close()
			return nil, errors.New("invalid cloud task scope for change notifications")
		}
		relevant[s.taskActivePath(a.Task)] = true
	} else if !os.IsNotExist(err) {
		_ = w.Close()
		return nil, err
	}
	for _, dir := range []string{filepath.Dir(s.Directory), s.Directory} {
		if err = w.Add(dir); err != nil {
			_ = w.Close()
			return nil, err
		}
	}
	if err = s.current(); err != nil {
		_ = w.Close()
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-w.Events:
				if !ok {
					if ctx.Err() == nil {
						cancel(errors.New("cloud scope change notifications closed"))
					}
					return
				}
				if relevant[event.Name] {
					if err := s.current(); err != nil {
						cancel(err)
						return
					}
				}
			case err, ok := <-w.Errors:
				if ctx.Err() == nil {
					if !ok {
						err = errors.New("notification errors channel closed")
					}
					cancel(fmt.Errorf("cloud scope change notifications failed: %w", err))
				}
				return
			}
		}
	}()
	return func() { _ = w.Close(); <-done }, nil
}
