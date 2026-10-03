package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// side is one machine of a scenario: here (in-process) or there (the helper over ssh).
type side interface {
	label() string
	do(op string, in, out any) error
}

type localSide struct{}

func (localSide) label() string { return "here" }

func (localSide) do(op string, in, out any) error {
	b, _ := json.Marshal(in)
	var res any
	var err error
	switch op {
	case "seed":
		var r SeedReq
		_ = json.Unmarshal(b, &r)
		res, err = seed(r)
	case "find":
		var r FindReq
		_ = json.Unmarshal(b, &r)
		res, err = find(r)
	case "append":
		var r AppendReq
		_ = json.Unmarshal(b, &r)
		res, err = map[string]any{}, appendTurn(r)
	case "head":
		var r HeadReq
		_ = json.Unmarshal(b, &r)
		var h string
		h, err = git(r.Dir, nil, "rev-parse", "HEAD")
		res = map[string]string{"head": h}
	case "base":
		res = map[string]string{"base": base(), "home": homeDir()}
	default:
		return fmt.Errorf("unknown op %s", op)
	}
	if err != nil {
		return err
	}
	b, _ = json.Marshal(res)
	return json.Unmarshal(b, out)
}

type remoteSide struct {
	dest   string // ssh destination
	helper string // the helper's command there
	log    *rowLog
}

func (remoteSide) label() string { return "there" }

func (s remoteSide) do(op string, in, out any) error {
	b, _ := json.Marshal(in)
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-o", "LogLevel=ERROR", s.dest, s.helper+" agent "+op)
	cmd.Stdin = bytes.NewReader(b)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if s.log != nil {
		s.log.printf("there$ hsmatrix agent %s %s\n%s%s", op, b, stdout.String(), stderr.String())
	}
	if err != nil {
		return fmt.Errorf("helper %s on %s: %v: %s", op, s.dest, err, strings.TrimSpace(stderr.String()))
	}
	return json.Unmarshal(stdout.Bytes(), out)
}

// runner holds what every row shares.
type runner struct {
	hopsesh string // the hopsesh program here
	here    localSide
	there   remoteSide
	hosts   map[string]string // naming → host name in hopsesh (box, boxa)
	out     string
	log     *rowLog
}

type rowLog struct{ b strings.Builder }

func (l *rowLog) printf(f string, a ...any) { fmt.Fprintf(&l.b, f, a...) }

