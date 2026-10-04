// Package fakecloud is the stand-in for the vendors' clouds that the tests run against:
// cloud sessions kept as JSON files in $FAKE_CLOUD_DIR, a local bare repository standing in
// for the GitHub repository the clouds clone and push, the cloud verbs of the stand-in
// claude and codex programs (see internal/testkit/fakeagent), a small third-party-style
// cloud CLI of its own (`fakecloud remote …`, driven by Stub), and `fakecloud work <id>`,
// which plays the cloud agent. Every call is logged to $FAKE_AGENT_LOG.
//
// The vendors' output shapes come from their docs and source where those say; the rest is
// marked "unverified" where it is printed. Nothing here talks to a network.
//
// FAKE_CLOUD_FAIL switches on one failure: signed-out, not-eligible, no-env,
// repo-mismatch, partial, empty, no-branch, archived, push-refused, bad-record (one
// unreadable record in a listing) or slow (a listing that takes five seconds).
//
// It is test infrastructure, not part of hopsesh.
package fakecloud

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/repos"
)

// Clouds the fake plays.
const (
	ClaudeCloud = "claude-cloud"
	CodexCloud  = "codex-cloud"
	FakeCloud   = "fake-cloud" // the third-party-style cloud of `fakecloud remote`
)

// States, in the fake's own words (each vendor's verbs map them to theirs).
const (
	StateRunning  = "running"
	StateIdle     = "idle"
	StateDone     = "done"
	StateFailed   = "failed"
	StateArchived = "archived"
)

