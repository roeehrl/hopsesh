package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one audited action.
type Entry struct {
	Time    time.Time      `json:"time"`
	Action  string         `json:"action"` // e.g. ssh.exec, sftp.read, pull.commit, undo
	Host    string         `json:"host,omitempty"`
	Session string         `json:"session,omitempty"`
	Detail  map[string]any `json:"detail,omitempty"`
}

// Log appends entries to <dir>/YYYY-MM-DD.jsonl. A nil *Log discards entries.
type Log struct {
	dir string
	mu  sync.Mutex
}

// Open returns a log writing under dir (created with user-only permissions).
func Open(dir string) (*Log, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Log{dir: dir}, nil
}

// Dir returns the log directory.
func (l *Log) Dir() string {
	if l == nil {
		return ""
	}
	return l.dir
}

// Write appends one entry; logging failures never interrupt the user's operation.
func (l *Log) Write(e Entry) {
	if l == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(l.dir, e.Time.Format("2006-01-02")+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}
