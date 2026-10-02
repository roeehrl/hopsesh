package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/fsys"
	"github.com/roeehrl/hopsesh/internal/core/hops"
	"github.com/roeehrl/hopsesh/internal/core/moved"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/rewrite"
	"github.com/roeehrl/hopsesh/internal/core/scan"
	"github.com/roeehrl/hopsesh/internal/core/sessions"
)

// Env is where Apply keeps its working state.
type Env struct {
	StateDir string // staging/ and undo/ live here
	Log      *audit.Log
	// Progress, if set, receives one line per step.
	Progress func(step string)
}

// Result reports a completed transport.
type Result struct {
	TargetFile string        `json:"targetFile"`
	UndoID     string        `json:"undoId"`
	Rewrite    rewrite.Stats `json:"rewrite"`
	Secrets    scan.Summary  `json:"secrets"`
	Copied     int           `json:"filesCopied"`
	Bytes      int64         `json:"bytesCopied"`
	Cloned     bool          `json:"cloned"`
	Worktree   string        `json:"worktreeCreated,omitempty"`
	SetAside   []string      `json:"setAside,omitempty"`
	// Round trips.
	Stopped   int               `json:"stoppedPid,omitempty"`
	Pushed    string            `json:"pushed,omitempty"`    // git's message after pushing on the source
	PushError string            `json:"pushError,omitempty"` // pushing failed; the move went on
	Sync      *repos.SyncResult `json:"sync,omitempty"`
	SyncNote  string            `json:"syncNote,omitempty"`
	Mark      string            `json:"mark"` // done | pending | off | failed
	MarkError string            `json:"markError,omitempty"`
}

// ErrBlocked means the plan has unresolved blockers.
var ErrBlocked = errors.New("plan has unresolved blockers")

