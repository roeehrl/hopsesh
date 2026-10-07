package localstate

import (
	"errors"
	"io"
)

// ReadPrivateFile checks the opened object before reading it and bounds the read.
// A pathname permission check followed by ReadFile would permit a symlink swap.
func ReadPrivateFile(path string, limit int64) ([]byte, error) {
	return readOwnedFile(path, limit, true)
}

// ReadOwnedFile is for explicitly authorized vendor data whose permissions are
// controlled by the vendor. It still refuses links, devices and foreign owners.
func ReadOwnedFile(path string, limit int64) ([]byte, error) {
	return readOwnedFile(path, limit, false)
}

func readOwnedFile(path string, limit int64, private bool) ([]byte, error) {
	if limit < 1 {
		return nil, errors.New("private file read requires a positive bound")
	}
	f, err := openOwnedRead(path, private)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() > limit {
		return nil, errors.New("private state file exceeds limit")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		return nil, errors.New("private state file exceeds limit")
	}
	return b, err
}
