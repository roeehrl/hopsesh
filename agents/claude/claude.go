// Package claude is the Claude Code module: sessions are JSON-lines transcripts under
// <config>/projects/<slug of the working directory>/<id>.jsonl, with side folders for
// subagents and spilled tool output, and file backups under <config>/file-history/<id>.
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

const id agent.ID = "claude"

const (
	home         = "home"              // the config folder root
	worktreesDir = ".claude/worktrees" // where Claude Code creates worktrees in a repo
)

// Module is the Claude Code module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

var (
	_ agent.Module        = (*Module)(nil)
	_ agent.LiveDetector  = (*Module)(nil)
	_ agent.Stopper       = (*Module)(nil)
	_ agent.Marker        = (*Module)(nil)
	_ agent.AccountProber = (*Module)(nil)
	_ agent.Sanitizer     = (*Module)(nil)
	_ agent.Notifier      = (*Module)(nil)
	_ agent.Integrator    = (*Module)(nil)
)

// Spec declares Claude Code.
func (*Module) Spec() agent.Spec {
	return agent.Spec{
		ID:        id,
		Name:      "Claude Code",
		Vendor:    "Anthropic",
		Stability: agent.Stable,
		Tested:    []string{"2.1"},
		Binaries: []agent.Binary{{
			Name: "claude",
			Candidates: map[string][]string{
				"*":       {"~/.local/bin/claude", "~/.claude/local/claude", "/opt/homebrew/bin/claude", "/usr/local/bin/claude"},
				"windows": {"~/.local/bin/claude.exe", "~/AppData/Roaming/npm/claude.cmd"},
			},
			VersionArgs: []string{"--version"},
		}},
		Roots:        []agent.Root{{Name: home, Env: []string{"CLAUDE_CONFIG_DIR"}, Default: map[string]string{"*": "~/.claude"}}},
		LoginEnv:     []string{"CLAUDE_CONFIG_DIR"},
		Secrets:      []string{"{home}/.credentials.json", "{home}/sessions/*.key"},
		Worktrees:    []string{worktreesDir},
		Instructions: []string{"CLAUDE.md", "AGENTS.md"},
		Tools:        "Bash, Read, Edit, Write, Grep, Glob, TodoWrite",
		Features:     []agent.Capability{agent.CapFork, agent.CapRemoteControl, agent.CapApp},
	}
}

// Detect resolves the config folder and the claude binary.
func (m *Module) Detect(_ context.Context, h agent.Host) (agent.Install, error) {
	return agent.DefaultInstall(m.Spec(), h), nil
}

// List summarises every transcript under <config>/projects, newest first. Subagent-only
// and bookkeeping-only transcripts are left out, as Claude Code's own picker does.
func (m *Module) List(_ context.Context, h agent.Host, in agent.Install) (agent.Listing, error) {
	pa, fsys := h.Path(), h.FS()
	projects := pa.Join(in.Root(home), "projects")
	dirs, err := fsys.ReadDir(projects)
	if err != nil {
		if isNotExist(err) {
			return agent.Listing{}, nil
		}
		return agent.Listing{}, err
	}
	type job struct {
		file    string
		info    fs.FileInfo
		sidecar bool
	}
	const workers = 16 // a remote filesystem answers in parallel
	var (
		mu   sync.Mutex
		jobs []job
		wg   sync.WaitGroup
		sem  = make(chan struct{}, workers)
	)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := pa.Join(projects, d.Name())
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			entries, err := fsys.ReadDir(dir)
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
				local = append(local, job{file: pa.Join(dir, n), info: e, sidecar: subdirs[strings.TrimSuffix(n, ".jsonl")]})
			}
			mu.Lock()
			jobs = append(jobs, local...)
			mu.Unlock()
		}()
	}
	wg.Wait()

	infos := make([]*info, len(jobs))
	errs := make([]error, len(jobs))
	next := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				j := jobs[i]
				side := j.sidecar
				infos[i], errs[i] = summarize(fsys, pa, j.file, j.info, &side)
			}
		}()
	}
	for i := range jobs {
		next <- i
	}
	close(next)
	wg.Wait()

	var out agent.Listing
	for i, s := range infos {
		if errs[i] != nil {
			out.Errors = append(out.Errors, agent.SessionError{Path: jobs[i].file, Err: errs[i]})
			continue
		}
		if s.IsSidechain || !s.HasMessages {
			continue
		}
		out.Sessions = append(out.Sessions, s.summary())
	}
	sort.Slice(out.Sessions, func(i, j int) bool { return out.Sessions[i].LastActivity.After(out.Sessions[j].LastActivity) })
	return out, nil
}