// Apply performs the plan. On failure nothing is committed into Claude's folders.
func Apply(ctx context.Context, p *Plan, src Source, env Env) (*Result, error) {
	if len(p.Blockers) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrBlocked, strings.Join(p.Blockers, "; "))
	}
	step := func(s string) {
		if env.Progress != nil {
			env.Progress(s)
		}
	}
	res := &Result{TargetFile: p.TargetFile}

	// 0. Round trip: quit a copy running here, push on the source.
	if p.StopPID > 0 {
		step(fmt.Sprintf("quitting the copy of this session running here (pid %d)", p.StopPID))
		if err := sessions.StopLocal(p.Options.targetConfigDir(p), p.SessionID, p.StopPID, 15*time.Second); err != nil {
			return nil, err
		}
		res.Stopped = p.StopPID
		env.Log.Write(audit.Entry{Action: "session.stop", Session: p.SessionID, Detail: map[string]any{"pid": p.StopPID}})
	}
	if p.Push && src.Push != nil {
		step("pushing " + p.Repo.SourceBranch + " on " + p.SourceHost)
		msg, err := src.Push(ctx, p.SourceCWD)
		if err != nil {
			res.PushError = err.Error()
		} else {
			res.Pushed = nonEmpty(msg, "pushed")
		}
		env.Log.Write(audit.Entry{Action: "git.push", Host: p.SourceHost, Session: p.SessionID, Detail: map[string]any{"branch": p.Repo.SourceBranch, "ok": err == nil}})
	}

	// 1. Repository.
	if p.Repo.Action == "clone" {
		step("cloning " + p.Repo.Remote + " into " + p.Repo.LocalPath)
		if err := repos.Clone(ctx, p.Repo.Remote, p.Repo.LocalPath); err != nil {
			return nil, fmt.Errorf("clone failed: %w", err)
		}
		res.Cloned = true
		env.Log.Write(audit.Entry{Action: "git.clone", Session: p.SessionID, Detail: map[string]any{"remote": p.Repo.Remote, "dest": p.Repo.LocalPath}})
	}
	if p.Repo.Worktree != "" {
		if _, err := os.Stat(p.Repo.Worktree); err == nil {
			step("worktree already exists at " + p.Repo.Worktree)
		} else {
			step("creating worktree " + p.Repo.Worktree + " on " + p.Repo.SourceBranch)
			if err := repos.AddWorktree(ctx, p.Repo.LocalPath, p.Repo.SourceBranch, p.Repo.Worktree); err != nil {
				return res, fmt.Errorf("worktree: %w", err)
			}
			res.Worktree = p.Repo.Worktree
			env.Log.Write(audit.Entry{Action: "git.worktree", Session: p.SessionID, Detail: map[string]any{"path": p.Repo.Worktree, "branch": p.Repo.SourceBranch}})
		}
	}

	// 1b. Bring the checkout to the session's commit.
	if p.Sync != "" {
		step("checking the code against the session's commit")
		var from *repos.FetchSource
		if src.GitFetch != nil && src.Host != p.StartContext.TargetHost {
			from = src.GitFetch(nonEmpty(p.Repo.SourceMain, p.Repo.SourceTop))
		}
		if r, err := repos.Sync(ctx, syncDir(p), p.Repo.SourceBranch, p.Repo.SourceHead, true, from); err == nil {
			res.Sync = &r
			res.SyncNote = describeSync(r, p.Repo.SourceBranch, p.SourceHost)
			env.Log.Write(audit.Entry{Action: "git.sync", Session: p.SessionID, Detail: map[string]any{"state": r.State, "commit": r.Commit, "behind": r.Behind}})
		} else {
			res.SyncNote = "could not compare the checkout with the session's commit: " + err.Error()
		}
	}

	// 2. Stage.
	id := time.Now().UTC().Format("20060102T150405Z") + "-" + p.SessionID
	stage := filepath.Join(env.StateDir, "staging", id)
	raw := filepath.Join(stage, "raw")
	out := filepath.Join(stage, "out")
	if err := os.MkdirAll(raw, 0o700); err != nil {
		return res, err
	}
	defer os.RemoveAll(stage)
	step(fmt.Sprintf("copying %d file(s), %s", len(p.Files), human(p.TotalBytes)))
	for _, f := range p.Files {
		dst := filepath.Join(raw, filepath.FromSlash(f.Rel))
		n, sum, err := copyStable(src.FS, f.Src, dst, f.Kind == "transcript")
		if err != nil {
			return res, fmt.Errorf("copy %s: %w", f.Src, err)
		}
		res.Copied++
		res.Bytes += n
		env.Log.Write(audit.Entry{Action: "copy", Host: p.SourceHost, Session: p.SessionID, Detail: map[string]any{"src": f.Src, "bytes": n, "sha256": sum}})
	}

	// 3. Scan the originals, then rewrite into out/.
	for _, f := range p.Files {
		if f.Rewrite == "none" {
			continue
		}
		if fh, err := os.Open(filepath.Join(raw, filepath.FromSlash(f.Rel))); err == nil {
			s, _ := scan.Reader(fh)
			fh.Close()
			res.Secrets.Merge(s)
		}
	}
	step("rewriting paths")
	var redact func([]byte) ([]byte, int)
	if p.Options.Redact {
		redact = scan.Redact
	}
	for _, f := range p.Files {
		in := filepath.Join(raw, filepath.FromSlash(f.Rel))
		dst := filepath.Join(out, filepath.FromSlash(f.Rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return res, err
		}
		var err error
		switch f.Rewrite {
		case "jsonl":
			opt := rewrite.Options{Mappings: p.Mappings, StripBridge: true, DropMovedMarks: true, DropThinking: p.Options.DropThinking, Redact: redact}
			if p.OriginalID != "" {
				opt.RenameSession = [2]string{p.OriginalID, p.SessionID}
			}
			if f.Kind == "transcript" {
				opt.SessionID, opt.RelocatedCWD = p.SessionID, p.TargetCWD
				if p.OriginalID != "" {
					opt.AppendTitle = p.Title
				}
			}
			var st rewrite.Stats
			st, err = rewriteFile(in, dst, func(r io.Reader, w io.Writer) (rewrite.Stats, error) { return rewrite.JSONL(r, w, opt) })
			if f.Kind == "transcript" {
				res.Rewrite = st
			}
		case "text":
			_, err = rewriteFile(in, dst, func(r io.Reader, w io.Writer) (rewrite.Stats, error) {
				_, e := rewrite.Text(r, w, p.Mappings)
				return rewrite.Stats{}, e
			})
		default:
			err = copyLocal(in, dst)
		}
		if err != nil {
			return res, fmt.Errorf("rewrite %s: %w", f.Rel, err)
		}
	}
	// Verify the staged transcript reads back as the target session.
	sum, err := sessions.Summarize(fsys.Local{}, filepath.Join(out, p.SessionID+".jsonl"))
	if err != nil {
		return res, fmt.Errorf("verify: %w", err)
	}
	if sum.CWD != p.TargetCWD {
		return res, fmt.Errorf("verify: staged session points at %q, expected %q", sum.CWD, p.TargetCWD)
	}

	// 4. Commit with an undo journal.
	step("installing into " + p.TargetDir)
	undo := &UndoManifest{ID: id, SessionID: p.SessionID, Time: time.Now().UTC(), TargetFile: p.TargetFile, SourceHost: p.SourceHost}
	undoDir := filepath.Join(env.StateDir, "undo", id)
	if err := os.MkdirAll(filepath.Join(undoDir, "set-aside"), 0o700); err != nil {
		return res, err
	}
	var setAside []string
	for _, d := range p.Duplicates {
		setAside = append(setAside, d, filepath.Join(filepath.Dir(d), p.SessionID)) // and its sidecar folder
	}
	setAside = append(setAside, filepath.Join(p.Options.targetConfigDir(p), "file-history", p.SessionID))
	for i, d := range setAside {
		if _, err := os.Lstat(d); err != nil {
			continue
		}
		dst := filepath.Join(undoDir, "set-aside", fmt.Sprintf("%02d-%s", i, filepath.Base(d)))
		if err := moveFile(d, dst); err != nil {
			return res, fmt.Errorf("set aside %s: %w", d, err)
		}
		undo.SetAside = append(undo.SetAside, Moved{From: d, To: dst})
		res.SetAside = append(res.SetAside, d)
	}
	if err := writeUndo(undoDir, undo); err != nil {
		return res, err
	}
	for _, f := range p.Files {
		staged := filepath.Join(out, filepath.FromSlash(f.Rel))
		var dst string
		if strings.HasPrefix(f.Rel, "@file-history/") {
			dst = filepath.Join(p.Options.targetConfigDir(p), "file-history", filepath.FromSlash(strings.TrimPrefix(f.Rel, "@file-history/")))
		} else if f.Kind == "memory" {
			dst = filepath.Join(p.TargetDir, filepath.FromSlash(f.Rel))
			if _, err := os.Stat(dst); err == nil {
				continue // merge: never overwrite an existing memory file
			}
		} else {
			dst = filepath.Join(p.TargetDir, filepath.FromSlash(f.Rel))
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return res, err
		}
		if err := moveFile(staged, dst); err != nil {
			return res, err
		}
		undo.Created = append(undo.Created, dst)
	}
	now := time.Now()
	_ = os.Chtimes(p.TargetFile, now, now) // fresh mtime: survive the 30-day cleanup
	if err := writeUndo(undoDir, undo); err != nil {
		return res, err
	}
	res.UndoID = id
	// Save the start prompt so the resume command can read it instead of inlining it.
	promptFile := filepath.Join(env.StateDir, "prompts", p.SessionID+".md")
	if err := os.MkdirAll(filepath.Dir(promptFile), 0o700); err == nil {
		if os.WriteFile(promptFile, []byte(p.Resume.StartPrompt), 0o600) == nil {
			p.Resume.PromptFile = promptFile
		}
	}
	env.Log.Write(audit.Entry{Action: "pull.commit", Host: p.SourceHost, Session: p.SessionID, Detail: map[string]any{
		"target": p.TargetFile, "files": res.Copied, "bytes": res.Bytes, "undo": id, "secrets": res.Secrets.Total}})

	// 5. Record the hop and mark the copy left behind.
	markCopyLeftBehind(p, src, env, res)
	step("done")
	return res, nil
}

