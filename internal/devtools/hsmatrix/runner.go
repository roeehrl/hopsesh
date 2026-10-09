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

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
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
	case "remove":
		var r RemoveReq
		_ = json.Unmarshal(b, &r)
		res, err = map[string]any{}, removeSession(r)
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
	Status  string  `json:"status"`
	OK      bool    `json:"ok"`
	Error   string  `json:"error,omitempty"`
	Reason  string  `json:"reason,omitempty"`
	Seconds float64 `json:"seconds"`
}

type unsupportedScenario string

func (e unsupportedScenario) Error() string { return string(e) }

func rowOutcome(err error) result {
	if err == nil {
		return result{Status: "passed", OK: true}
	}
	var unsupported unsupportedScenario
	if errors.As(err, &unsupported) {
		return result{Status: "unsupported", Reason: string(unsupported)}
	}
	return result{Status: "failed", Error: err.Error()}
}

func (r *runner) run(row Row) (res result) {
	start := time.Now()
	r.log = &rowLog{}
	r.there.log = r.log
	r.log.printf("== %s\n", row)
	defer func() {
		if p := recover(); p != nil {
			res = rowOutcome(fmt.Errorf("scenario panic: %v", p))
		}
		res.Row, res.Seconds = row, time.Since(start).Seconds()
		name := fmt.Sprintf("row-%03d-%s.log", row.N, res.Status)
		_ = os.WriteFile(filepath.Join(r.out, name), []byte(r.log.b.String()+"\n"+res.Error+"\n"+res.Reason+"\n"), 0o644)
	}()
	res = rowOutcome(r.scenario(row))
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
	switch row.Op {
	case "fetch":
		return r.fetch(row)
	case "handoff", "cloud-roundtrip":
		return r.handoff(row)
	case "cloud-hop":
		return r.cloudHop(row)
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
	case "move", "continue", "bounded", "fork":
		return r.pullAndUndo(s)
	case "push":
		return r.push(s)
	case "roundtrip", "quiet-roundtrip":
		return r.roundtrip(s)
	case "repeat-roundtrip", "fork-roundtrip":
		return r.repeatRoundtrip(s)
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
	if s.row.Op == "fork" {
		args = append(args, "--fork")
	}
	if s.row.Op == "quiet-roundtrip" {
		args = append(args, "--notify=false")
	}
	if s.row.Op == "bounded" {
		args = append(args, "--bounded")
	}
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
	const ready = "[hopsesh] Import ready."
	const fakeReply = "Understood. I'll check the working tree first, then continue from where the conversation stopped."
	if err := on.do("find", FindReq{Marker: s.marker, Needles: []string{s.text, cwd, otherCwd, ready, fakeReply}}, &fs); err != nil {
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
	if s.row.To != s.row.From && (!f.Has[ready] || f.Has[fakeReply]) {
		return f, fmt.Errorf("%s: imported history must identify Hopsesh's notice without claiming an agent turn in %s", on.label(), f.Path)
	}
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
	if s.row.Content == "large" && f.Bytes == 0 {
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

// cloudTitles are the clouds' names in a mark ("continued in Claude Code cloud").
var cloudTitles = map[string]string{"claude-cloud": "Claude Code cloud", "codex-cloud": "Codex cloud"}

func (r *runner) markPrefix(s *sc) string {
	if s.row.Op == "fork" {
		return ""
	}
	// The CLI scans fixture profiles as separate account scopes, so even a
	// same-agent transfer uses portable preparation into the other profile.
	return "↪ prepared in "
}

func (r *runner) pullAndUndo(s *sc) error {
	if _, err := r.hs(true, s.pullArgs()...); err != nil {
		return err
	}
	f, err := r.arrived(s, r.here, s.row.To, s.dstCwd, s.srcCwd)
	if err != nil {
		return err
	}
	if err := checkMovement(f, 1, true, s.row.Op == "fork"); err != nil {
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
	if err := checkMovement(f, 1, s.row.Op != "quiet-roundtrip", false); err != nil {
		return err
	}
	back := "and back again " + s.marker
	if err := r.here.do("append", AppendReq{Agent: s.row.To, Path: f.Path, ID: f.ID, Text: back}, &struct{}{}); err != nil {
		return err
	}
	args := []string{"push", s.row.To + "/" + f.ID, s.host, "--yes", "--json"}
	if s.row.Op == "quiet-roundtrip" {
		args = append(args, "--notify=false")
	}
	if s.row.To != s.row.From {
		args = append(args, "--in", s.row.From)
	}
	// A return has an exact original repository, including unpushed/worktree
	// cases. Do not make the test depend on incidental repository discovery.
	args = append(args, "--to", s.srcCwd)
	if _, err := r.hs(true, args...); err != nil {
		return err
	}
	var fs []Found
	if err := r.there.do("find", FindReq{Marker: s.marker, Needles: []string{back, s.srcCwd}}, &fs); err != nil {
		return err
	}
	for _, g := range fs {
		if g.Agent == s.row.From && g.Mark == "" && g.Has[back] {
			if err := checkMovement(g, 2, s.row.Op != "quiet-roundtrip", false); err != nil {
				return err
			}
			if !g.Has[s.srcCwd] {
				return fmt.Errorf("there: the copy that came home names the wrong folder (%s)", g.Path)
			}
			if s.row.Op == "roundtrip" {
				return r.newSessionAfterMissingOriginal(s, f, g, back)
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
	if err := r.here.do("append", AppendReq{Agent: s.row.To, Path: f.Path, ID: f.ID, Text: "here " + s.marker}, &struct{}{}); err != nil {
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
	if err := r.here.do("append", AppendReq{Agent: s.row.To, Path: f.Path, ID: f.ID, Text: "kept working " + s.marker}, &struct{}{}); err != nil {
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

// fetch brings a Claude Code cloud session here. A stand-in cloud plays it (the fake's
// store, and a bare repository standing in for its GitHub repository); hopsesh plans it,
// runs the teleport in a new worktree (--run), checks the copy and keeps the
// cloud's branch under hopsesh/from/claude-cloud/ (or says none was pushed), continues it in
// Codex for claude→codex rows, and undo takes it all back.
func (r *runner) fetch(row Row) error {
	if row.Location == "codex-cloud" {
		return r.fetchCodex(row)
	}
	id := newID()
	marker := fmt.Sprintf("hsm%03dx%s", row.N, id[:6])
	text := contents[row.Content] + " " + marker
	name := fmt.Sprintf("r%03d", row.N)
	var sr SeedRes
	if err := r.here.do("seed", SeedReq{ID: id, Repo: name, State: "clean"}, &sr); err != nil {
		return fmt.Errorf("seeding the repository: %w", err)
	}
	world := filepath.Join(r.out, "work", "cloud")
	gitConfig := filepath.Join(world, "gitconfig")
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": gitConfig, "FAKE_CLOUD_DIR": filepath.Join(world, "store"), "FAKE_CLOUD_FAIL": "", "FAKE_CLAUDE_SAYS": "ok"} {
		old, had := os.LookupEnv(k)
		os.Setenv(k, v)
		defer func() { // the other rows run with this machine's own settings
			if had {
				os.Setenv(k, old)
			} else {
				os.Unsetenv(k)
			}
		}()
	}
	o, err := fakecloud.NewOrigin(world, "https://github.com/hsm-matrix/"+name+".git")
	if err != nil {
		return err
	}
	if err := o.Redirect(gitConfig); err != nil {
		return err
	}
	if _, err := git(sr.Cwd, nil, "remote", "set-url", "origin", o.URL); err != nil {
		return err
	}
	if _, err := git(sr.Cwd, nil, "push", "-q", "origin", "main"); err != nil {
		return err
	}
	msgs := []fakecloud.Message{{Role: "user", Text: text}, {Role: "assistant", Text: "On it."}}
	if row.Content == "large" {
		for i, p := range padding(SeedReq{Large: true})[:300] {
			msgs = append(msgs, fakecloud.Message{Role: "assistant", Text: fmt.Sprintf("%d %s", i, p)})
		}
	}
	cs, err := fakecloud.Open(os.Getenv("FAKE_CLOUD_DIR")).Seed(fakecloud.Session{Cloud: fakecloud.ClaudeCloud, Title: fmt.Sprintf("matrix row %d", row.N),
		Repo: "github.com/hsm-matrix/" + name, CloneURL: o.FileURL(), Branch: "main", Code: "branch", Messages: msgs})
	if err != nil {
		return err
	}
	if row.Repo == "unpushed" {
		os.Setenv("FAKE_CLOUD_FAIL", "no-branch") // the cloud session never pushed its work
	} else if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, cs.ID, false); err != nil {
		return err
	}
	if _, err := r.hs(true, "clouds", "allow", "claude-cloud"); err != nil {
		return err
	}
	args := []string{"pull", "claude-cloud:" + cs.ID, "--to", sr.Cwd, "--yes", "--run", "--json"}
	if row.To != row.From {
		args = append(args, "--in", row.To)
	}
	out, err := r.hs(true, args...)
	if err != nil {
		return err
	}
	var res struct {
		Brought struct {
			Outcome, Branch, Worktree string
			NoBranch                  bool `json:"noBranch"`
			Restored, Expected        int
		} `json:"brought"`
		Continued *struct {
			Result *struct{ Journal string } `json:"result"`
		} `json:"continued"`
	}
	if i := strings.Index(out, "{\n"); i < 0 || json.Unmarshal([]byte(out[i:]), &res) != nil {
		return fmt.Errorf("pull --json printed no result:\n%s", tail(out, 800))
	}
	b := res.Brought
	want := len(msgs) + 1 // and the cloud's report of its work
	if row.Repo == "unpushed" {
		want = len(msgs)
	}
	switch {
	case b.Outcome != "unchecked" || b.Restored != want: // hopsesh did not start it: no briefing to check against
		return fmt.Errorf("the copy: %+v", b)
	case row.Repo == "unpushed" && !b.NoBranch:
		return fmt.Errorf("a session that never pushed reports a branch: %+v", b)
	case row.Repo != "unpushed" && !strings.HasPrefix(b.Branch, "hopsesh/from/claude-cloud/"):
		return fmt.Errorf("the cloud's branch was not renamed: %+v", b)
	case row.To != row.From && (res.Continued == nil || res.Continued.Result == nil):
		return fmt.Errorf("it did not continue in %s", row.To)
	}
	var fs []Found
	if err := r.here.do("find", FindReq{Marker: marker, Needles: []string{text, b.Worktree}}, &fs); err != nil {
		return err
	}
	ok := false
	for _, f := range fs {
		ok = ok || f.Agent == row.To && f.Mark == "" && f.Has[text] && f.Has[b.Worktree]
	}
	if !ok {
		return fmt.Errorf("here: no %s session with the conversation in %s (%+v)", row.To, b.Worktree, fs)
	}
	undos := 1
	if row.To != row.From {
		undos = 2 // the continuation, then the fetch
	}
	for i := 0; i < undos; i++ {
		if _, err := r.hs(true, "undo", "--yes"); err != nil {
			return err
		}
	}
	if _, err := os.Stat(b.Worktree); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("undo left the worktree %s", b.Worktree)
	}
	if left, _ := git(sr.Cwd, nil, "branch", "--list", "hopsesh/from/*", "claude/*"); left != "" {
		return fmt.Errorf("undo left branches: %s", left)
	}
	return nil
}

// fetchCodex brings a Codex cloud task here (a stand-in cloud plays it): the task's diff is
// committed on hopsesh/from/codex-cloud/<id> in a new worktree and the task is written as a
// session of the agent it comes into (Codex, or Claude Code for codex→claude rows), with its
// title; a task still running (unpushed rows) is refused, with no diff to bring. Undo takes
// it all back.
func (r *runner) fetchCodex(row Row) error {
	id := newID()
	marker := fmt.Sprintf("hsm%03dx%s", row.N, id[:6])
	name := fmt.Sprintf("r%03d", row.N)
	var sr SeedRes
	if err := r.here.do("seed", SeedReq{ID: id, Repo: name, State: "clean"}, &sr); err != nil {
		return fmt.Errorf("seeding the repository: %w", err)
	}
	o, restore, err := r.cloudWorld(name)
	defer restore()
	if err != nil {
		return err
	}
	if _, err := git(sr.Cwd, nil, "remote", "set-url", "origin", o.URL); err != nil {
		return err
	}
	if _, err := git(sr.Cwd, nil, "push", "-q", "origin", "main"); err != nil {
		return err
	}
	head, _ := git(sr.Cwd, nil, "rev-parse", "HEAD")
	title := contents[row.Content] + " " + marker
	cs, err := fakecloud.Open(os.Getenv("FAKE_CLOUD_DIR")).Seed(fakecloud.Session{Cloud: fakecloud.CodexCloud, Title: title, Repo: "github.com/hsm-matrix/" + name,
		CloneURL: o.FileURL(), Branch: "main", Base: strings.TrimSpace(head), Code: "branch", Env: "env_hsm_" + name, EnvLabel: name,
		Messages: []fakecloud.Message{{Role: "user", Text: title}}})
	if err != nil {
		return err
	}
	if row.Repo != "unpushed" {
		if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, cs.ID, false); err != nil {
			return err
		}
	}
	if _, err := r.hs(true, "clouds", "allow", "codex-cloud"); err != nil {
		return err
	}
	args := []string{"pull", "codex-cloud:" + cs.ID, "--to", sr.Cwd, "--yes", "--json"}
	if row.To != row.From {
		args = append(args, "--in", row.To)
	}
	if row.Repo == "unpushed" {
		out, err := r.hs(false, args...)
		if err != nil {
			return err
		}
		if !strings.Contains(out, "still running in Codex cloud, so there is no code to bring yet") {
			return fmt.Errorf("the refusal does not say why:\n%s", tail(out, 600))
		}
		return nil
	}
	out, err := r.hs(true, args...)
	if err != nil {
		return err
	}
	var res struct {
		Brought struct {
			Outcome, Branch, Worktree string
			Written                   bool
		} `json:"brought"`
	}
	if i := strings.Index(out, "{\n"); i < 0 || json.Unmarshal([]byte(out[i:]), &res) != nil {
		return fmt.Errorf("pull --json printed no result:\n%s", tail(out, 800))
	}
	b := res.Brought
	if b.Outcome != "complete" || !b.Written || b.Branch != "hopsesh/from/codex-cloud/"+cs.ID {
		return fmt.Errorf("the task: %+v", b)
	}
	if _, err := os.Stat(filepath.Join(b.Worktree, "cloud-work", cs.ID+".md")); err != nil {
		return fmt.Errorf("the task's diff is not in %s", b.Worktree)
	}
	var fs []Found
	if err := r.here.do("find", FindReq{Marker: marker, Needles: []string{marker, b.Worktree}}, &fs); err != nil {
		return err
	}
	ok := false
	for _, f := range fs {
		ok = ok || f.Agent == row.To && f.Has[marker] && f.Has[b.Worktree]
	}
	if !ok {
		return fmt.Errorf("here: no %s session with the task in %s (%+v)", row.To, b.Worktree, fs)
	}
	if _, err := r.hs(true, "undo", "--yes"); err != nil {
		return err
	}
	if _, err := os.Stat(b.Worktree); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("undo left the worktree %s", b.Worktree)
	}
	if left, _ := git(sr.Cwd, nil, "branch", "--list", "hopsesh/from/*"); left != "" {
		return fmt.Errorf("undo left branches: %s", left)
	}
	return nil
}

// cloudWorld points git and the stand-in cloud at a world of their own for one row (a bare
// repository standing in for the repository on GitHub, and the cloud's store), and returns
// the stand-in remote and a function that restores this machine's settings.
func (r *runner) cloudWorld(name string) (fakecloud.Origin, func(), error) {
	world := filepath.Join(r.out, "work", "cloud")
	gitConfig := filepath.Join(world, "gitconfig")
	var restore []func()
	done := func() {
		for _, f := range restore {
			f()
		}
	}
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": gitConfig, "FAKE_CLOUD_DIR": filepath.Join(world, "store"), "FAKE_CLOUD_FAIL": "", "FAKE_CLAUDE_SAYS": "ok"} {
		old, had := os.LookupEnv(k)
		os.Setenv(k, v)
		restore = append(restore, func() {
			if had {
				os.Setenv(k, old)
			} else {
				os.Unsetenv(k)
			}
		})
	}
	o, err := fakecloud.NewOrigin(world, "https://github.com/hsm-matrix/"+name+".git")
	if err == nil {
		err = o.Redirect(gitConfig)
	}
	return o, done, err
}

// handoff hands a session here off to Claude Code cloud or Codex cloud (a stand-in cloud
// plays it; Codex cloud runs it in an environment): the
// plan's code (the branch as it is when clean and pushed, else a snapshot on a handoff
// branch, with the untracked draft when asked), the briefing with the session's words,
// the checkout left as it was, the mark; a cloud round trip then lets the cloud work and
// brings the session home through the bring-back path. Undo takes it all back (the branch
// with a lease; the cloud session stays, as a step owed).
func (r *runner) handoff(row Row) error {
	id := newID()
	marker := fmt.Sprintf("hsm%03dx%s", row.N, id[:6])
	text := contents[row.Content] + " " + marker
	name := fmt.Sprintf("r%03d", row.N)
	var sr SeedRes
	if err := r.here.do("seed", SeedReq{Agent: row.From, ID: id, Title: fmt.Sprintf("matrix row %d", row.N), Text: text, Large: row.Content == "large",
		Repo: name, State: row.Repo, Session: true}, &sr); err != nil {
		return fmt.Errorf("seeding the session: %w", err)
	}
	o, restore, err := r.cloudWorld(name)
	defer restore()
	if err != nil {
		return err
	}
	cloud := row.Location
	if _, err := r.hs(true, "clouds", "allow", cloud); err != nil {
		return err
	}
	to := []string{"--to", cloud}
	if cloud == "codex-cloud" {
		to = append(to, "--env", "env_hsm_"+name)
	}
	ref := row.From + "/" + id
	if row.Repo == "none" {
		out, err := r.hs(false, append(append([]string{"handoff", ref}, to...), "--yes")...)
		if err != nil {
			return err
		}
		if !strings.Contains(out, "isn't in a git repository with a remote") {
			return fmt.Errorf("the refusal does not say why:\n%s", tail(out, 600))
		}
		return nil
	}
	first, err := git(sr.Cwd, nil, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return err
	}
	if _, err := git(sr.Cwd, nil, "remote", "set-url", "origin", o.URL); err != nil {
		return err
	}
	if _, err := git(sr.Cwd, nil, "push", "-q", "origin", strings.TrimSpace(first)+":refs/heads/main"); err != nil {
		return err
	}
	before, _ := git(sr.Cwd, nil, "--no-optional-locks", "status", "--porcelain")
	head, _ := git(sr.Cwd, nil, "rev-parse", "HEAD")
	args := append(append([]string{"handoff", ref}, to...), "--yes", "--json")
	if row.Repo == "uncommitted" {
		args = append(args, "--untracked", "draft-*")
	}
	run := r.hs
	if cloud == "claude-cloud" {
		// Claude Code starts the session only in a terminal the user answers.
		run = func(_ bool, args ...string) (string, error) { return r.hsTerminal(args...) }
	}
	out, err := run(true, args...)
	if err != nil {
		return err
	}
	var res struct {
		Result  struct{ Journal string } `json:"result"`
		Handoff struct {
			Session, Branch, Snapshot string
			Reuse, Pushed             bool
			MarkText                  string `json:"markText"`
		} `json:"handoff"`
	}
	if i := strings.Index(out, "{\n"); i < 0 || json.Unmarshal([]byte(out[i:]), &res) != nil {
		return fmt.Errorf("handoff --json printed no result:\n%s", tail(out, 800))
	}
	h := res.Handoff
	switch {
	case row.Repo == "clean" && (!h.Reuse || h.Branch != "main" || h.Pushed):
		return fmt.Errorf("a clean pushed branch goes as it is: %+v", h)
	case row.Repo != "clean" && (h.Reuse || !strings.HasPrefix(h.Branch, "hopsesh/handoff/") || !h.Pushed || h.Snapshot == ""):
		return fmt.Errorf("work in progress goes on a handoff branch: %+v", h)
	}
	if row.Repo == "uncommitted" {
		tree, _ := git(sr.Cwd, nil, "ls-tree", "-r", "--name-only", h.Snapshot)
		if !strings.Contains(tree, "draft-"+id[:8]+".txt") {
			return fmt.Errorf("the chosen untracked file is not in the snapshot:\n%s", tree)
		}
	}
	if after, _ := git(sr.Cwd, nil, "--no-optional-locks", "status", "--porcelain"); after != before {
		return fmt.Errorf("the checkout changed:\n%s\n---\n%s", before, after)
	}
	if now, _ := git(sr.Cwd, nil, "rev-parse", "HEAD"); now != head {
		return fmt.Errorf("HEAD moved: %s → %s", head, now)
	}
	cs, err := fakecloud.Open(os.Getenv("FAKE_CLOUD_DIR")).Get(h.Session)
	if err != nil {
		return err
	}
	if len(cs.Messages) == 0 || !strings.HasPrefix(cs.Messages[0].Text, "[hopsesh] ") || !strings.Contains(cs.Messages[0].Text, marker) || cs.Branch != h.Branch {
		return fmt.Errorf("the cloud session: branch %s, first message %q", cs.Branch, tail(cs.Messages[0].Text, 300))
	}
	var fs []Found
	if err := r.here.do("find", FindReq{Marker: marker}, &fs); err != nil {
		return err
	}
	marked := false
	for _, f := range fs {
		marked = marked || f.ID == id && strings.Contains(f.Mark, "continued in "+cloudTitles[cloud])
	}
	if !marked {
		return fmt.Errorf("the session here is not marked (%+v)", fs)
	}
	undos := 1
	if row.Op == "cloud-roundtrip" {
		if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, h.Session, false); err != nil {
			return err
		}
		top, _ := git(sr.Cwd, nil, "rev-parse", "--show-toplevel")
		pull := []string{"pull", cloud + ":" + h.Session, "--to", strings.TrimSpace(top), "--yes", "--json"}
		if cloud == "claude-cloud" {
			pull = append(pull, "--run") // the teleport runs here
		}
		out, err := r.hs(true, pull...)
		if err != nil {
			return err
		}
		var back struct {
			Brought struct{ Outcome, Branch, Worktree string } `json:"brought"`
		}
		if i := strings.Index(out, "{\n"); i < 0 || json.Unmarshal([]byte(out[i:]), &back) != nil {
			return fmt.Errorf("pull --json printed no result:\n%s", tail(out, 800))
		}
		if back.Brought.Outcome != "complete" || !strings.HasPrefix(back.Brought.Branch, "hopsesh/from/"+cloud+"/") {
			return fmt.Errorf("the way back: %+v", back.Brought)
		}
		if _, err := os.Stat(filepath.Join(back.Brought.Worktree, "cloud-work", h.Session+".md")); err != nil {
			return fmt.Errorf("the cloud's work did not come back")
		}
		undos = 2 // the fetch, then the hand-off
	}
	for i := 0; i < undos; i++ {
		undo := []string{"undo", "--yes"}
		if i == 1 && cloud == "codex-cloud" {
			undo = append(undo, "--force") // the task worked since the hand-off, so undo asks first
		}
		if _, err := r.hs(true, undo...); err != nil {
			return err
		}
	}
	if h.Pushed {
		if left, _ := git(o.Bare, nil, "for-each-ref", "refs/heads/"+h.Branch); left != "" {
			return fmt.Errorf("undo left the handoff branch: %s", left)
		}
	}
	fs = nil
	if err := r.here.do("find", FindReq{Marker: marker}, &fs); err != nil {
		return err
	}
	for _, f := range fs {
		if f.ID == id && f.Mark != "" {
			return fmt.Errorf("undo left the mark %q", f.Mark)
		}
	}
	return nil
}

// cloudHop hands a cloud session on to the other vendor's cloud through this machine (a
// stand-in cloud plays both): a Claude Code cloud session comes here by teleport and goes
// on to Codex cloud from the claude/… branch as it is, or a Codex cloud task comes here as
// its diff and a Codex thread and goes on to Claude Code cloud from a handoff branch (in
// this terminal, where Claude Code starts it). The second cloud's briefing carries the
// row's words; one undo takes both legs back.
func (r *runner) cloudHop(row Row) error {
	id := newID()
	marker := fmt.Sprintf("hsm%03dx%s", row.N, id[:6])
	title := contents[row.Content] + " " + marker
	name := fmt.Sprintf("r%03d", row.N)
	var sr SeedRes
	if err := r.here.do("seed", SeedReq{ID: id, Repo: name, State: "clean"}, &sr); err != nil {
		return fmt.Errorf("seeding the repository: %w", err)
	}
	o, restore, err := r.cloudWorld(name)
	defer restore()
	if err != nil {
		return err
	}
	if _, err := git(sr.Cwd, nil, "remote", "set-url", "origin", o.URL); err != nil {
		return err
	}
	if _, err := git(sr.Cwd, nil, "push", "-q", "origin", "main"); err != nil {
		return err
	}
	head, _ := git(sr.Cwd, nil, "rev-parse", "HEAD")
	from, to := row.Location, map[string]string{"claude-cloud": "codex-cloud", "codex-cloud": "claude-cloud"}[row.Location]
	seed := fakecloud.Session{Cloud: fakecloud.ClaudeCloud, Title: title, Repo: "github.com/hsm-matrix/" + name, CloneURL: o.FileURL(), Branch: "main",
		Base: strings.TrimSpace(head), Code: "branch", Messages: []fakecloud.Message{{Role: "user", Text: title}, {Role: "assistant", Text: "On it."}}} // started on the web
	if from == "codex-cloud" {
		seed.Cloud, seed.Env, seed.EnvLabel, seed.Messages = fakecloud.CodexCloud, "env_hsm_"+name, name, []fakecloud.Message{{Role: "user", Text: title}}
	}
	cs, err := fakecloud.Open(os.Getenv("FAKE_CLOUD_DIR")).Seed(seed)
	if err != nil {
		return err
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, cs.ID, false); err != nil {
		return err
	}
	cs, _ = fakecloud.Open(os.Getenv("FAKE_CLOUD_DIR")).Get(cs.ID)
	for _, c := range []string{from, to} {
		if _, err := r.hs(true, "clouds", "allow", c); err != nil {
			return err
		}
	}
	if from == "codex-cloud" {
		if _, err := r.hs(true, "clouds", "env", "codex-cloud", "github.com/hsm-matrix/"+name, "env_hsm_"+name); err != nil {
			return err
		}
	} else if _, err := r.hs(true, "plan", "https://claude.ai/code/"+cs.ID, "--to", sr.Cwd, "--json"); err != nil {
		return err
	}
	args := []string{"handoff", from + ":" + cs.ID, "--to", to, "--to-dir", sr.Cwd, "--yes", "--json"}
	if to == "codex-cloud" {
		args = append(args, "--env", "env_hsm_"+name)
	}
	run := r.hs
	if to == "claude-cloud" {
		run = func(_ bool, args ...string) (string, error) { return r.hsTerminal(args...) }
	}
	out, err := run(true, args...)
	if err != nil {
		return err
	}
	var res struct {
		Result struct {
			Journal string
			Fetch   struct{ Worktree string }
		} `json:"result"`
		Hop struct {
			State, Fetch, Handoff string
		} `json:"hop"`
		Handoff struct {
			Session, Branch string
			Reuse, Pushed   bool
		} `json:"handoff"`
	}
	if i := strings.Index(out, "{\n"); i < 0 || json.Unmarshal([]byte(out[i:]), &res) != nil {
		return fmt.Errorf("handoff --json printed no result:\n%s", tail(out, 800))
	}
	h := res.Handoff
	switch {
	case res.Hop.State != "done" || res.Hop.Fetch == "" || res.Hop.Handoff == "" || h.Session == "":
		return fmt.Errorf("the hop: %+v %+v", res.Hop, h)
	case from == "claude-cloud" && (!h.Reuse || h.Branch != cs.Result || h.Pushed):
		return fmt.Errorf("the Codex cloud task must start from the claude/… branch as it is: %+v", h)
	case from == "codex-cloud" && (!h.Pushed || !strings.HasPrefix(h.Branch, "hopsesh/handoff/")):
		return fmt.Errorf("the task's work goes up on a handoff branch: %+v", h)
	}
	next, err := fakecloud.Open(os.Getenv("FAKE_CLOUD_DIR")).Get(h.Session)
	if err != nil {
		return err
	}
	if len(next.Messages) == 0 || !strings.HasPrefix(next.Messages[0].Text, "[hopsesh] ") || !strings.Contains(next.Messages[0].Text, marker) {
		return fmt.Errorf("the second cloud's briefing lacks the row's words: %q", tail(next.Messages[0].Text, 300))
	}
	if _, err := r.hs(true, "undo", "--yes"); err != nil {
		return err
	}
	if wt := res.Result.Fetch.Worktree; wt != "" {
		if _, err := os.Stat(wt); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("undo left the worktree %s", wt)
		}
	}
	if left, _ := git(sr.Cwd, nil, "branch", "--list", "hopsesh/*", "claude/*"); left != "" {
		return fmt.Errorf("undo left branches: %s", left)
	}
	if h.Pushed {
		if left, _ := git(o.Bare, nil, "for-each-ref", "refs/heads/"+h.Branch); left != "" {
			return fmt.Errorf("undo left the handoff branch: %s", left)
		}
	}
	return nil
}

// Receipts cross the same helper/SSH boundary as native sessions. Every matrix
// return checks durable metadata, including when movement notices are disabled.
func checkMovement(f Found, transfers int, notify, fork bool) error {
	if f.Graph == nil {
		return fmt.Errorf("%s: missing movement receipt", f.Path)
	}
	var hops []lineage.Hop
	for _, h := range f.Graph.ActiveHops() {
		if h.Line == f.Graph.Branch {
			hops = append(hops, h)
		}
	}
	if len(hops) != transfers {
		return fmt.Errorf("%s: movements=%d, want %d", f.Path, len(hops), transfers)
	}
	for _, h := range hops {
		if h.Notify != notify {
			return fmt.Errorf("%s: lost notify=%t preference", f.Path, notify)
		}
	}
	last := hops[len(hops)-1]
	if last.Fork != fork {
		return fmt.Errorf("%s: fork=%t, want %t", f.Path, last.Fork, fork)
	}
	if _, ok := f.Graph.Departure(last.To); ok {
		return fmt.Errorf("%s: arrival has departure notice", f.Path)
	}
	departure, departed := f.Graph.Departure(last.From)
	if departed != notify || departed && departure.ID != last.ID {
		return fmt.Errorf("%s: incorrect source departure", f.Path)
	}
	returns := f.Graph.ReturnReplicas(last.To)
	if fork {
		if len(returns) != 0 {
			return fmt.Errorf("%s: fork offers parent as a return", f.Path)
		}
	} else if len(returns) == 0 || returns[0].ID != last.From {
		return fmt.Errorf("%s: missing previous native replica in returns", f.Path)
	}
	return nil
}
