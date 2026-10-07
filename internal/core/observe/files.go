package observe

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Files provides bounded directory notifications. It never follows directory
// symlinks, reads file contents, or creates a missing watched root. Reconciliation
// remains necessary for network filesystems, overflow and watcher resource limits.
type Files struct {
	mu       sync.Mutex
	w        *fsnotify.Watcher
	roots    []string
	notify   func()
	limit    int
	problems chan error
	done     chan struct{}
}

func NewFiles(limit int, notify func()) (*Files, error) {
	if limit <= 0 || notify == nil {
		return nil, errors.New("invalid directory watcher options")
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	f := &Files{w: w, limit: limit, notify: notify, problems: make(chan error, 1), done: make(chan struct{})}
	go f.run()
	return f, nil
}

func (f *Files) Problems() <-chan error { return f.problems }
func (f *Files) Close() error           { err := f.w.Close(); <-f.done; return err }

func within(root, p string) bool {
	r, err := filepath.Rel(root, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

func (f *Files) relevant(p string) bool {
	for _, root := range f.roots {
		if within(root, p) || within(p, root) {
			return true
		}
	}
	return false
}

func (f *Files) report(err error) {
	select {
	case f.problems <- err:
	default:
	}
}

// SetRoots discovers new subdirectories after a notification or reconciliation,
// and removes obsolete watches. Missing roots watch their closest existing parent
// so installation/first-session creation is observed without polling the home tree.
func (f *Files) SetRoots(roots []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cleaned := []string{}
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			return fmt.Errorf("watch root must be absolute: %q", root)
		}
		cleaned = append(cleaned, filepath.Clean(root))
	}
	f.roots = cleaned
	wanted := map[string]bool{}
	var problems []error
	for _, root := range f.roots {
		info, err := os.Lstat(root)
		if errors.Is(err, fs.ErrNotExist) {
			parent := filepath.Dir(root)
			for {
				if st, e := os.Lstat(parent); e == nil && st.IsDir() {
					if len(wanted) >= f.limit && !wanted[parent] {
						problems = append(problems, fmt.Errorf("directory watch limit %d reached; reconciliation remains active", f.limit))
					} else {
						wanted[parent] = true
					}
					break
				}
				next := filepath.Dir(parent)
				if next == parent {
					break
				}
				parent = next
			}
			continue
		}
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				problems = append(problems, err)
				return fs.SkipDir
			}
			if !d.IsDir() {
				return nil
			}
			if len(wanted) >= f.limit && !wanted[path] {
				return fmt.Errorf("directory watch limit %d reached; reconciliation remains active", f.limit)
			}
			wanted[path] = true
			return nil
		})
		if err != nil {
			problems = append(problems, err)
		}
	}
	watched := map[string]bool{}
	for _, path := range f.w.WatchList() {
		watched[path] = true
		if !wanted[path] {
			if err := f.w.Remove(path); err != nil && !errors.Is(err, fsnotify.ErrNonExistentWatch) {
				problems = append(problems, err)
			}
		}
	}
	for path := range wanted {
		if !watched[path] {
			if err := f.w.Add(path); err != nil {
				problems = append(problems, err)
			}
		}
	}
	return errors.Join(problems...)
}

func (f *Files) run() {
	defer close(f.done)
	defer close(f.problems)
	for {
		select {
		case event, ok := <-f.w.Events:
			if !ok {
				return
			}
			if !event.Has(fsnotify.Write | fsnotify.Create | fsnotify.Remove | fsnotify.Rename) {
				continue
			}
			f.mu.Lock()
			relevant := f.relevant(event.Name)
			f.mu.Unlock()
			if relevant {
				f.notify()
			}
		case err, ok := <-f.w.Errors:
			if !ok {
				return
			}
			f.report(err)
			f.notify()
		}
	}
}
