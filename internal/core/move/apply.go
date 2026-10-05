package move

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/rewrite"
	"github.com/roeehrl/hopsesh/internal/core/scan"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// ErrBlocked means the plan has blockers.
var ErrBlocked = errors.New("the move cannot go ahead")

// Env is where apply keeps its state and logs.
type Env struct {
	StateDir string
	Audit    *audit.Log
	Progress func(step string)
	// Step runs a driver's terminal step (a hand-off's Sent.Run) where the user sees and
	// answers it, and returns the session it started, as the module read it from what the
	// step printed (or as the user pasted its link). nil: this front end has no terminal for
	// it, and the plan said so.
	Step StepRunner
}

// StepRunner runs a terminal step (Env.Step).
type StepRunner func(ctx context.Context, s TermStep) (StepResult, error)

// StepResult is the session a terminal step started.
type StepResult struct {
	Session agent.CloudSession
	// Pasted: the user pasted its link (hopsesh saw none in what the step printed).
	Pasted bool
}

// TermStep is a cloud driver's command that needs the user's terminal (claude --cloud
// "<briefing>"): the user sees it and answers what it asks; hopsesh only watches what it
// prints and its exit code, and the module reads the session from them.
type TermStep struct {
	Agent      agent.ID `json:"agent"` // the module that reads what it printed
	Cloud      string   `json:"cloud"`
	CloudTitle string   `json:"cloudTitle"`
	Title      string   `json:"title"` // the session handed off, for people
	// Run is the command; its Argv[0] is the driver's path here when hopsesh found it.
	Run agent.Command `json:"run"`
	// Folder is the hand-off folder it runs in, which the driver may ask the user to trust.
	Folder string `json:"folder"`
}

// Result is what a move did.
type Result struct {
	Journal    string            `json:"journal"` // id for undo
	Files      int               `json:"files"`
	Bytes      int64             `json:"bytes"`
	Rewrite    rewrite.Stats     `json:"rewrite"`
	Secrets    scan.Summary      `json:"secrets"`
	Cloned     bool              `json:"cloned,omitempty"`
	Worktree   string            `json:"worktree,omitempty"`
	SetAside   []string          `json:"setAside,omitempty"`
	Stopped    bool              `json:"stopped,omitempty"`
	Pushed     string            `json:"pushed,omitempty"`
	PushError  string            `json:"pushError,omitempty"`
	Sync       *repos.SyncResult `json:"sync,omitempty"`
	SyncNote   string            `json:"syncNote,omitempty"`
	Mark       string            `json:"mark"` // done | pending | off | failed
	MarkError  string            `json:"markError,omitempty"`
	PromptFile string            `json:"promptFile,omitempty"`
	Command    string            `json:"command"` // to resume, for this machine's shell
	// Run is Command as an argument list (with the start prompt as its last argument when
	// PromptFile is set), for a terminal hopsesh opens on it.
	Run      agent.Command `json:"run"`
	Notice   string        `json:"notice,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`
	// Owed is a mark the source still needs once its open copy ends, when the source is a
	// snapshot: its sender keeps it (on other sources hopsesh keeps it here).
	Owed *lineage.Pending `json:"owed,omitempty"`
	// Fetch is what a fetch from a cloud did.
	Fetch *FetchResult `json:"fetch,omitempty"`
	// Handoff is what a hand-off to a cloud did (also when a step failed).
	Handoff *HandoffResult `json:"handoff,omitempty"`
	// Hop is where a hop from one cloud to another stands (Fetch is its first leg's result,
	// Handoff its second's).
	Hop *HopResult `json:"hop,omitempty"`
}

// machinesOf reaches the two machines of a move by name, for the journal.
func machinesOf(ctx context.Context, in Input) func(string) (host.FS, error) {
	return func(name string) (host.FS, error) {
		switch name {
		case in.Target.Machine.Name:
			return in.Target.Machine.FS(ctx)
		case in.Source.Machine.Name:
			return in.Source.Machine.FS(ctx)
		}
		return nil, fmt.Errorf("%s is not part of this move", name)
	}
}