func (s *info) summary() agent.Summary {
	return agent.Summary{
		Key:          agent.SessionKey{Agent: id, Session: agent.SessionID(s.ID)},
		Title:        s.Title,
		TitleSource:  titleSource(s.TitleSource),
		CWD:          s.CWD,
		LastPrompt:   s.LastPrompt,
		LastActivity: s.LastActivity,
		Size:         s.Size,
		GitBranch:    s.GitBranch,
		AgentVersion: s.Version,
		WorktreeRoot: s.WorktreeRoot,
		Subagents:    s.Subagents,
		Mark:         s.Mark,
		Path:         s.File,
	}
}

func titleSource(s string) string {
	switch s {
	case "custom":
		return "custom"
	case "ai", "summary":
		return "generated"
	case "prompt":
		return "prompt"
	}
	return ""
}

// Bundle is the transcript, its side folder and its file-history backups.
func (m *Module) Bundle(_ context.Context, h agent.Host, in agent.Install, s agent.Summary) (agent.Bundle, error) {
	pa, fsys := h.Path(), h.FS()
	root := in.Root(home)
	rel, ok := pa.Rel(root, s.Path)
	if !ok {
		return agent.Bundle{}, fmt.Errorf("%s is outside %s", s.Path, root)
	}
	fi, err := fsys.Stat(s.Path)
	if err != nil {
		return agent.Bundle{}, err
	}
	rel = toSlash(rel)
	b := agent.Bundle{Files: []agent.BundleFile{{Root: home, Rel: rel, Size: fi.Size(), Role: agent.RoleMain, Rewrite: agent.RewriteJSONL, Growable: true}}}
	sid := string(s.Key.Session)
	walk(fsys, pa, pa.Join(pa.Dir(s.Path), sid), path.Join(path.Dir(rel), sid), func(r string, size int64) {
		f := agent.BundleFile{Root: home, Rel: r, Size: size, Role: agent.RoleSide}
		switch {
		case strings.Contains(r, "/subagents/") && strings.HasSuffix(r, ".jsonl"):
			f.Rewrite = agent.RewriteJSONL
		case strings.Contains(r, "/tool-results/"):
			f.Rewrite = agent.RewriteText
		case strings.HasSuffix(r, ".json"):
			f.Rewrite = agent.RewriteJSONL
		default:
			f.Rewrite = agent.RewriteNone
		}
		b.Files = append(b.Files, f)
	})
	walk(fsys, pa, pa.Join(root, "file-history", sid), "file-history/"+sid, func(r string, size int64) {
		// backups of file contents: never altered
		b.Files = append(b.Files, agent.BundleFile{Root: home, Rel: r, Size: size, Role: agent.RoleSide, Rewrite: agent.RewriteNone})
	})
	return b, nil
}

// walk lists regular files under dir, giving each a slash path rooted at relRoot.
func walk(fsys agent.FS, pa agent.Path, dir, relRoot string, emit func(rel string, size int64)) {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		rel := path.Join(relRoot, e.Name())
		if e.IsDir() {
			walk(fsys, pa, pa.Join(dir, e.Name()), rel, emit)
			continue
		}
		if e.Mode().IsRegular() {
			emit(rel, e.Size())
		}
	}
}

// PlanMove puts the transcript in the target working directory's project folder, renames
// the session for keep-both copies, and appends the records Claude Code itself writes when
// a session changes folder (relocated) or is renamed (custom-title).
func (m *Module) PlanMove(src, dst agent.Install, s agent.Summary, b agent.Bundle, p agent.Placement) (agent.MovePlan, error) {
	oldID, newID := string(p.SourceID), string(p.Key.Session)
	main, ok := b.Main()
	if !ok {
		return agent.MovePlan{}, fmt.Errorf("%s has no transcript", s.Key)
	}
	srcProject := path.Dir(main.Rel) // projects/<slug>
	dstProject := "projects/" + Slug(p.CWD)
	if p.CWD == s.CWD && sameRoots(src, dst) {
		dstProject = srcProject // an identity move keeps the folder (it may be a worktree's repo root)
	}
	mp := agent.MovePlan{Policy: policy()}
	if oldID != newID {
		mp.Policy.Rename = [2]string{oldID, newID}
		mp.Policy.RenameKeys = []string{"sessionId"}
	}
	for _, f := range b.Files {
		to := f.Rel
		switch {
		case f.Role == agent.RoleMain:
			to = dstProject + "/" + newID + ".jsonl"
		case strings.HasPrefix(f.Rel, srcProject+"/"+oldID+"/"):
			to = dstProject + "/" + newID + "/" + strings.TrimPrefix(f.Rel, srcProject+"/"+oldID+"/")
		case strings.HasPrefix(f.Rel, "file-history/"+oldID+"/"):
			to = "file-history/" + newID + "/" + strings.TrimPrefix(f.Rel, "file-history/"+oldID+"/")
		}
		pf := agent.PlacedFile{From: f, ToRoot: home, ToRel: to}
		if f.Role == agent.RoleMain {
			if p.CWD != s.CWD {
				pf.Append = append(pf.Append, encodeRecord(map[string]any{"type": "relocated", "sessionId": newID, "relocatedCwd": p.CWD}))
			}
			if p.Title != "" {
				pf.Append = append(pf.Append, encodeRecord(map[string]any{"type": "custom-title", "customTitle": p.Title, "sessionId": newID}))
			}
		}
		mp.Files = append(mp.Files, pf)
	}
	return mp, nil
}

