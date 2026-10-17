package cloudintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

// Directory watches retire superseded connectors without another polling timer.
// No vendor transcript or sibling relay delivery triggers scope validation.
// A failed scope watch stops access rather than extending a stale incarnation.
func (s Incarnation) watchScope(ctx context.Context, cancel context.CancelCauseFunc) (func(), error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	// Install watches before reading the initial association, so a change
	// between observation and subscription cannot leave the wrong task watched.
	parent := filepath.Dir(s.Directory)
	scopeDirectories := map[string]bool{parent: true, s.Directory: true}
	// Watching the containing directory also observes a rename of the sessions
	// directory on backends that only report changes to a watched directory's
	// children. Only the two exact scope directories can invalidate this host.
	for _, dir := range []string{filepath.Dir(parent), parent, s.Directory} {
		if err = w.Add(dir); err != nil {
			_ = w.Close()
			return nil, err
		}
	}
	taskPath := filepath.Join(s.Directory, "task.json")
	relevant := map[string]bool{s.activePath(): true, taskPath: true}
	b, err := localstate.ReadPrivateFile(taskPath, 8192)
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
				if scopeDirectories[event.Name] && event.Op&(fsnotify.Rename|fsnotify.Remove) != 0 {
					cancel(errors.New("cloud scope directory moved or removed; restart the connector"))
					return
				}
				if relevant[event.Name] {
					if event.Name == taskPath {
						current, err := localstate.ReadPrivateFile(taskPath, 8192)
						if err != nil && !os.IsNotExist(err) {
							cancel(err)
							return
						}
						// Association is fixed for this running connector. In
						// particular, deleting it must not silently drop logical
						// task supersession checks and retain the old access.
						if !bytes.Equal(current, b) {
							cancel(errors.New("cloud task association changed; restart the connector"))
							return
						}
					}
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
