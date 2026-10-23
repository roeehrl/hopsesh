package claude

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// cdFloor is the first Claude Code with /cd, whose effect on disk PlanChangeDir mirrors.
const cdFloor = "2.1.169"

// PlanChangeDir does what Claude Code's own /cd does on disk: the transcript and its side
// folder (subagents, tool results) move to the new folder's project folder, and a relocated
// record is appended. Every record already written stays as it is, so the session keeps
// its id, its signed thinking and its prompt cache; its old messages keep the folder they
// ran in, as with /cd.
func (m *Module) PlanChangeDir(_ context.Context, h agent.Host, in agent.Install, s agent.Summary, dir string) (agent.DirPlan, error) {
	pa, fsys := h.Path(), h.FS()
	p := agent.DirPlan{From: s.CWD, To: dir, Method: agent.DirRecord, Main: s.Path}
	root := in.Root(home)
	rel, ok := pa.Rel(root, s.Path)
	if !ok {
		return p, fmt.Errorf("%s is outside %s", s.Path, root)
	}
	rel = toSlash(rel)
	if !strings.HasPrefix(rel, "projects/") || strings.Count(rel, "/") != 2 {
		return p, fmt.Errorf("%s is not a session in a project folder", s.Path)
	}
	if in.Version != "" && !agent.HookVersionAtLeast(in.Version, cdFloor) {
		p.Blockers = append(p.Blockers, fmt.Sprintf("Claude Code %s has no /cd; update it to %s or later", in.Version, cdFloor))
	}
	if h.Facts().Env["CLAUDE_CODE_PROJECT_DIR_NAME"] != "" {
		p.Blockers = append(p.Blockers, "CLAUDE_CODE_PROJECT_DIR_NAME is set, so the new folder's project folder can't be known; use /cd in Claude Code")
	}
	if isClaudeWorktree(s.CWD) || isClaudeWorktree(dir) {
		p.Blockers = append(p.Blockers, "Claude Code manages this worktree itself; change the folder with /cd in Claude Code (or leave the worktree with ExitWorktree)")
	}
	sid := string(s.Key.Session)
	oldProject := pa.Dir(s.Path)
	newProject := pa.Join(root, "projects", Slug(dir))
	if oldProject != newProject {
		main := pa.Join(newProject, sid+".jsonl")
		if _, err := fsys.Stat(main); err == nil {
			p.Blockers = append(p.Blockers, fmt.Sprintf("%s already has a session with this id", newProject))
		} else if !errors.Is(err, fs.ErrNotExist) {
			return p, err
		}
		p.Main = main
		p.Moves = append(p.Moves, agent.FileMove{From: s.Path, To: main})
		side := pa.Join(oldProject, sid)
		walk(fsys, pa, side, sid, func(r string, _ int64) {
			sub := strings.TrimPrefix(r, sid+"/")
			p.Moves = append(p.Moves, agent.FileMove{From: pa.Join(side, sub), To: pa.Join(newProject, sid, sub)})
		})
	}
	p.Records = append(p.Records, "a relocated record (as /cd writes)")
	p.Notes = append(p.Notes, "Claude Code loads "+dir+"'s CLAUDE.md, settings and MCP servers when the session resumes, and asks to trust the folder if it is new to it")
	return p, nil
}

// ChangeDir moves the files and appends the relocated record, through h's journaled FS.
func (m *Module) ChangeDir(_ context.Context, h agent.Host, _ agent.Install, s agent.Summary, p agent.DirPlan) error {
	if len(p.Blockers) > 0 {
		return errors.New(p.Blockers[0])
	}
	for _, mv := range p.Moves {
		if err := h.FS().Rename(mv.From, mv.To); err != nil {
			return err
		}
	}
	rec := encodeRecord(map[string]any{"type": "relocated", "sessionId": string(s.Key.Session), "relocatedCwd": p.To})
	return h.FS().Append(p.Main, append(rec, '\n'), agent.AppendOptions{NewLine: true})
}

// InSession is Claude Code's own /cd, which keeps the session.
func (m *Module) InSession(_ agent.Install, dir string) string { return "/cd " + dir }

// isClaudeWorktree reports a folder inside a Claude-managed worktree (<repo>/.claude/worktrees/<name>).
func isClaudeWorktree(dir string) bool {
	return strings.Contains(path.Clean(toSlash(dir))+"/", "/.claude/worktrees/")
}