// Apply carries out a plan.
func Apply(ctx context.Context, p *Plan, in Input, env Env) (*Result, error) {
	if len(p.Blockers) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrBlocked, strings.Join(p.Blockers, "; "))
	}
	switch p.Kind {
	case KindFetch:
		return applyFetch(ctx, p, env)
	case KindHandoff:
		return applyHandoff(ctx, p, env)
	}
	step := func(s string) {
		if env.Progress != nil {
			env.Progress(s)
		}
	}
	tgt := in.Target
	if !tgt.Machine.Local {
		return nil, errors.New("a move installs on this machine")
	}
	if p.Kind == KindContinue {
		return applyContinue(ctx, p, in, env)
	}
	j, err := journal.New(env.StateDir, journal.KindMove, fmt.Sprintf("%s from %s", p.Title, p.Source.Location))
	if err != nil {
		return nil, err
	}
	j.AddKey(p.Placement.Key)
	res := &Result{Journal: j.ID}
	tgtHost, err := tgt.Machine.For(ctx, tgt.Module.Spec(), tgt.Install, j)
	if err != nil {
		return nil, err
	}

	// 0. Round trip: quit the copy open here, push on the source.
	if p.StopHere {
		step("quitting the copy of this session open here")
		for _, c := range in.Copies {
			if c.Live.State == agent.Live {
				if err := tgt.Module.(agent.Stopper).Stop(ctx, tgtHost, tgt.Install, c.Summary, 15*time.Second); err != nil {
					return nil, err
				}
			}
		}
		res.Stopped = true
		env.Audit.Write(audit.Entry{Action: "session.stop", Session: p.Key.String()})
	}
	if p.Push && in.Push != nil {
		step("pushing " + p.Repo.SourceBranch + " on " + p.Source.Location)
		msg, err := in.Push(ctx, p.Source.CWD)
		if err != nil {
			res.PushError = err.Error()
		} else {
			res.Pushed = nonEmpty(msg, "pushed")
		}
		env.Audit.Write(audit.Entry{Action: "git.push", Host: p.Source.Location, Session: p.Key.String(), Detail: map[string]any{"ok": err == nil}})
	}

	// 1. Repository and code.
	if err := applyRepo(ctx, p, in, env, res, step); err != nil {
		return res, err
	}

	mainDst, srcHead, tgtHead, err := install(ctx, p, in, env, j, res, step)
	if err != nil {
		return res, err
	}
	env.Audit.Write(audit.Entry{Action: "move.install", Host: p.Source.Location, Session: p.Placement.Key.String(),
		Detail: map[string]any{"target": mainDst, "files": res.Files, "bytes": res.Bytes, "journal": j.ID, "secrets": res.Secrets.Total}})

	// 5. Lineage beside both copies.
	recordLineage(ctx, p, in, j, mainDst, srcHead, tgtHead, res)
	if pi, ok := tgt.Module.(agent.PostInstaller); ok {
		if err := pi.AfterInstall(ctx, tgtHost, tgt.Install, p.Placement.Key, p.Placement); err != nil {
			res.Warnings = append(res.Warnings, "after installing: "+err.Error())
		}
	}

	// 6. Mark the copy left behind, and save the start prompt.
	markWith(ctx, p, in, j, env, srcHead, agent.Mark{Kind: agent.MarkMoved, Location: p.Target.Location}, res)
	promptFile := filepath.Join(env.StateDir, "prompts", string(p.Placement.Key.Agent)+"-"+string(p.Placement.Key.Session)+".md")
	if os.MkdirAll(filepath.Dir(promptFile), 0o700) == nil && os.WriteFile(promptFile, []byte(p.StartPrompt), 0o600) == nil {
		res.PromptFile = promptFile
	}
	res.Command, res.Run = launch.Shell(p.Resume, res.PromptFile, launch.DefaultShell()), p.Resume
	if p.Options.Notify && (!agent.Has(tgt.Module, agent.CapNotify) || !p.Options.RemoteControl) {
		// The agent cannot tell the old session itself: the user pastes this there.
		res.Notice = launch.OldSessionNotice(tgt.Machine.Name, p.Target.CWD, p.NewName, p.Options.Fork && p.Live)
	}
	if err := j.Seal(machinesOf(ctx, in)); err != nil {
		res.Warnings = append(res.Warnings, "could not record what this changed, for a safe undo: "+err.Error())
	}
	step("done")
	return res, nil
}