// Session is one cloud session or task.
type Session struct {
	ID    string `json:"id"`
	Cloud string `json:"cloud"`
	Title string `json:"title"`
	// Repo is the repository's identity (github.com/example/demo); CloneURL is where the
	// cloud agent clones it (the remote after url.insteadOf: the bare origin).
	Repo     string `json:"repo"`
	CloneURL string `json:"cloneUrl"`
	Branch   string `json:"branch"`           // the branch it started from
	Base     string `json:"base"`             // the commit it started from
	Code     string `json:"code"`             // how the code came up: branch or bundle
	Diff     string `json:"diff,omitempty"`   // a starting diff sent with it, then the task's diff (codex)
	Result   string `json:"result,omitempty"` // the branch the cloud pushed its work to
	Env      string `json:"env,omitempty"`
	Attempts int    `json:"attempts"`
	State    string `json:"state"`
	// Applied: codex apply ran for this task.
	Applied  bool      `json:"applied,omitempty"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Messages []Message `json:"messages"`
}

// Message is one turn of a cloud session's conversation.
type Message struct {
	Role string    `json:"role"` // user | assistant
	Text string    `json:"text"`
	Time time.Time `json:"time"`
}

// URL is the session's page, in its vendor's form.
func (s Session) URL() string {
	switch s.Cloud {
	case ClaudeCloud:
		return "https://claude.ai/code/" + s.ID
	case CodexCloud:
		return "https://chatgpt.com/codex/tasks/" + s.ID
	}
	return "https://cloud.example.com/sessions/" + s.ID
}

// Proc is one run of a stand-in program: its arguments (after the program's name), its
// environment, its folder and its output.
type Proc struct {
	Args []string
	// Vars are variables set for this run; Env falls back to the process's own.
	Vars   map[string]string
	Dir    string
	Stdout io.Writer
	Stderr io.Writer
}

// Env returns a variable of the run.
func (p Proc) Env(k string) string {
	if v, ok := p.Vars[k]; ok {
		return v
	}
	return os.Getenv(k)
}

// environ is the environment for the programs a run starts (git).
func (p Proc) environ() []string {
	env := os.Environ()
	for k, v := range p.Vars {
		env = append(env, k+"="+v)
	}
	return env
}

// fail is the failure FAKE_CLOUD_FAIL switched on ("" for none).
func (p Proc) fail() string { return p.Env("FAKE_CLOUD_FAIL") }

func (p Proc) errorf(code int, format string, a ...any) int {
	fmt.Fprintf(p.Stderr, format+"\n", a...)
	return code
}

// Log records a call in $FAKE_AGENT_LOG, one line each.
func (p Proc) Log(line string) {
	if f := p.Env("FAKE_AGENT_LOG"); f != "" {
		if w, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintln(w, line)
			w.Close()
		}
	}
}

// Store is the fake clouds' state: one JSON file per session under dir/sessions.
type Store struct{ Dir string }

// Open returns the store in dir.
func Open(dir string) Store { return Store{Dir: dir} }

func (p Proc) store() (Store, error) {
	d := p.Env("FAKE_CLOUD_DIR")
	if d == "" {
		return Store{}, errors.New("FAKE_CLOUD_DIR is not set")
	}
	return Open(d), nil
}

func (s Store) path(id string) string {
	return filepath.Join(s.Dir, "sessions", filepath.Base(id)+".json")
}

// Get reads a session.
func (s Store) Get(id string) (Session, error) {
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		return Session{}, fmt.Errorf("no cloud session %s", id)
	}
	var x Session
	return x, json.Unmarshal(b, &x)
}

// Put writes a session.
func (s Store) Put(x Session) error {
	if err := os.MkdirAll(filepath.Join(s.Dir, "sessions"), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(x, "", "  ")
	return os.WriteFile(s.path(x.ID), b, 0o600)
}

// List returns one cloud's sessions, newest first.
func (s Store) List(cloud string) ([]Session, error) {
	files, _ := filepath.Glob(filepath.Join(s.Dir, "sessions", "*.json"))
	var out []Session
	for _, f := range files {
		x, err := s.Get(strings.TrimSuffix(filepath.Base(f), ".json"))
		if err != nil {
			return nil, err
		}
		if x.Cloud == cloud {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// Seed adds a session as if the cloud had made it (for a test that starts from one): its
// id is made for its cloud unless given, and its times are now unless given.
func (s Store) Seed(x Session) (Session, error) {
	if x.ID == "" {
		x.ID = newID(x.Cloud)
	}
	now := time.Now().UTC()
	if x.Created.IsZero() {
		x.Created = now
	}
	if x.Updated.IsZero() {
		x.Updated = x.Created
	}
	if x.State == "" {
		x.State = StateRunning
	}
	if x.Attempts == 0 {
		x.Attempts = 1
	}
	return x, s.Put(x)
}

// newID makes an id in the vendor's form: session_01… (Claude Code), task_e_… (Codex),
// fk-… (the fake's own cloud).
func newID(cloud string) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	h := hex.EncodeToString(b[:])
	switch cloud {
	case ClaudeCloud:
		const alphabet = "0123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
		id := []byte("session_01")
		for _, c := range b[:12] {
			id = append(id, alphabet[int(c)%len(alphabet)])
		}
		return string(id)
	case CodexCloud:
		return "task_e_" + h
	}
	return "fk-" + h[:12]
}

// newUUID is a version-4-looking UUID.
func newUUID() string {
	var u [16]byte
	_, _ = rand.Read(u[:])
	return fmt.Sprintf("%x-%x-4%x-8%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// Origin is a bare repository standing in for a repository on GitHub.
type Origin struct {
	Bare string // the bare repository
	URL  string // the URL it stands in for: https://github.com/example/demo.git
}

// NewOrigin makes a bare repository under dir for url. Its pre-receive hook refuses every
// push while FAKE_CLOUD_FAIL=push-refused, as branch protection would.
func NewOrigin(dir, url string) (Origin, error) {
	name := strings.TrimSuffix(filepath.Base(url), ".git") + ".git"
	o := Origin{Bare: filepath.Join(dir, "origins", name), URL: url}
	if err := os.MkdirAll(o.Bare, 0o700); err != nil {
		return o, err
	}
	if _, err := git(nil, o.Bare, "init", "-q", "--bare", "-b", "main"); err != nil {
		return o, err
	}
	hook := "#!/bin/sh\nif [ \"$FAKE_CLOUD_FAIL\" = push-refused ]; then\n  echo 'refused: branch protection (fake)' >&2\n  exit 1\nfi\nexit 0\n"
	return o, os.WriteFile(filepath.Join(o.Bare, "hooks", "pre-receive"), []byte(hook), 0o755)
}

// FileURL is the bare repository as a file URL.
func (o Origin) FileURL() string {
	p := filepath.ToSlash(o.Bare)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // C:/… on Windows
	}
	return "file://" + p
}

// Redirect makes git, with this config file (a repository's .git/config, or the file
// GIT_CONFIG_GLOBAL names), fetch from and push to the bare repository whenever it is asked
// for the GitHub URL. The remote's configured URL stays the GitHub one, so hopsesh still
// sees the repository as github.com/owner/repo.
func (o Origin) Redirect(configFile string) error {
	_, err := git(nil, "", "config", "--file", configFile, "url."+o.FileURL()+".insteadOf", o.URL)
	return err
}

// git runs git in dir with env (nil: the process's own) and returns its trimmed output.
func git(env []string, dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// Work plays the cloud agent on a session: it clones the session's branch, commits a
// change, and, as its cloud does, pushes it to a branch of its own (claude/web-session-…
// for Claude Code, fake/… for the fake's own cloud; sameBranch pushes back to the branch
// it started from) or keeps it as the task's diff (Codex). The session then waits for the
// user (Claude Code) or is done.
func Work(p Proc, id string, sameBranch bool) error {
	st, err := p.store()
	if err != nil {
		return err
	}
	s, err := st.Get(id)
	if err != nil {
		return err
	}
	if s.CloneURL == "" {
		return fmt.Errorf("%s has no repository to clone", id)
	}
	tmp, err := os.MkdirTemp("", "fakecloud-work-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	env := append(p.environ(), "GIT_AUTHOR_NAME=Cloud Agent", "GIT_AUTHOR_EMAIL=cloud@example.com",
		"GIT_COMMITTER_NAME=Cloud Agent", "GIT_COMMITTER_EMAIL=cloud@example.com")
	work := filepath.Join(tmp, "work")
	if _, err := git(env, "", "clone", "-q", "--branch", s.Branch, s.CloneURL, work); err != nil {
		return err
	}
	if s.Diff != "" && s.Cloud == CodexCloud {
		if err := applyDiff(env, work, s.Diff); err != nil {
			return err
		}
	}
	file := filepath.Join(work, "cloud-work", s.ID+".md")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(file, []byte("Work done by "+s.Cloud+" session "+s.ID+".\n"), 0o600); err != nil {
		return err
	}
	if _, err := git(env, work, "add", "-A"); err != nil {
		return err
	}
	if _, err := git(env, work, "commit", "-q", "-m", "Cloud work for "+s.Title); err != nil {
		return err
	}
	now := time.Now().UTC()
	switch s.Cloud {
	case CodexCloud:
		diff, err := git(env, work, "diff", s.Base, "HEAD")
		if err != nil {
			return err
		}
		s.Diff, s.State = diff+"\n", StateDone
		s.Messages = append(s.Messages, Message{Role: "assistant", Text: "Added cloud-work/" + s.ID + ".md.", Time: now})
	default:
		branch := s.Branch
		if !sameBranch {
			branch = "fake/" + s.ID
			if s.Cloud == ClaudeCloud {
				branch = "claude/web-session-" + strings.ToLower(s.ID[len(s.ID)-6:])
			}
		}
		if _, err := git(env, work, "push", "-q", "origin", "HEAD:refs/heads/"+branch); err != nil {
			s.State, s.Updated = StateFailed, now
			_ = st.Put(s)
			return err
		}
		s.Result, s.State = branch, StateDone
		if s.Cloud == ClaudeCloud {
			s.State = StateIdle // a Claude Code cloud session stays open after it pushes
		}
		s.Messages = append(s.Messages, Message{Role: "assistant", Text: "I added cloud-work/" + s.ID + ".md and pushed it to " + branch + ".", Time: now})
	}
	s.Updated = now
	return st.Put(s)
}

// applyDiff applies a unified diff in a checkout and commits nothing.
func applyDiff(env []string, dir, diff string) error {
	cmd := exec.Command("git", "apply", "--whitespace=nowarn", "-")
	cmd.Dir, cmd.Env, cmd.Stdin = dir, env, strings.NewReader(diff)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git apply: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// repoOf reads a checkout's remote: its identity (from the configured URL, as hopsesh
// reads it) and the URL git really uses (after url.insteadOf), the current branch and its
// commit.
func repoOf(env []string, dir string) (identity, cloneURL, branch, head string, err error) {
	raw, err := git(env, dir, "config", "--get", "remote.origin.url")
	if err != nil {
		return "", "", "", "", errors.New("this folder has no git remote named origin")
	}
	cloneURL, _ = git(env, dir, "ls-remote", "--get-url", "origin")
	branch, _ = git(env, dir, "rev-parse", "--abbrev-ref", "HEAD")
	head, _ = git(env, dir, "rev-parse", "HEAD")
	return repos.Identity(raw), cloneURL, branch, head, nil
}

// onRemote reports whether branch exists on origin.
func onRemote(env []string, dir, branch string) bool {
	out, err := git(env, dir, "ls-remote", "--heads", "origin", branch)
	return err == nil && out != ""
}
