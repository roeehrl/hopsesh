package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

const appTransactionFile = ".hopsesh-app-update.json"

type appTransaction struct {
	Stage   string          `json:"stage"`
	Files   []string        `json:"files"`
	Existed map[string]bool `json:"existed"`
}

func writeAppTransaction(dir string, m appTransaction) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".hopsesh-manifest-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(dir, appTransactionFile))
}

// The write-ahead manifest survives a process exit between file renames. Until
// every program/library is published, recovery restores the complete old set.
// File updates and startup cleanup share the same OS-held lock.
func publishWindowsAppFiles(dir string, files map[string][]byte, names []string, rename func(string, string) error) error {
	lock, err := localstate.Lock(context.Background(), filepath.Join(dir, ".hopsesh-app-update.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = recoverWindowsApp(dir, rename); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(dir, ".hopsesh-app-stage-*")
	if err != nil {
		return err
	}
	m := appTransaction{Stage: filepath.Base(stage), Files: names, Existed: map[string]bool{}}
	ready := false
	defer func() {
		if !ready {
			_ = os.RemoveAll(stage)
		}
	}()
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		parent, e := filepath.EvalSymlinks(filepath.Dir(path))
		if e != nil {
			return e
		}
		base, e := filepath.EvalSymlinks(dir)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(base, parent)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("app archive destination escapes its installation directory")
		}
		if info, e := os.Lstat(path); e == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("app destination %s is not a regular file", name)
			}
			m.Existed[name] = true
		} else if !os.IsNotExist(e) {
			return e
		}
		if err = os.Remove(path + ".old"); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("previous app version is still in use: %w", err)
		}
		newPath := filepath.Join(stage, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(newPath), 0700); err != nil {
			return err
		}
		f, e := os.OpenFile(newPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
		if e != nil {
			return e
		}
		if _, e = f.Write(files[name]); e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if err = writeAppTransaction(dir, m); err != nil {
		return err
	}
	ready = true // retain stage and manifest after an interrupted publication
	rollback := func(cause error) error { return errors.Join(cause, recoverWindowsApp(dir, rename)) }
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if m.Existed[name] {
			if err = rename(path, path+".old"); err != nil {
				return rollback(err)
			}
		}
		if err = rename(filepath.Join(stage, filepath.FromSlash(name)), path); err != nil {
			return rollback(err)
		}
	}
	if err = os.Remove(filepath.Join(dir, appTransactionFile)); err != nil {
		return rollback(err)
	}
	_ = os.RemoveAll(stage)
	return nil
}

func recoverWindowsApp(dir string, rename func(string, string) error) error {
	path := filepath.Join(dir, appTransactionFile)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 16384 {
		return errors.New("invalid app update recovery manifest")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m appTransaction
	if err = json.Unmarshal(data, &m); err != nil {
		return err
	}
	if filepath.Base(m.Stage) != m.Stage || !strings.HasPrefix(m.Stage, ".hopsesh-app-stage-") || len(m.Files) == 0 || len(m.Files) > len(conptyFiles)+2 {
		return errors.New("invalid app update recovery scope")
	}
	allowed := append([]string{"hopsesh-app.exe", "hopsesh.exe"}, conptyFiles...)
	for _, name := range m.Files {
		if !slices.Contains(allowed, name) {
			return errors.New("invalid app recovery filename")
		}
	}
	for _, name := range m.Files {
		live := filepath.Join(dir, filepath.FromSlash(name))
		if _, e := os.Lstat(live + ".old"); e == nil {
			if err = os.Remove(live); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err = rename(live+".old", live); err != nil {
				return fmt.Errorf("restore %s: %w", name, err)
			}
		} else if !os.IsNotExist(e) {
			return e
		} else if !m.Existed[name] {
			if _, e = os.Stat(filepath.Join(dir, m.Stage, filepath.FromSlash(name))); os.IsNotExist(e) {
				if err = os.Remove(live); err != nil && !os.IsNotExist(err) {
					return err
				}
			} else if e != nil {
				return e
			}
		}
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(dir, m.Stage))
}

func cleanupWindowsApp(dir string) {
	lock, err := localstate.TryLock(filepath.Join(dir, ".hopsesh-app-update.lock"))
	if err != nil {
		return
	}
	defer lock.Close()
	if recoverWindowsApp(dir, os.Rename) != nil {
		return
	}
	for _, name := range append([]string{"hopsesh-app.exe", "hopsesh.exe"}, conptyFiles...) {
		_ = os.Remove(filepath.Join(dir, filepath.FromSlash(name)) + ".old")
	}
}
