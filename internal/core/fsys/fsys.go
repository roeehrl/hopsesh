// Package fsys is the small filesystem interface hopsesh reads sessions through, so the
// same code enumerates sessions on this machine (os) and on a remote one (SFTP).
package fsys

import (
	"io"
	"os"
	"path"
	"path/filepath"
)

// File is an open file that supports random access.
type File interface {
	io.Reader
	io.ReaderAt
	io.Closer
	Stat() (os.FileInfo, error)
}

// FS is a read-mostly filesystem.
type FS interface {
	ReadDir(dir string) ([]os.FileInfo, error)
	Stat(name string) (os.FileInfo, error)
	Open(name string) (File, error)
	// Join joins path elements using this filesystem's separator rules.
	Join(elem ...string) string
	// Base returns the last element of a path.
	Base(p string) string
}

// Local is the machine's own filesystem.
type Local struct{}

func (Local) ReadDir(dir string) ([]os.FileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]os.FileInfo, 0, len(entries))
	for _, e := range entries {
		if fi, err := e.Info(); err == nil {
			out = append(out, fi)
		}
	}
	return out, nil
}
func (Local) Stat(name string) (os.FileInfo, error) { return os.Stat(name) }
func (Local) Open(name string) (File, error)        { return os.Open(name) }
func (Local) Join(elem ...string) string            { return filepath.Join(elem...) }
func (Local) Base(p string) string                  { return filepath.Base(p) }

// Slash joins with forward slashes; remote SFTP paths always use them, including on
// Windows servers (where absolute paths look like /C:/Users/...).
type Slash struct{}

func (Slash) Join(elem ...string) string { return path.Join(elem...) }
func (Slash) Base(p string) string       { return path.Base(p) }

// Appender is a filesystem that can append a record to a file without changing its
// modification time (used only to mark a copy left behind; never on a live session).
type Appender interface {
	AppendKeepTime(name string, data []byte) error
}

// AppendKeepTime appends data, starting on a new line, and restores the file's times.
func (Local) AppendKeepTime(name string, data []byte) error {
	fi, err := os.Stat(name)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	size := fi.Size()
	if size > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, size-1); err == nil && last[0] != '\n' {
			data = append([]byte{'\n'}, data...)
		}
	}
	if _, err := f.WriteAt(data, size); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chtimes(name, fi.ModTime(), fi.ModTime())
}
