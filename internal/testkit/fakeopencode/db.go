package fakeopencode

import (
	"crypto/rand"
	"crypto/sha1"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// schemaSQL is OpenCode 2.3.0's schema (agents/opencode/testdata/2.3.0/schema.sql) and
// the migrations it records on a new database.
//
//go:embed schema.sql
var schemaSQL string

// open opens OpenCode's database, making it (and its folder) the way OpenCode does on
// first use.
func (r *run) open() (*sql.DB, error) {
	p := r.paths()
	if err := os.MkdirAll(p.data, 0o700); err != nil {
		return nil, err
	}
	db, err := Open(p.db)
	if err != nil {
		return nil, err
	}
	if err := r.markTurns(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// markTurns makes the rows of the sessions with a turn running say so, as the service's
// rows do while it runs one: the execution claim (time_suspended) and a time_busy after
// the last idle.
func (r *run) markTurns(db *sql.DB) error {
	ms := r.now().UnixMilli()
	for _, id := range r.turns() {
		if _, err := db.Exec(`UPDATE session_v2 SET time_suspended = coalesce(time_suspended, ?),
			time_busy = CASE WHEN time_busy IS NULL OR time_busy <= coalesce(time_idle, 0) THEN ? ELSE time_busy END WHERE id = ?`, ms, ms, id); err != nil {
			return err
		}
	}
	return nil
}

// Open opens (or makes) an OpenCode 2.3.0 database at file.
func Open(file string) (*sql.DB, error) {
	if file == ":memory:" {
		return nil, errors.New("the OpenCode stand-in keeps no database in memory")
	}
	if fi, err := os.Stat(file); err == nil && fi.IsDir() {
		return nil, fmt.Errorf("unable to open database file: %s is a folder", file)
	}
	_, statErr := os.Stat(file)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(file)+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if os.IsNotExist(statErr) {
		if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
			db.Close()
			return nil, err
		}
		if _, err := db.Exec(schemaSQL); err != nil {
			db.Close()
			return nil, fmt.Errorf("making the database: %w", err)
		}
	}
	return db, nil
}

// OpenCode's ids (packages/schema/src/identifier.ts): a prefix, 12 hex digits of the low
// 48 bits of ms·4096 + a counter within the millisecond (negated for sessions, which sort
// newest first), and 14 random base62 characters.

var (
	idMu      sync.Mutex
	idLast    int64
	idCounter int64
)

// SessionID and MessageID mint ids as OpenCode does at t.
func SessionID(t time.Time) string { return newID("ses_", true, t) }
func MessageID(t time.Time) string { return newID("msg_", false, t) }

func newID(prefix string, descending bool, t time.Time) string {
	ms := t.UnixMilli()
	idMu.Lock()
	if ms != idLast {
		idLast, idCounter = ms, 0
	}
	idCounter++
	n := idCounter
	idMu.Unlock()
	v := uint64(ms)*0x1000 + uint64(n)
	if descending {
		v = ^v
	}
	var b [6]byte
	for i := range b {
		b[i] = byte(v >> (40 - 8*i))
	}
	const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var tail [14]byte
	_, _ = rand.Read(tail[:])
	for i := range tail {
		tail[i] = base62[int(tail[i])%len(base62)]
	}
	return prefix + hex.EncodeToString(b[:]) + string(tail[:])
}

// slug is a session's short name, two random words as OpenCode makes them.
func slug() string {
	adj := []string{"brave", "calm", "eager", "happy", "kind", "misty", "quiet", "silent", "sunny", "witty"}
	noun := []string{"canyon", "circuit", "comet", "falcon", "forest", "island", "lagoon", "moon", "otter", "wolf"}
	var b [2]byte
	_, _ = rand.Read(b[:])
	return adj[int(b[0])%len(adj)] + "-" + noun[int(b[1])%len(noun)]
}

// project is the project OpenCode files a folder under (packages/core/src/project.ts):
// in a git repository, the hash of its origin's host and path, else the id cached in
// <git common folder>/opencode, else its first root commit, else "global"; outside one,
// the hash of the folder.
type project struct {
	id, worktree, vcs string
}

// lookup reads an environment variable (nil: the process's).
type lookup func(string) (string, bool)

// gitEnv is the environment git runs with: the process's, with what the stand-in's
// environment says of the variables git reads.
func gitEnv(getenv lookup) []string {
	if getenv == nil {
		return nil
	}
	env := os.Environ()
	for _, k := range []string{"HOME", "USERPROFILE", "PATH", "XDG_CONFIG_HOME", "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR",
		"GIT_CEILING_DIRECTORIES", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM"} {
		kept := env[:0]
		for _, e := range env {
			if !strings.HasPrefix(e, k+"=") {
				kept = append(kept, e)
			}
		}
		env = kept
		if v, ok := getenv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func resolveProject(dir string, getenv lookup) project {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	env := gitEnv(getenv)
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", abs}, args...)...)
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	top := git("rev-parse", "--show-toplevel")
	if top == "" {
		return project{id: hash("directory:" + abs), worktree: filepath.ToSlash(abs)}
	}
	p := project{worktree: filepath.ToSlash(top), vcs: "git"}
	if n := remotePath(git("remote", "get-url", "origin")); n != "" {
		p.id = hash("git-remote:" + n)
		return p
	}
	// An id OpenCode cached in the repository's common git folder comes next.
	if common := git("rev-parse", "--path-format=absolute", "--git-common-dir"); common != "" {
		if b, err := os.ReadFile(filepath.Join(common, "opencode")); err == nil {
			if id := strings.TrimSpace(string(b)); id != "" {
				p.id = id
				return p
			}
		}
	}
	if roots := strings.Fields(git("rev-list", "--max-parents=0", "HEAD")); len(roots) > 0 {
		sort.Strings(roots)
		p.id = roots[0]
		return p
	}
	p.id = "global"
	return p
}

var scpRemote = regexp.MustCompile(`^([^@/:]+@)?([^/:]+):(.+)$`)

// remotePath is a remote's host and path the way OpenCode normalises it ("" for a local
// one).
func remotePath(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	parts := func(host, name string) string {
		name = strings.TrimLeft(name, "/")
		name = regexp.MustCompile(`\.git/?$`).ReplaceAllString(name, "")
		name = strings.TrimRight(name, "/")
		if host == "" || name == "" {
			return ""
		}
		return strings.ToLower(host) + "/" + name
	}
	if u, err := url.Parse(remote); err == nil && u.Scheme != "" && u.Host != "" {
		if u.Scheme == "file" {
			return ""
		}
		return parts(u.Hostname(), u.Path)
	}
	if m := scpRemote.FindStringSubmatch(remote); m != nil {
		return parts(m[2], m[3])
	}
	return ""
}

func hash(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// upsertProject records a project as OpenCode does when it first sees one.
func upsertProject(tx *sql.Tx, p project, now int64) error {
	var vcs any
	if p.vcs != "" {
		vcs = p.vcs
	}
	_, err := tx.Exec(`INSERT INTO project (id, worktree, vcs, time_created, time_updated, time_active, sandboxes)
		VALUES (?, ?, ?, ?, ?, ?, '[]') ON CONFLICT(id) DO UPDATE SET time_active = excluded.time_active`, p.id, p.worktree, vcs, now, now, now)
	return err
}