func applyRepo(ctx context.Context, p *Plan, in Input, env Env, res *Result, step func(string)) error {
	r := p.Repo
	if r.Action == RepoClone {
		step("cloning " + r.Remote + " into " + r.LocalPath)
		if err := repos.Clone(ctx, r.Remote, r.LocalPath); err != nil {
			return fmt.Errorf("clone failed: %w", err)
		}
		res.Cloned = true
		env.Audit.Write(audit.Entry{Action: "git.clone", Session: p.Key.String(), Detail: map[string]any{"remote": r.Remote, "dest": r.LocalPath}})
	}
	// The other machine's repository, for commits and branches that were never pushed.
	var from *repos.FetchSource
	if in.GitFetch != nil && p.Source.Location != p.Target.Location {
		from = in.GitFetch(nonEmpty(r.SourceMain, r.SourceTop))
	}
	if r.Worktree != "" {
		if _, err := os.Stat(r.Worktree); err != nil {
			step("creating worktree " + r.Worktree + " on " + r.SourceBranch)
			if err := repos.AddWorktree(ctx, r.LocalPath, r.SourceBranch, r.Worktree, from); err != nil {
				return fmt.Errorf("worktree: %w", err)
			}
			res.Worktree = r.Worktree
			env.Audit.Write(audit.Entry{Action: "git.worktree", Session: p.Key.String(), Detail: map[string]any{"path": r.Worktree, "branch": r.SourceBranch}})
		}
	}
	if p.Sync == "" {
		return nil
	}
	step("checking the code against the session's commit")
	dir := p.Target.CWD
	if r.Worktree != "" {
		dir = r.Worktree
	}
	sr, err := repos.Sync(ctx, dir, r.SourceBranch, r.SourceHead, true, from, in.Worktrees)
	if err != nil {
		res.SyncNote = "could not compare the checkout with the session's commit: " + err.Error()
		return nil
	}
	res.Sync = &sr
	res.SyncNote = DescribeSync(sr, r.SourceBranch, p.Source.Location)
	env.Audit.Write(audit.Entry{Action: "git.sync", Session: p.Key.String(), Detail: map[string]any{"state": sr.State, "commit": sr.Commit, "behind": sr.Behind}})
	return nil
}

// DescribeSync turns a sync result into a line for people.
func DescribeSync(r repos.SyncResult, branch, source string) string {
	c := short(r.Commit)
	switch r.State {
	case repos.SyncUpToDate:
		return "the checkout is at the session's commit " + c
	case repos.SyncFastForwarded:
		if r.FromSource {
			return fmt.Sprintf("fast-forwarded the checkout by %d commit(s) to %s, fetched straight from %s (not pushed yet)", r.Behind, c, source)
		}
		return fmt.Sprintf("fast-forwarded the checkout by %d commit(s) to %s", r.Behind, c)
	case repos.SyncAhead:
		return "the checkout already contains the session's commit " + c + " (and more)"
	case repos.SyncMissing:
		return fmt.Sprintf("commit %s is not here, even after fetching from origin and from %s: push it on %s, then run `git pull` here", c, source, source)
	case repos.SyncDiverged:
		return fmt.Sprintf("the checkout and %s have diverged from %s; merge or rebase yourself", source, c)
	case repos.SyncDirty:
		return fmt.Sprintf("the checkout is %d commit(s) behind %s but has uncommitted changes; commit or stash, then `git pull --ff-only`", r.Behind, c)
	case repos.SyncOtherBranch:
		return fmt.Sprintf("the checkout is on %s, not %s; switch branches to get commit %s", r.Branch, branch, c)
	case repos.SyncBehind:
		return fmt.Sprintf("the checkout is %d commit(s) behind %s; run `git pull --ff-only`", r.Behind, c)
	}
	return r.State
}

