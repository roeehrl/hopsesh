package sessions

import (
	"time"

	"github.com/roeehrl/hopsesh/internal/core/fsys"
	"github.com/roeehrl/hopsesh/internal/core/moved"
)

// turnsWindow is how much of a transcript's end is read to compare copies.
const turnsWindow = 512 << 10

func tailRecords(fs fsys.FS, file string) ([]record, error) {
	f, err := fs.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	start := size - turnsWindow
	partial := start > 0
	if start < 0 {
		start = 0
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && int64(len(buf)) != size-start {
		return nil, err
	}
	return parseLines(buf, partial), nil
}

func isTurn(r record) bool { return r.Type == "user" || r.Type == "assistant" }

// TurnsAfterMoveMark counts the messages added to a copy after hopsesh marked it
// "moved" (someone kept working on the copy left behind). It returns -1 when the copy
// carries no mark in the part of the file it reads.
func TurnsAfterMoveMark(fs fsys.FS, file string) (int, error) {
	rs, err := tailRecords(fs, file)
	if err != nil {
		return 0, err
	}
	n := -1
	for _, r := range rs {
		switch {
		case r.Type == "custom-title" && moved.IsTitle(r.CustomTitle):
			n = 0
		case r.Type == "custom-title" && n >= 0:
			n = -1 // renamed after the mark: no longer a "moved" copy
		case isTurn(r) && n >= 0:
			n++
		}
	}
	return n, nil
}

// TurnsSince counts the messages in a copy timestamped after t.
func TurnsSince(fs fsys.FS, file string, t time.Time) (int, error) {
	rs, err := tailRecords(fs, file)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rs {
		if !isTurn(r) || r.Timestamp == "" {
			continue
		}
		if ts, err := time.Parse(time.RFC3339Nano, r.Timestamp); err == nil && ts.After(t) {
			n++
		}
	}
	return n, nil
}
