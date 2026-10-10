// Package catalog stores disposable browsing metadata, never authentication files,
// full transcript bodies, or evidence used to authorize a transfer.
package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	mu   sync.Mutex
	held map[string]heldLease // collector leases this store holds
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
		s.db, s.err = sql.Open("sqlite", "file:"+p+"?_pragma=busy_timeout(1000)")
		if s.err != nil {
			return
		}
		s.db.SetMaxOpenConns(1)
		s.err = setup(s.db, `PRAGMA busy_timeout=1000; PRAGMA journal_mode=WAL; PRAGMA journal_size_limit=8388608;
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
				s.db, s.err = sql.Open("sqlite", "file:"+p+"?_pragma=busy_timeout(1000)")
				if s.err != nil {
					return
				}
				s.db.SetMaxOpenConns(1)
				_, s.err = s.db.Exec(`PRAGMA busy_timeout=1000;PRAGMA journal_mode=WAL;PRAGMA journal_size_limit=8388608;
CREATE TABLE snapshots(scope TEXT PRIMARY KEY,started INTEGER NOT NULL,body BLOB NOT NULL);
CREATE TABLE summaries(scope TEXT NOT NULL,path TEXT NOT NULL,signature TEXT NOT NULL,touched INTEGER NOT NULL,body BLOB NOT NULL,PRIMARY KEY(scope,path));`)
			}
		}
	})
	return s.err
}

// setup runs the schema, retrying while another process is opening the same catalog:
// switching to WAL can report SQLITE_BUSY at once instead of waiting.
func setup(db *sql.DB, schema string) error {
	var err error
	for range 40 {
		if _, err = db.Exec(schema); err == nil {
			return nil
		}
		if coded, ok := err.(interface{ Code() int }); !ok || coded.Code()&0xff != 5 {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return err
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
	if err != nil || bytes.Equal(b, old) {
		return // unchanged: an idle rescan writes nothing
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
	s.reclaim()
}

// reclaim gives back space once a quarter of the file is free (after snapshots shrink),
// so a catalog that once held large rows does not stay large. It rewrites the live
// rows once, then the next collection finds little to free and writes nothing.
func (s *Store) reclaim() {
	var free, pages, size int64
	if s.db.QueryRow("PRAGMA freelist_count").Scan(&free) != nil || s.db.QueryRow("PRAGMA page_count").Scan(&pages) != nil || s.db.QueryRow("PRAGMA page_size").Scan(&size) != nil {
		return
	}
	if free*size < 32<<20 || free*4 < pages {
		return
	}
	if _, err := s.db.Exec("VACUUM"); err == nil {
		_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	}
}

func (s *Store) Close() error {
	s.mu.Lock()
	for scope, h := range s.held {
		unlockFile(h.f)
		h.f.Close()
		delete(s.held, scope)
	}
	s.mu.Unlock()
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Claim coalesces browsing collectors across front ends. It is an OS file lock, not a
// database row: holding, renewing and releasing it writes nothing, and a crashed
// owner's lock is released with its process. A lock that cannot be taken for any
// reason other than another holder never blocks discovery.
func (s *Store) Claim(scope, owner string) bool {
	if s.dir == "catalog" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.held[scope]; ok {
		return h.owner == owner
	}
	dir := filepath.Join(s.dir, "leases")
	if os.MkdirAll(dir, 0700) != nil {
		return true
	}
	sum := sha256.Sum256([]byte(scope))
	f, err := os.OpenFile(filepath.Join(dir, hex.EncodeToString(sum[:12])+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return true
	}
	ok, err := tryLock(f)
	if err != nil {
		f.Close()
		return true
	}
	if !ok {
		f.Close()
		return false
	}
	if s.held == nil {
		s.held = map[string]heldLease{}
	}
	s.held[scope] = heldLease{owner, f}
	return true
}

func (s *Store) Release(scope, owner string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.held[scope]; ok && h.owner == owner {
		unlockFile(h.f)
		h.f.Close()
		delete(s.held, scope)
	}
}

type heldLease struct {
	owner string
	f     *os.File
}
