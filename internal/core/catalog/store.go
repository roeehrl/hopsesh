// Package catalog stores disposable browsing metadata, never authentication files,
// full transcript bodies, or evidence used to authorize a transfer.
package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	dir  string
	once sync.Once
	db   *sql.DB
	err  error
}

func New(dir string) *Store { return &Store{dir: filepath.Join(dir, "catalog")} }

func (s *Store) open() error {
	s.once.Do(func() {
		if s.dir == "catalog" {
			s.err = os.ErrInvalid
			return
		}
		if s.err = os.MkdirAll(s.dir, 0700); s.err != nil {
			return
		}
		p := filepath.Join(s.dir, "sessions-v1.sqlite")
		// Create privately before SQLite creates its journals alongside it.
		f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			s.err = err
			return
		}
		f.Close()
		s.db, s.err = sql.Open("sqlite", p)
		if s.err != nil {
			return
		}
		s.db.SetMaxOpenConns(1)
		_, s.err = s.db.Exec(`PRAGMA busy_timeout=100; PRAGMA journal_mode=WAL;
CREATE TABLE IF NOT EXISTS leases(scope TEXT PRIMARY KEY,owner TEXT NOT NULL,expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS snapshots (scope TEXT PRIMARY KEY, started INTEGER NOT NULL, body BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS summaries (scope TEXT NOT NULL, path TEXT NOT NULL, signature TEXT NOT NULL, touched INTEGER NOT NULL, body BLOB NOT NULL, PRIMARY KEY(scope,path));`)
		if coded, ok := s.err.(interface{ Code() int }); ok && (coded.Code() == 11 || coded.Code() == 26) {
			s.db.Close()
			if os.Remove(p) == nil {
				_ = os.Remove(p + "-wal")
				_ = os.Remove(p + "-shm")
				f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
				if e != nil {
					s.err = e
					return
				}
				f.Close()
				s.db, s.err = sql.Open("sqlite", p)
				if s.err != nil {
					return
				}
				s.db.SetMaxOpenConns(1)
				_, s.err = s.db.Exec(`PRAGMA busy_timeout=100;PRAGMA journal_mode=WAL;
CREATE TABLE leases(scope TEXT PRIMARY KEY,owner TEXT NOT NULL,expires INTEGER NOT NULL);
CREATE TABLE snapshots(scope TEXT PRIMARY KEY,started INTEGER NOT NULL,body BLOB NOT NULL);
CREATE TABLE summaries(scope TEXT NOT NULL,path TEXT NOT NULL,signature TEXT NOT NULL,touched INTEGER NOT NULL,body BLOB NOT NULL,PRIMARY KEY(scope,path));`)
			}
		}
	})
	return s.err
}

func (s *Store) Read(scope string, dst any) bool {
	if s.open() != nil {
		return false
	}
	var b []byte
	if s.db.QueryRow("SELECT body FROM snapshots WHERE scope=?", scope).Scan(&b) != nil {
		return false
	}
	return json.Unmarshal(b, dst) == nil
}

func (s *Store) Load(scope, path, signature string, dst any) bool {
	if s.open() != nil {
		return false
	}
	var b []byte
	if s.db.QueryRow("SELECT body FROM summaries WHERE scope=? AND path=? AND signature=? AND touched>?", scope, path, signature, time.Now().Add(-24*time.Hour).UnixNano()).Scan(&b) != nil {
		return false
	}
	return json.Unmarshal(b, dst) == nil
}

func (s *Store) Save(scope, path, signature string, value any) {
	if s.open() != nil {
		return
	}
	b, err := json.Marshal(value)
	if err != nil {
		return
	}
	_, _ = s.db.Exec(`INSERT INTO summaries VALUES(?,?,?,?,?) ON CONFLICT(scope,path) DO UPDATE SET signature=excluded.signature,touched=excluded.touched,body=excluded.body`, scope, path, signature, time.Now().UnixNano(), b)
}

func (s *Store) Invalidate() {
	if s.open() == nil {
		_, _ = s.db.Exec("DELETE FROM summaries")
	}
}

func (s *Store) InvalidatePath(path string) {
	if s.open() == nil {
		_, _ = s.db.Exec("DELETE FROM summaries WHERE path=?", path)
	}
}

// Update serializes source merges across processes, avoiding lost updates when
// different front ends finish scans of different machines at the same time.
func (s *Store) Update(scope string, merge func([]byte) any) {
	if s.open() != nil {
		return
	}
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		return
	}
	defer conn.Close()
	if _, err = conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		return
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }()
	var old []byte
	_ = conn.QueryRowContext(context.Background(), "SELECT body FROM snapshots WHERE scope=?", scope).Scan(&old)
	b, err := json.Marshal(merge(old))
	if err != nil {
		return
	}
	if _, err = conn.ExecContext(context.Background(), `INSERT INTO snapshots VALUES(?,?,?) ON CONFLICT(scope) DO UPDATE SET started=excluded.started,body=excluded.body`, scope, time.Now().UnixNano(), b); err != nil {
		return
	}
	_, _ = conn.ExecContext(context.Background(), "COMMIT")
	_ = conn.Close()
	s.collect()
}

// Collection is bounded and runs after a snapshot, never once per summary.
func (s *Store) collect() {
	_, _ = s.db.Exec("DELETE FROM summaries WHERE touched<?", time.Now().Add(-24*time.Hour).UnixNano())
	_, _ = s.db.Exec("DELETE FROM summaries WHERE rowid IN (SELECT rowid FROM summaries ORDER BY touched DESC LIMIT -1 OFFSET 100000)")
	_, _ = s.db.Exec("DELETE FROM snapshots WHERE started<?", time.Now().Add(-30*24*time.Hour).UnixNano())
	_, _ = s.db.Exec("DELETE FROM snapshots WHERE rowid IN (SELECT rowid FROM snapshots ORDER BY started DESC LIMIT -1 OFFSET 100)")
}

func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Claim coalesces browsing collectors across front ends. A failed cache is not a
// reason to block discovery; a crashed owner's lease expires without a daemon.
func (s *Store) Claim(scope, owner string) bool {
	if s.open() != nil {
		return true
	}
	result, err := s.db.Exec(`INSERT INTO leases VALUES(?,?,?) ON CONFLICT(scope) DO UPDATE SET owner=excluded.owner,expires=excluded.expires WHERE leases.expires<? OR leases.owner=excluded.owner`, scope, owner, time.Now().Add(15*time.Second).UnixNano(), time.Now().UnixNano())
	if err != nil {
		// Only contention can indicate an active collector. A failed cache must
		// not make every front end wait for a lease it can never read.
		if coded, ok := err.(interface{ Code() int }); ok {
			code := coded.Code() & 0xff
			if code == 5 || code == 6 {
				return false
			}
		}
		return true
	}
	n, _ := result.RowsAffected()
	return n > 0
}
func (s *Store) Release(scope, owner string) {
	if s.open() == nil {
		_, _ = s.db.Exec("DELETE FROM leases WHERE scope=? AND owner=?", scope, owner)
	}
}