// markCopyLeftBehind records the hop and, after a handoff, titles the source copy
// "↪ moved to <this host> · <title>". A session still running there is marked by a later
// scan once it has stopped; marking never fails the move.
func markCopyLeftBehind(p *Plan, src Source, env Env, res *Result) {
	h := hops.Hop{Time: time.Now().UTC(), SessionID: p.SessionID, Title: p.Title, From: p.SourceHost,
		To: p.StartContext.TargetHost, Fork: p.Resume.Fork, SourceFile: p.SourceFile, Notified: p.Options.NotifyOld}
	switch p.Mark {
	case MarkNow:
		if ap, ok := src.FS.(fsys.Appender); ok {
			if err := ap.AppendKeepTime(p.SourceFile, moved.Record(p.SessionID, h.To, p.Title)); err != nil {
				h.Mark, h.MarkError = hops.MarkFailed, err.Error()
			} else {
				h.Mark = hops.MarkDone
			}
		} else {
			h.Mark, h.MarkError = hops.MarkFailed, "this machine's files cannot be written"
		}
	case MarkWhenStopped:
		h.Mark = hops.MarkPending
	default:
		h.Mark = hops.MarkOff
	}
	res.Mark, res.MarkError = h.Mark, h.MarkError
	if err := hops.Append(env.StateDir, h); err != nil && res.MarkError == "" {
		res.MarkError = "could not record the move: " + err.Error()
	}
	env.Log.Write(audit.Entry{Action: "hop.mark", Host: p.SourceHost, Session: p.SessionID, Detail: map[string]any{"mark": h.Mark, "error": h.MarkError}})
}