// heads reads the conversation head of the copy as it left the source (the staged raw
// file) and as it arrives (the staged rewritten file), when the module can read.
func heads(ctx context.Context, p *Plan, in Input, h agent.Host, raw string, staged map[string]string) (src, tgt ir.Cursor) {
	reader, ok := in.Target.Module.(agent.Reader)
	if !ok {
		return
	}
	for _, f := range p.Files.Files {
		if f.From.Role != agent.RoleMain {
			continue
		}
		s := in.Session
		s.Path = filepath.Join(raw, f.From.Root, filepath.FromSlash(f.From.Rel))
		if seg, err := reader.Read(ctx, h, in.Source.Install, s, ir.Cursor{}); err == nil {
			src = seg.Cursor
		}
		t := s
		t.Key, t.Path, t.CWD = p.Placement.Key, staged[agent.StagedKey(f)], p.Target.CWD
		if seg, err := reader.Read(ctx, h, in.Target.Install, t, ir.Cursor{}); err == nil {
			tgt = seg.Cursor
		}
	}
	return
}

// install copies a session's bundle here, rewrites and verifies it, and installs it,
// setting aside the copies it replaces (steps 2 to 4 of a move). It returns the installed
// main file and the heads of the source and of the installed copy.
func install(ctx context.Context, p *Plan, in Input, env Env, j *journal.Journal, res *Result, step func(string)) (string, ir.Cursor, ir.Cursor, error) {
	src, tgt := in.Source, in.Target
	var none ir.Cursor
	// 2. Stage: copy every file here, complete records only.
	stage := filepath.Join(env.StateDir, "staging", j.ID)
	raw, out := filepath.Join(stage, "raw"), filepath.Join(stage, "out")
	defer os.RemoveAll(stage)
	srcFS, err := src.Machine.FS(ctx)
	if err != nil {
		return "", none, none, err
	}
	step(fmt.Sprintf("copying %d file(s), %s", len(p.bundle.Files), Human(p.Bytes)))
	for _, f := range p.Files.Files {
		from := src.Machine.Path().Join(src.Install.Root(f.From.Root), fromSlash(src.Machine.Path(), f.From.Rel))
		n, sum, err := copyStable(srcFS, from, filepath.Join(raw, f.From.Root, filepath.FromSlash(f.From.Rel)), f.From.Growable)
		if err != nil {
			return "", none, none, fmt.Errorf("copy %s: %w", from, err)
		}
		res.Files++
		res.Bytes += n
		env.Audit.Write(audit.Entry{Action: "copy", Host: p.Source.Location, Session: p.Key.String(), Detail: map[string]any{"src": from, "bytes": n, "sha256": sum}})
	}

	// 3. Scan, then rewrite with the module's policy.
	pol := p.Files.Policy
	if p.Options.OtherAccount {
		san := tgt.Module.(agent.Sanitizer).Sanitize()
		pol.DropElems = append(pol.DropElems, san.DropElems...)
		pol.DropRecords = append(pol.DropRecords, san.DropRecords...)
	}
	var redact func([]byte) ([]byte, int)
	if p.Options.Redact {
		redact = scan.Redact
	}
	step("rewriting paths")
	staged := map[string]string{}
	for _, f := range p.Files.Files {
		in := filepath.Join(raw, f.From.Root, filepath.FromSlash(f.From.Rel))
		dst := filepath.Join(out, f.ToRoot, filepath.FromSlash(f.ToRel))
		staged[agent.StagedKey(f)] = dst
		if f.From.Rewrite != agent.RewriteNone {
			if fh, err := os.Open(in); err == nil {
				s, _ := scan.Reader(fh)
				fh.Close()
				res.Secrets.Merge(s)
			}
		}
		st, err := rewriteFile(in, dst, f, p.Placement.Mappings, pol, redact)
		if err != nil {
			return "", none, none, fmt.Errorf("rewrite %s: %w", f.From.Rel, err)
		}
		if f.From.Role == agent.RoleMain {
			res.Rewrite = st
		}
	}
	stagedHost, err := tgt.Machine.For(ctx, tgt.Module.Spec(), agent.Install{}, nil)
	if err != nil {
		return "", none, none, err
	}
	if err := tgt.Module.Verify(ctx, stagedHost, p.Files, staged, p.Placement); err != nil {
		return "", none, none, fmt.Errorf("verify: %w", err)
	}
	srcHead, tgtHead := heads(ctx, p, in, stagedHost, raw, staged)

	// 4. Install, keeping what it replaces.
	step("installing")
	for _, c := range p.SetAside {
		b, err := tgt.Module.Bundle(ctx, stagedHost, tgt.Install, c)
		if err != nil {
			continue
		}
		for _, f := range b.Files {
			pth := filepath.Join(tgt.Install.Root(f.Root), filepath.FromSlash(f.Rel))
			if err := j.SetAside(tgt.Machine.Name, pth); err != nil {
				return "", none, none, fmt.Errorf("set aside %s: %w", pth, err)
			}
			res.SetAside = append(res.SetAside, pth)
		}
		_ = j.SetAside(tgt.Machine.Name, lineage.PathFor(c.Path))
	}
	var mainDst string
	for _, f := range p.Files.Files {
		dst := filepath.Join(tgt.Install.Root(f.ToRoot), filepath.FromSlash(f.ToRel))
		if err := j.Place(tgt.Machine.Name, staged[agent.StagedKey(f)], dst, 0o600); err != nil {
			return "", none, none, err
		}
		if f.From.Role == agent.RoleMain {
			mainDst = dst
			now := time.Now()
			_ = os.Chtimes(dst, now, now) // fresh: agents clean up old sessions by date
		}
	}
	return mainDst, srcHead, tgtHead, nil
}

