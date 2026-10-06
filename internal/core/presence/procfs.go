package presence

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
)

// procRoot is where Linux mounts the process table (tests point it elsewhere).
var procRoot = "/proc"

// readProc reads a /proc tree: each numeric folder's stat file gives the pid, the
// program's name (comm, cut to 15 bytes) and the parent's pid. Processes that exit while
// it reads are left out.
func readProc(ctx context.Context, root string) (Table, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	t := make(Table, len(ents))
	for _, e := range ents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, e.Name(), "stat"))
		if err != nil {
			continue
		}
		if p, ok := parseStat(b); ok && p.PID == pid {
			t[pid] = p
		}
	}
	return t, nil
}

// parseStat reads a /proc/<pid>/stat line: "4242 (tmux: server) S 1 …". The name sits
// between the first "(" and the last ")", since it may hold both.
func parseStat(b []byte) (Proc, bool) {
	lp, rp := bytes.IndexByte(b, '('), bytes.LastIndexByte(b, ')')
	if lp < 1 || rp < lp {
		return Proc{}, false
	}
	pid, err := strconv.Atoi(string(bytes.TrimSpace(b[:lp])))
	if err != nil {
		return Proc{}, false
	}
	rest := bytes.Fields(b[rp+1:]) // state, ppid, …
	if len(rest) < 2 {
		return Proc{}, false
	}
	ppid, err := strconv.Atoi(string(rest[1]))
	if err != nil {
		return Proc{}, false
	}
	return Proc{PID: pid, PPID: ppid, Name: string(b[lp+1 : rp])}, true
}