// hs runs hopsesh here and returns its output; want says whether it must succeed.
func (r *runner) hs(want bool, args ...string) (string, error) {
	cmd := exec.Command(r.hopsesh, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	r.log.printf("here$ hopsesh %s\n%s\n", strings.Join(args, " "), out.String())
	if want && err != nil {
		return out.String(), fmt.Errorf("hopsesh %s failed: %v\n%s", strings.Join(args, " "), err, tail(out.String(), 1200))
	}
	if !want && err == nil {
		return out.String(), fmt.Errorf("hopsesh %s should have been refused", strings.Join(args, " "))
	}
	return out.String(), nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

var contents = map[string]string{
	"ascii": "fix the flaky parser test",
	"zh":    "修复解析器里不稳定的测试",
	"ja":    "パーサーの不安定なテストを直して",
	"ar":    "أصلح اختبار المحلل غير المستقر",
	"el":    "διόρθωσε το ασταθές τεστ του αναλυτή",
	"large": "summarize the long investigation",
}

// result is one row's outcome.
type result struct {
	Row     Row     `json:"row"`
	OK      bool    `json:"ok"`
	Error   string  `json:"error,omitempty"`
	Seconds float64 `json:"seconds"`
}

func (r *runner) run(row Row) (res result) {
	start := time.Now()
	r.log = &rowLog{}
	r.there.log = r.log
	r.log.printf("== %s\n", row)
	defer func() {
		res.Row, res.Seconds = row, time.Since(start).Seconds()
		if p := recover(); p != nil {
			res.Error = fmt.Sprint(p)
		}
		res.OK = res.Error == ""
		name := fmt.Sprintf("row-%03d-%s.log", row.N, map[bool]string{true: "ok", false: "FAIL"}[res.OK])
		_ = os.WriteFile(filepath.Join(r.out, name), []byte(r.log.b.String()+"\n"+res.Error+"\n"), 0o644)
	}()
	if err := r.scenario(row); err != nil {
		res.Error = err.Error()
	}
	return
}

// sc is one row's state.
type sc struct {
	row                 Row
	marker, text, title string
	id                  string
	src, dst            side
	srcCwd, dstCwd      string
	srcFile             string
	host                string
}

func (r *runner) scenario(row Row) error {
	if row.Op == "skill" {
		return r.skill()
	}
	s := &sc{row: row, id: newID(), host: r.hosts[row.Naming]}
	s.marker = fmt.Sprintf("hsm%03dx%s", row.N, s.id[:6])
	s.text = contents[row.Content] + " " + s.marker
	s.title = fmt.Sprintf("matrix row %d", row.N)
	if row.Op == "push" {
		s.src, s.dst = r.here, r.there
	} else {
		s.src, s.dst = r.there, r.here
	}
	repo := fmt.Sprintf("r%03d", row.N)
	var sr SeedRes
	if err := s.src.do("seed", SeedReq{Agent: row.From, ID: s.id, Title: s.title, Text: s.text, Large: row.Content == "large",
		Repo: repo, State: row.Repo, Session: true}, &sr); err != nil {
		return fmt.Errorf("seeding the source: %w", err)
	}
	s.srcCwd, s.srcFile = sr.Cwd, sr.File
	// The target has the repository too (cloned before), or, without one, a folder to use.
	tState := "clean"
	if row.Repo == "none" {
		tState = "none"
	}
	var tr SeedRes
	if err := s.dst.do("seed", SeedReq{ID: s.id, Repo: repo, State: tState}, &tr); err != nil {
		return fmt.Errorf("seeding the target: %w", err)
	}
	s.dstCwd = tr.Cwd
	switch row.Op {
	case "move", "continue":
		return r.pullAndUndo(s)
	case "push":
		return r.push(s)
	case "roundtrip":
		return r.roundtrip(s)
	case "conflict":
		return r.conflict(s)
	case "undo-used":
		return r.undoUsed(s)
	}
	return fmt.Errorf("unknown op %s", row.Op)
}

func (s *sc) ref() string { return s.host + ":" + s.row.From + "/" + s.id }

func (s *sc) pullArgs(extra ...string) []string {
	args := []string{"pull", s.ref(), "--yes", "--json"}
	if s.row.To != s.row.From {
		args = append(args, "--in", s.row.To)
	}
	if s.row.Repo == "none" {
		args = append(args, "--to", s.dstCwd)
	}
	return append(args, extra...)
}

// arrived checks the target has the conversation, with the target's paths.
func (r *runner) arrived(s *sc, on side, agent, cwd, otherCwd string) (Found, error) {
	var fs []Found
	if err := on.do("find", FindReq{Marker: s.marker, Needles: []string{s.text, cwd, otherCwd}}, &fs); err != nil {
		return Found{}, err
	}
	var got []Found
	for _, f := range fs {
		if f.Agent == agent && f.Mark == "" {
			got = append(got, f)
		}
	}
	if len(got) != 1 {
		return Found{}, fmt.Errorf("%s: want one %s session with the conversation, found %d (%+v)", on.label(), agent, len(got), fs)
	}
	f := got[0]
	if !f.Has[s.text] {
		return f, fmt.Errorf("%s: the conversation's text did not arrive intact in %s", on.label(), f.Path)
	}
	if !f.Has[cwd] {
		return f, fmt.Errorf("%s: %s does not name its folder %s", on.label(), f.Path, cwd)
	}
	if cwd != otherCwd && f.Has[otherCwd] {
		return f, fmt.Errorf("%s: %s still names the other machine's folder %s", on.label(), f.Path, otherCwd)
	}
	// Continuing in the other agent summarizes the oldest steps of a long conversation to
	// fit the target's window; only a move keeps all of it.
	if s.row.Content == "large" && agent == s.row.From && f.Bytes < 1<<20 {
		return f, fmt.Errorf("%s: the large conversation arrived with %d bytes", on.label(), f.Bytes)
	}
	return f, nil
}

// marked checks the source copy's mark ("" checks it has none).
func (r *runner) marked(s *sc, on side, prefix string) error {
	var fs []Found
	if err := on.do("find", FindReq{Marker: s.marker}, &fs); err != nil {
		return err
	}
	for _, f := range fs {
		if f.ID == s.id && f.Agent == s.row.From && f.Path == s.srcFile {
			if prefix == "" && f.Mark != "" {
				return fmt.Errorf("%s: the source copy is still marked %q", on.label(), f.Mark)
			}
			if prefix != "" && !strings.HasPrefix(f.Mark, prefix) {
				return fmt.Errorf("%s: the source copy's mark is %q, want %q…", on.label(), f.Mark, prefix)
			}
			return nil
		}
	}
	return fmt.Errorf("%s: the source copy %s is gone", on.label(), s.srcFile)
}

func (r *runner) gone(s *sc, on side, agent string) error {
	var fs []Found
	if err := on.do("find", FindReq{Marker: s.marker}, &fs); err != nil {
		return err
	}
	for _, f := range fs {
		if f.Agent == agent && f.Path != s.srcFile {
			return fmt.Errorf("%s: %s is still there after undo", on.label(), f.Path)
		}
	}
	return nil
}

func (r *runner) codeCame(s *sc) error {
	if s.row.Repo != "unpushed" {
		return nil
	}
	var a, b map[string]string
	if err := s.src.do("head", HeadReq{Dir: s.srcCwd}, &a); err != nil {
		return err
	}
	if err := s.dst.do("head", HeadReq{Dir: s.dstCwd}, &b); err != nil {
		return err
	}
	if a["head"] != b["head"] {
		return fmt.Errorf("the unpushed commit did not come along: source %s, target %s", a["head"], b["head"])
	}
	return nil
}

func (r *runner) markPrefix(s *sc) string {
	if s.row.To != s.row.From {
		return "↪ continued in "
	}
	return "↪ moved to "
}

func (r *runner) pullAndUndo(s *sc) error {
	if _, err := r.hs(true, s.pullArgs()...); err != nil {
		return err
	}
	if _, err := r.arrived(s, r.here, s.row.To, s.dstCwd, s.srcCwd); err != nil {
		return err
	}
	if err := r.codeCame(s); err != nil {
		return err
	}
	if err := r.marked(s, r.there, r.markPrefix(s)); err != nil {
		return err
	}
	if _, err := r.hs(true, "undo", "--yes"); err != nil {
		return err
	}
	if err := r.gone(s, r.here, s.row.To); err != nil {
		return err
	}
	return r.marked(s, r.there, "")
}

func (r *runner) push(s *sc) error {
	ref := s.row.From + "/" + s.id
	args := []string{"push", ref, s.host, "--yes", "--json"}
	if s.row.To != s.row.From {
		args = append(args, "--in", s.row.To)
	}
	args = append(args, "--to", s.dstCwd)
	if _, err := r.hs(true, args...); err != nil {
		return err
	}
	if _, err := r.arrived(s, r.there, s.row.To, s.dstCwd, s.srcCwd); err != nil {
		return err
	}
	if err := r.marked(s, r.here, r.markPrefix(s)); err != nil {
		return err
	}
	if _, err := r.hs(true, "undo", "--yes"); err != nil {
		return err
	}
	if err := r.gone(s, r.there, s.row.To); err != nil {
		return err
	}
	return r.marked(s, r.here, "")
}

// roundtrip: there → here, a turn here, then back there by push; there's copy has the turn.
func (r *runner) roundtrip(s *sc) error {
	if _, err := r.hs(true, s.pullArgs()...); err != nil {
		return err
	}
	f, err := r.arrived(s, r.here, s.row.To, s.dstCwd, s.srcCwd)
	if err != nil {
		return err
	}
	back := "and back again " + s.marker
	if err := r.here.do("append", AppendReq{Agent: s.row.To, Path: f.Path, ID: s.id, Text: back}, &struct{}{}); err != nil {
		return err
	}
	args := []string{"push", s.row.To + "/" + s.id, s.host, "--yes", "--json"}
	if s.row.Repo == "none" {
		args = append(args, "--to", s.srcCwd)
	}
	if _, err := r.hs(true, args...); err != nil {
		return err
	}
	var fs []Found
	if err := r.there.do("find", FindReq{Marker: s.marker, Needles: []string{back, s.srcCwd}}, &fs); err != nil {
		return err
	}
	for _, g := range fs {
		if g.Agent == s.row.From && g.Mark == "" && g.Has[back] {
			if !g.Has[s.srcCwd] {
				return fmt.Errorf("there: the copy that came home names the wrong folder (%s)", g.Path)
			}
			return nil
		}
	}
	return fmt.Errorf("there: no unmarked copy with the turn added here (%+v)", fs)
}

// conflict: both copies change after a move; a second move is refused until --keep-both.
func (r *runner) conflict(s *sc) error {
	if _, err := r.hs(true, s.pullArgs()...); err != nil {
		return err
	}
	f, err := r.arrived(s, r.here, s.row.To, s.dstCwd, s.srcCwd)
	if err != nil {
		return err
	}
	if err := r.here.do("append", AppendReq{Agent: s.row.To, Path: f.Path, ID: s.id, Text: "here " + s.marker}, &struct{}{}); err != nil {
		return err
	}
	if err := r.there.do("append", AppendReq{Agent: s.row.From, Path: s.srcFile, ID: s.id, Text: "there " + s.marker}, &struct{}{}); err != nil {
		return err
	}
	out, err := r.hs(false, s.pullArgs()...)
	if err != nil {
		return err
	}
	if !strings.Contains(out, "keep-both") {
		return fmt.Errorf("the refusal does not offer --keep-both:\n%s", tail(out, 800))
	}
	if _, err := r.hs(true, s.pullArgs("--keep-both")...); err != nil {
		return err
	}
	var fs []Found
	if err := r.here.do("find", FindReq{Marker: s.marker}, &fs); err != nil {
		return err
	}
	n := 0
	for _, g := range fs {
		if g.Agent == s.row.To {
			n++
		}
	}
	if n != 2 {
		return fmt.Errorf("here: want both copies after --keep-both, found %d", n)
	}
	return nil
}

// undoUsed: a moved session used here is not undone without --force.
func (r *runner) undoUsed(s *sc) error {
	if _, err := r.hs(true, s.pullArgs()...); err != nil {
		return err
	}
	f, err := r.arrived(s, r.here, s.row.To, s.dstCwd, s.srcCwd)
	if err != nil {
		return err
	}
	if err := r.here.do("append", AppendReq{Agent: s.row.To, Path: f.Path, ID: s.id, Text: "kept working " + s.marker}, &struct{}{}); err != nil {
		return err
	}
	out, err := r.hs(false, "undo", "--yes")
	if err != nil {
		return err
	}
	if !strings.Contains(out, "changed since") {
		return fmt.Errorf("the refusal does not say the session changed since:\n%s", tail(out, 800))
	}
	if _, err := r.hs(true, "undo", "--yes", "--force"); err != nil {
		return err
	}
	if err := r.gone(s, r.here, s.row.To); err != nil {
		return err
	}
	return r.marked(s, r.there, "")
}

// skill: install the hopsesh skill in every agent here, then remove it.
func (r *runner) skill() error {
	if _, err := r.hs(true, "skill", "install"); err != nil {
		return err
	}
	for _, p := range []string{filepath.Join(claudeDir(), "skills", "hopsesh", "SKILL.md")} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("the skill is not at %s", p)
		}
	}
	if _, err := r.hs(true, "skill", "remove"); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(claudeDir(), "skills", "hopsesh")); !errors.Is(err, os.ErrNotExist) {
		return errors.New("the skill is still there after remove")
	}
	return nil
}