// recordLineage writes the session's lineage beside the new copy and beside the copy
// left behind.
func recordLineage(ctx context.Context, p *Plan, in Input, j *journal.Journal, mainDst string, src, tgt ir.Cursor, res *Result) {
	m := in.Lineage
	if m == nil {
		m = lineage.New(newID())
	}
	for _, c := range in.Copies {
		m.Merge(c.Lineage)
	}
	now := time.Now().UTC()
	from := m.Upsert(lineage.Replica{Key: p.Key, Location: p.Source.Location, AgentVersion: p.Source.Version, Head: src.Head, Offset: src.Offset, Time: now})
	to := m.Upsert(lineage.Replica{Key: p.Placement.Key, Location: p.Target.Location, AgentVersion: p.Target.Version, Head: tgt.Head, Offset: tgt.Offset, Time: now})
	m.Hops = append(m.Hops, lineage.Hop{Time: now, From: from, To: to, Kind: lineage.HopMove, Fork: p.Options.Fork && p.Live})
	body := m.Encode()
	if err := j.WriteFile(host.LocalFS(), p.Target.Location, lineage.PathFor(mainDst), body, 0o600); err != nil {
		res.Warnings = append(res.Warnings, "could not record the session's lineage here: "+err.Error())
	}
	if p.Source.Location == p.Target.Location {
		return
	}
	srcFS, err := in.Source.Machine.FS(ctx)
	if err == nil {
		err = j.WriteFile(srcFS, p.Source.Location, lineage.PathFor(in.Session.Path), body, 0o600)
	}
	if err != nil {
		res.Warnings = append(res.Warnings, "could not record the session's lineage on "+p.Source.Location+": "+err.Error())
	}
}

