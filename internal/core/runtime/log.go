package runtime

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

// LogBytes bounds each runtime log. Three archived files keep total usage below
// four MiB; rotation happens only on writes, without a timer or collector wakeup.
const LogBytes = 1 << 20

type LogWriter struct {
	mu         sync.Mutex
	path       string
	file, lock *os.File
	size       int64
}

func OpenLog(state string) (*LogWriter, error) {
	if err := os.MkdirAll(state, 0700); err != nil {
		return nil, err
	}
	if err := localstate.PrivateDirectory(state); err != nil {
		return nil, err
	}
	lock, err := localstate.TryLock(filepath.Join(state, "runtime-log.lock"))
	if err != nil {
		return nil, err
	}
	w := &LogWriter{path: filepath.Join(state, "runtime.log"), lock: lock}
	w.file, err = localstate.OpenPrivateAppend(w.path)
	if err != nil {
		lock.Close()
		return nil, err
	}
	st, err := w.file.Stat()
	if err != nil {
		w.Close()
		return nil, err
	}
	w.size = st.Size()
	if w.size > LogBytes {
		if err = w.rotate(); err != nil {
			w.Close()
			return nil, err
		}
	}
	return w, nil
}
func (w *LogWriter) rotate() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}
	// Refuse links/public archives before any replacement. Remove the oldest
	// before renaming on Windows, where an existing destination is not replaced.
	for i := 1; i <= 3; i++ {
		p := w.path + "." + strconv.Itoa(i)
		if _, err := os.Lstat(p); err == nil {
			if err = localstate.PrivateFile(p); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Remove(w.path + ".3"); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := 2; i >= 0; i-- {
		src := w.path
		if i > 0 {
			src += "." + strconv.Itoa(i)
		}
		dst := w.path + "." + strconv.Itoa(i+1)
		if err := os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	f, err := localstate.OpenPrivateAppend(w.path)
	if err != nil {
		return err
	}
	w.file = f
	w.size = 0
	return nil
}
func (w *LogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	if len(p) > LogBytes {
		return 0, errors.New("runtime log entry exceeds bound")
	}
	if w.size+int64(len(p)) > LogBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}
func (w *LogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var err error
	if w.file != nil {
		err = w.file.Close()
		w.file = nil
	}
	if w.lock != nil {
		closed := w.lock.Close()
		w.lock = nil
		if err == nil {
			err = closed
		}
	}
	return err
}