func (o Options) targetConfigDir(p *Plan) string {
	// TargetDir is <configDir>/projects/<slug>.
	return filepath.Dir(filepath.Dir(p.TargetDir))
}

// copyStable copies src (on fs) to dst. For a transcript that may still be growing it
// re-copies until the size is unchanged across the copy (at most three attempts).
func copyStable(fs fsys.FS, src, dst string, growable bool) (int64, string, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return 0, "", err
	}
	attempts := 1
	if growable {
		attempts = 3
	}
	var n int64
	var sum string
	for i := 0; i < attempts; i++ {
		before, err := fs.Stat(src)
		if err != nil {
			return 0, "", err
		}
		n, sum, err = copyOnce(fs, src, dst)
		if err != nil {
			return 0, "", err
		}
		after, err := fs.Stat(src)
		if err != nil {
			return 0, "", err
		}
		if before.Size() == after.Size() && n == after.Size() {
			return n, sum, nil
		}
	}
	// Still growing (a live session): keep the last consistent snapshot of complete lines.
	return n, sum, trimToLastNewline(dst)
}

func copyOnce(fs fsys.FS, src, dst string) (int64, string, error) {
	in, err := fs.Open(src)
	if err != nil {
		return 0, "", err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), os.Rename(tmp, dst)
}

func trimToLastNewline(p string) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	i := strings.LastIndexByte(string(b), '\n')
	if i < 0 || i == len(b)-1 {
		return nil
	}
	return os.WriteFile(p, b[:i+1], 0o600)
}

func rewriteFile(in, out string, f func(io.Reader, io.Writer) (rewrite.Stats, error)) (rewrite.Stats, error) {
	r, err := os.Open(in)
	if err != nil {
		return rewrite.Stats{}, err
	}
	defer r.Close()
	w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return rewrite.Stats{}, err
	}
	st, err := f(r, w)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return st, err
}

func copyLocal(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// moveFile renames, falling back to copy+remove across filesystems (files or directories).
func moveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		if err := copyTree(src, dst); err != nil {
			return err
		}
		return os.RemoveAll(src)
	}
	if err := copyLocal(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		return copyLocal(p, target)
	})
}

func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// Human formats a byte count for display.
func Human(n int64) string { return human(n) }

// Moved records a file moved out of the way.
type Moved struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// UndoManifest is the journal of one transport.
type UndoManifest struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"sessionId"`
	SourceHost string    `json:"sourceHost"`
	Time       time.Time `json:"time"`
	TargetFile string    `json:"targetFile"`
	Created    []string  `json:"created"`
	SetAside   []Moved   `json:"setAside"`
	Undone     bool      `json:"undone"`
}

func writeUndo(dir string, u *UndoManifest) error {
	b, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "manifest.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "manifest.json"))
}

// ListUndo returns transports that can be undone, newest first.
func ListUndo(stateDir string) ([]UndoManifest, error) {
	dirs, err := os.ReadDir(filepath.Join(stateDir, "undo"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []UndoManifest
	for i := len(dirs) - 1; i >= 0; i-- {
		b, err := os.ReadFile(filepath.Join(stateDir, "undo", dirs[i].Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var u UndoManifest
		if json.Unmarshal(b, &u) == nil && !u.Undone {
			out = append(out, u)
		}
	}
	return out, nil
}

// Undo reverses the newest transport of a session (match by session id prefix or undo id).
// Files created by the transport are removed and anything set aside is restored. Clones
// and worktrees are left in place (they may hold your work); the result says so.
func Undo(stateDir, match string, log *audit.Log) (*UndoManifest, error) {
	list, err := ListUndo(stateDir)
	if err != nil {
		return nil, err
	}
	for _, u := range list {
		if u.ID != match && !strings.HasPrefix(u.SessionID, match) {
			continue
		}
		for _, c := range u.Created {
			_ = os.Remove(c)
		}
		// Remove now-empty sidecar directories we created.
		for i := len(u.Created) - 1; i >= 0; i-- {
			removeEmptyParents(filepath.Dir(u.Created[i]), filepath.Dir(u.TargetFile))
		}
		for _, m := range u.SetAside {
			if err := moveFile(m.To, m.From); err != nil {
				return &u, fmt.Errorf("restore %s: %w", m.From, err)
			}
		}
		u.Undone = true
		_ = writeUndo(filepath.Join(stateDir, "undo", u.ID), &u)
		log.Write(audit.Entry{Action: "undo", Session: u.SessionID, Detail: map[string]any{"undo": u.ID}})
		return &u, nil
	}
	return nil, fmt.Errorf("no transport to undo matches %q", match)
}

func removeEmptyParents(dir, stop string) {
	for dir != stop && strings.HasPrefix(dir, stop) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