// markWith marks the copy left behind now, or records an owed mark when it is still open.
func markWith(ctx context.Context, p *Plan, in Input, j *journal.Journal, env Env, src ir.Cursor, mark agent.Mark, res *Result) {
	if m := in.Session.Mark; m != nil && *m == mark {
		res.Mark = "done" // already marked so
		return
	}
	switch p.Mark {
	case MarkNow:
		res.Mark = "done"
		if marker, ok := in.Source.Module.(agent.Marker); ok {
			h, err := in.Source.Machine.For(ctx, in.Source.Module.Spec(), in.Source.Install, j)
			if err == nil {
				err = marker.Mark(ctx, h, in.Source.Install, in.Session, mark)
			}
			if err != nil {
				res.Mark, res.MarkError = "failed", err.Error()
			}
		}
	case MarkWhenStopped:
		res.Mark = "pending"
		owed := lineage.Pending{Time: time.Now().UTC(), Location: p.Source.Location, Key: p.Key,
			Path: in.Session.Path, Title: p.Title, Mark: mark, Head: string(src.Head)}
		if in.Source.Machine.IsSnapshot() {
			res.Owed = &owed
		} else if err := lineage.AddPending(env.StateDir, owed); err != nil {
			res.Mark, res.MarkError = "failed", err.Error()
		}
	default:
		res.Mark = "off"
	}
	env.Audit.Write(audit.Entry{Action: "move.mark", Host: p.Source.Location, Session: p.Key.String(), Detail: map[string]any{"mark": res.Mark, "error": res.MarkError}})
}

// rewriteFile writes a staged file's rewritten form, then the module's appended records.
func rewriteFile(in, out string, f agent.PlacedFile, maps []agent.Mapping, pol agent.RewritePolicy, redact func([]byte) ([]byte, int)) (rewrite.Stats, error) {
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return rewrite.Stats{}, err
	}
	r, err := os.Open(in)
	if err != nil {
		return rewrite.Stats{}, err
	}
	defer r.Close()
	w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return rewrite.Stats{}, err
	}
	var st rewrite.Stats
	switch f.From.Rewrite {
	case agent.RewriteJSONL:
		st, err = rewrite.JSONL(r, w, rewrite.Options{Mappings: maps, Policy: pol, Redact: redact})
	case agent.RewriteText:
		_, err = rewrite.Text(r, w, maps)
	default:
		_, err = io.Copy(w, r)
	}
	for _, rec := range f.Append {
		if err != nil {
			break
		}
		_, err = w.Write(append(append([]byte(nil), rec...), '\n'))
	}
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return st, err
}

// copyStable copies a file here; a file still being appended to (an open session) is
// copied until two reads agree, else trimmed to its last complete line.
func copyStable(fsys host.FS, src, dst string, growable bool) (int64, string, error) {
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
		before, err := fsys.Stat(src)
		if err != nil {
			return 0, "", err
		}
		if n, sum, err = copyOnce(fsys, src, dst); err != nil {
			return 0, "", err
		}
		after, err := fsys.Stat(src)
		if err != nil {
			return 0, "", err
		}
		if before.Size() == after.Size() && n == after.Size() {
			return n, sum, nil
		}
	}
	return n, sum, trimToLastNewline(dst)
}

func copyOnce(fsys host.FS, src, dst string) (int64, string, error) {
	in, err := fsys.Open(src)
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

// fromSlash turns a bundle's slash path into the machine's form.
func fromSlash(pa agent.Path, rel string) string {
	if pa.Sep() == "/" {
		return rel
	}
	return strings.ReplaceAll(rel, "/", pa.Sep())
}

// Human formats a byte count.
func Human(n int64) string {
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
