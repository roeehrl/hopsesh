package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/roeehrl/hopsesh/internal/core/fsys"
)

// ConfigDir returns Claude Code's config directory for a machine: $CLAUDE_CONFIG_DIR, else
// <home>/.claude. For the local machine pass os.UserHomeDir() and os.Getenv.
func ConfigDir(home string, getenv func(string) string, join func(...string) string) string {
	if getenv != nil {
		if d := getenv("CLAUDE_CONFIG_DIR"); d != "" {
			return d
		}
	}
	if join == nil {
		join = filepath.Join
	}
	return join(home, ".claude")
}

// LocalConfigDir is ConfigDir for this machine.
func LocalConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return ConfigDir(home, os.Getenv, filepath.Join), nil
}

// Locator finds sessions under one config directory on one filesystem.
type Locator struct {
	FS        fsys.FS
	ConfigDir string
	// Workers bounds parallel summarisation (remote filesystems benefit from more).
	Workers int
}

// ListOptions filters List.
type ListOptions struct {
	IncludeSidechains bool // subagent-only transcripts (normally hidden, like the picker)
	IncludeEmpty      bool // bookkeeping-only transcripts with no messages
}

// List summarises every session transcript under <configDir>/projects.
func (l Locator) List(opt ListOptions) ([]*Summary, error) {
	projects := l.FS.Join(l.ConfigDir, "projects")
	dirs, err := l.FS.ReadDir(projects)
	if err != nil {
		return nil, err
	}
	workers := l.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	type job struct {
		file    string
		info    os.FileInfo
		sidecar bool
	}
	// List project folders in parallel (each is a round trip on a remote filesystem).
	var mu sync.Mutex
	var jobs []job
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := l.FS.Join(projects, d.Name())
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			entries, err := l.FS.ReadDir(dir)
			if err != nil {
				return
			}
			subdirs := map[string]bool{}
			for _, e := range entries {
				if e.IsDir() {
					subdirs[e.Name()] = true
				}
			}
			var local []job
			for _, e := range entries {
				n := e.Name()
				if e.IsDir() || !strings.HasSuffix(n, ".jsonl") || strings.Contains(n, ".orphaned-") {
					continue
				}
				local = append(local, job{file: l.FS.Join(dir, n), info: e, sidecar: subdirs[strings.TrimSuffix(n, ".jsonl")]})
			}
			mu.Lock()
			jobs = append(jobs, local...)
			mu.Unlock()
		}()
	}
	wg.Wait()

	out := make([]*Summary, len(jobs))
	next := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				j := jobs[i]
				side := j.sidecar
				if s, err := SummarizeHint(l.FS, j.file, j.info, &side); err == nil {
					out[i] = s
				}
			}
		}()
	}
	for i := range jobs {
		next <- i
	}
	close(next)
	wg.Wait()
	res := out[:0]
	for _, s := range out {
		if s == nil || (!opt.IncludeSidechains && s.IsSidechain) || (!opt.IncludeEmpty && !s.HasMessages) {
			continue
		}
		res = append(res, s)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].LastActivity.After(res[j].LastActivity) })
	return res, nil
}

// Find returns every transcript file for a session id (normally one; duplicates break
// `claude --resume <id>`, which is why transports remove them).
func (l Locator) Find(id string) ([]string, error) {
	projects := l.FS.Join(l.ConfigDir, "projects")
	dirs, err := l.FS.ReadDir(projects)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		p := l.FS.Join(projects, d.Name(), id+".jsonl")
		if fi, err := l.FS.Stat(p); err == nil && !fi.IsDir() {
			out = append(out, p)
		}
	}
	return out, nil
}

// LiveEntry is one running Claude Code process from <configDir>/sessions/<pid>.json.
// The format is internal to Claude Code; unknown fields are ignored.
type LiveEntry struct {
	PID             int    `json:"pid"`
	SessionID       string `json:"sessionId"`
	CWD             string `json:"cwd"`
	Status          string `json:"status"`
	WaitingFor      string `json:"waitingFor"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	Entrypoint      string `json:"entrypoint"`
	Kind            string `json:"kind"`
	ProcStart       string `json:"procStart"`
	StartedAt       int64  `json:"startedAt"`
	BridgeSessionID string `json:"bridgeSessionId"`
	Alive           bool   `json:"alive"`
}

// LiveRegistry reads the per-process registry. alive reports which pids are running on
// that machine; entries whose pid is not running are dropped (crash leftovers).
func (l Locator) LiveRegistry(alive func(pids []int) map[int]bool) ([]LiveEntry, error) {
	dir := l.FS.Join(l.ConfigDir, "sessions")
	entries, err := l.FS.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []LiveEntry
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".json") { // skips <pid>.<hash>.key secrets
			continue
		}
		names = append(names, n)
	}
	// Read the small registry files in parallel, one request each.
	results := make([]*LiveEntry, len(names))
	var wg sync.WaitGroup
	for i, n := range names {
		wg.Add(1)
		go func(i int, n string) {
			defer wg.Done()
			b, err := ReadSmall(l.FS, l.FS.Join(dir, n), 256*1024)
			if err != nil {
				return
			}
			var le LiveEntry
			if json.Unmarshal(b, &le) == nil && le.SessionID != "" && le.PID > 0 {
				results[i] = &le
			}
		}(i, n)
	}
	wg.Wait()
	for _, le := range results {
		if le != nil {
			out = append(out, *le)
		}
	}
	if alive != nil && len(out) > 0 {
		pids := make([]int, len(out))
		for i, le := range out {
			pids[i] = le.PID
		}
		up := alive(pids)
		kept := out[:0]
		for _, le := range out {
			if up[le.PID] {
				le.Alive = true
				kept = append(kept, le)
			}
		}
		out = kept
	}
	return out, nil
}