func sameRoots(a, b agent.Install) bool { return a.Root(home) == b.Root(home) }

// policy protects signed thinking, opaque data and record identifiers, drops Remote
// Control bridge records (a copy must not reattach to the source's link) and any
// "moved"/"continued" marks the copy carried.
func policy() agent.RewritePolicy {
	return agent.RewritePolicy{
		Protect: []string{"thinking", "signature", "data", "uuid", "parentUuid", "sessionId", "leafUuid",
			"messageId", "promptId", "requestId", "logicalParentUuid"},
		DropRecords: []agent.FieldMatch{
			{Field: "type", Values: []string{"bridge-session"}},
			{Field: "customTitle", Prefixes: agent.MarkPrefixes()},
		},
	}
}

// Sanitize removes thinking blocks for a machine signed in to another account: their
// signatures are bound to the original account, and removing only the signature fails.
func (m *Module) Sanitize() agent.RewritePolicy {
	return agent.RewritePolicy{DropElems: []agent.ElemMatch{{Array: "message.content", Field: "type", Values: []string{"thinking", "redacted_thinking"}}}}
}

// Verify reads the staged transcript back: it must open in the target folder.
func (m *Module) Verify(_ context.Context, h agent.Host, mp agent.MovePlan, staged map[string]string, p agent.Placement) error {
	for _, f := range mp.Files {
		if f.From.Role != agent.RoleMain {
			continue
		}
		local := staged[agent.StagedKey(f)]
		s, err := summarize(h.FS(), h.Path(), local, nil, new(bool))
		if err != nil {
			return err
		}
		if s.CWD != p.CWD {
			return fmt.Errorf("the staged session points at %q, expected %q", s.CWD, p.CWD)
		}
		return nil
	}
	return fmt.Errorf("no transcript was staged")
}

// Resume is `claude [--desktop] --resume <id> [--fork-session] [--remote-control [name]]`.
func (m *Module) Resume(in agent.Install, key agent.SessionKey, p agent.Placement, o agent.ResumeOptions) agent.Command {
	argv := []string{"claude"}
	if o.App {
		argv = append(argv, "--desktop")
	}
	argv = append(argv, "--resume", string(key.Session))
	if o.Fork {
		argv = append(argv, "--fork-session")
	}
	if o.RemoteControl {
		argv = append(argv, "--remote-control")
		if o.Name != "" {
			argv = append(argv, o.Name)
		}
	}
	if o.Prompt != "" {
		argv = append(argv, o.Prompt)
	}
	return agent.Command{Argv: argv, Dir: p.CWD}
}

// NotifyInstruction asks the resumed session to message the old one through Remote
// Control's cross-session messaging.
func (m *Module) NotifyInstruction(oldName, oldLocation, newName string, fork bool) string {
	s := fmt.Sprintf("Also use ListAgents and SendMessage to tell the session %q on %s that this work now continues here", oldName, oldLocation)
	if newName != "" {
		s += fmt.Sprintf(" as %q", newName)
	}
	if fork {
		return s + ", so you can coordinate."
	}
	return s + ", and ask it to stop working on this task and not edit these files."
}

// Mark retitles the copy left behind with a custom-title record (what /rename writes), so
// Claude Code's own resume list shows where it went. The file's time is kept.
func (m *Module) Mark(_ context.Context, h agent.Host, in agent.Install, s agent.Summary, mk agent.Mark) error {
	rec := encodeRecord(map[string]any{"type": "custom-title", "customTitle": agent.MarkTitle(mk, s.Title), "sessionId": string(s.Key.Session)})
	return h.FS().Append(s.Path, append(rec, '\n'), agent.AppendOptions{NewLine: true, KeepMtime: true})
}

// record encodes a transcript record with its keys in a fixed order (type first), like
// Claude Code writes them.
func encodeRecord(fields map[string]any) []byte {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if k != "type" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	keys = append([]string{"type"}, keys...)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, _ := marshalNoEscape(fields[k])
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return []byte(b.String())
}

func toSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

func isNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }
