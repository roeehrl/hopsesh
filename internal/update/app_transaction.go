package update

import (
	"context"
	"crypto/rand"
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

func writeAppTransaction(root *os.Root, m appTransaction) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	name := ".hopsesh-manifest-" + rand.Text()
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(name) }()
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
	return root.Rename(name, appTransactionFile)
}

// The write-ahead manifest survives a process exit between file renames. Until
// every program/library is published, recovery restores the complete old set.
// File updates and startup cleanup share the same OS-held lock.
func publishWindowsAppFiles(dir string, files map[string][]byte, names []string, rename func(*os.Root, string, string) error) error {
	lock, err := localstate.Lock(context.Background(), filepath.Join(dir, ".hopsesh-app-update.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = checkAppFileNames(names); err != nil {
		return err
	}
	if err = recoverWindowsAppRoot(root); err != nil {
		return err
	}
	stage := ".hopsesh-app-stage-" + rand.Text()
	if err = root.Mkdir(stage, 0700); err != nil {
		return err
	}
	m := appTransaction{Stage: stage, Files: names, Existed: map[string]bool{}}
	ready := false
	defer func() {
		if !ready {
			_ = root.RemoveAll(stage)
		}
	}()
	for _, name := range names {
		path := filepath.FromSlash(name)
		if err = root.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if info, e := root.Lstat(path); e == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("app destination %s is not a regular file", name)
			}
			m.Existed[name] = true
		} else if !os.IsNotExist(e) {
			return e
		}
		if err = root.Remove(path + ".old"); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("previous app version is still in use: %w", err)
		}
		newPath := filepath.Join(stage, filepath.FromSlash(name))
		if err = root.MkdirAll(filepath.Dir(newPath), 0700); err != nil {
			return err
		}
		f, e := root.OpenFile(newPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
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
	if err = writeAppTransaction(root, m); err != nil {
		return err
	}
	ready = true // retain stage and manifest after an interrupted publication
	rollback := func(cause error) error { return errors.Join(cause, recoverWindowsAppRoot(root)) }
	for _, name := range names {
		path := filepath.FromSlash(name)
		if m.Existed[name] {
			if err = rename(root, path, path+".old"); err != nil {
				return rollback(err)
			}
		}
		if err = rename(root, filepath.Join(stage, filepath.FromSlash(name)), path); err != nil {
			return rollback(err)
		}
	}
	if err = root.Remove(appTransactionFile); err != nil {
		return rollback(err)
	}
	_ = root.RemoveAll(stage)
	return nil
}

func checkAppFileNames(names []string) error {
	allowed := append([]string{"hopsesh-app.exe", "hopsesh.exe"}, conptyFiles...)
	if len(names) == 0 || len(names) > len(allowed) {
		return errors.New("invalid app update file count")
	}
	seen := map[string]bool{}
	for _, name := range names {
		if !slices.Contains(allowed, name) || seen[name] {
			return errors.New("invalid or repeated app update filename")
		}
		seen[name] = true
	}
	return nil
}

func recoverWindowsAppRoot(root *os.Root) error {
	path := filepath.Join(root.Name(), appTransactionFile)
	data, err := localstate.ReadPrivateFile(path, 16384)
	if os.IsNotExist(err) {
		return nil
	}
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
	if err = checkAppFileNames(m.Files); err != nil {
		return err
	}
	for _, name := range m.Files {
		live := filepath.FromSlash(name)
		if old, e := root.Lstat(live + ".old"); e == nil {
			if !old.Mode().IsRegular() {
				return errors.New("app recovery backup is not a regular file")
			}
			if err = root.Remove(live); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err = root.Rename(live+".old", live); err != nil {
				return fmt.Errorf("restore %s: %w", name, err)
			}
		} else if !os.IsNotExist(e) {
			return e
		} else if !m.Existed[name] {
			if _, e = root.Stat(filepath.Join(m.Stage, filepath.FromSlash(name))); os.IsNotExist(e) {
				if err = root.Remove(live); err != nil && !os.IsNotExist(err) {
					return err
				}
			} else if e != nil {
				return e
			}
		}
	}
	if err = root.Remove(appTransactionFile); err != nil {
		return err
	}
	return root.RemoveAll(m.Stage)
}

func cleanupWindowsApp(dir string) {
	lock, err := localstate.TryLock(filepath.Join(dir, ".hopsesh-app-update.lock"))
	if err != nil {
		return
	}
	defer lock.Close()
	root, err := os.OpenRoot(dir)
	if err != nil {
		return
	}
	defer root.Close()
	if recoverWindowsAppRoot(root) != nil {
		return
	}
	for _, name := range append([]string{"hopsesh-app.exe", "hopsesh.exe"}, conptyFiles...) {
		_ = root.Remove(filepath.FromSlash(name) + ".old")
	}
}
