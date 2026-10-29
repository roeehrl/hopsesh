package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/peer"
)

// The scenarios need:
//
//	HOPSESH_REMOTE_SCENARIOS=1
//	HOPSESH_BIN              hopsesh here (the build under test)
//	HOPSESH_REMOTE_THERE     the other machine's ssh destination (default hsremote@127.0.0.1);
//	                         this user logs in there with a key, and stand-in agents
//	                         (internal/devtools/fakeagent) are on its PATH; hopsesh is not
//	HOPSESH_REMOTE_OFFLINE   an ssh alias nothing answers at (default hsr-offline)
//	HOPSESH_REMOTE_OLD_BIN   optional: hopsesh 0.4.5 (peer protocol 5) for the other
//	                         machine's system, for the version-skew scenario
//
// scripts/remote-scenarios.sh sets all of it up. Everything of hopsesh's there is
// removed first: the other machine's user exists for these scenarios only.

type env struct {
	bin, there, offline, oldBin string
	work, config, state         string
	home                        string // the home folder there
}

const sessionID = "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a10"

func TestRemoteScenarios(t *testing.T) {
	if os.Getenv("HOPSESH_REMOTE_SCENARIOS") != "1" {
		t.Skip("set HOPSESH_REMOTE_SCENARIOS=1 (scripts/remote-scenarios.sh does) to run the remote-behaviour scenarios")
	}
	e := &env{bin: os.Getenv("HOPSESH_BIN"), there: os.Getenv("HOPSESH_REMOTE_THERE"), offline: os.Getenv("HOPSESH_REMOTE_OFFLINE"),
		oldBin: os.Getenv("HOPSESH_REMOTE_OLD_BIN")}
	if e.bin == "" {
		t.Fatal("HOPSESH_BIN is required")
	}
	if e.there == "" {
		e.there = "hsremote@127.0.0.1"
	}
	if e.offline == "" {
		e.offline = "hsr-offline"
	}
	e.work = t.TempDir()
	e.config, e.state = filepath.Join(e.work, "config"), filepath.Join(e.work, "state")
	for _, d := range []string{"claude", "codex/sessions", "here"} {
		if err := os.MkdirAll(filepath.Join(e.work, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	out, err := e.remote(t, `printf %s "$HOME"`)
	if err != nil || out == "" {
		t.Fatalf("ssh %s: %v %s", e.there, err, out)
	}
	e.home = out
	e.stopServe(t)
	if out, err := e.remote(t, `rm -rf ~/.hopsesh ~/.config/hopsesh ~/.local/state/hopsesh ~/.claude ~/.codex ~/proj ~/.config/systemd/user/hopsesh.service`); err != nil {
		t.Fatalf("cleaning the other machine: %v %s", err, out)
	}
	t.Cleanup(func() { e.stopServe(t) })

	ok := t.Run("ssh-only", e.sshOnly) &&
		t.Run("setup-nobody-logged-in", e.setUp) &&
		t.Run("login-session", e.loginSession) &&
		t.Run("logged-out", e.loggedOut)
	if !ok {
		t.Fatal("the scenarios after a failed one depend on it")
	}
	t.Run("version-skew", e.versionSkew)
	t.Run("offline", e.offlineMachine)
}

// (a) A machine with no hopsesh: read over SSH, its originals unprotected, and nothing of
// hopsesh written there; this machine keeps its identity.
func (e *env) sshOnly(t *testing.T) {
	proj := e.home + "/proj"
	dir := e.home + "/.claude/projects/" + ClaudeSlug(proj)
	if out, err := e.remote(t, "mkdir -p "+ShQuote(dir)+" "+ShQuote(proj)); err != nil {
		t.Fatal(err, out)
	}
	if err := e.remoteWrite(t, dir+"/"+sessionID+".jsonl", ClaudeSession(sessionID, proj, "remote scenario")); err != nil {
		t.Fatal(err)
	}
	e.mustHS(t, "hosts", "add", "box", e.there)
	e.mustHS(t, "trust", "box", "--yes")

	m := e.machine(t, "box")
	if m.Status != "ok" || m.Presence != "ssh" || m.Hopsesh != "" {
		t.Fatalf("a machine without hopsesh: %+v", m)
	}
	if st, code := e.status(t, "box", false); st.Version != "" || st.Hopsesh.Fix != "setup" || st.Receiving.State != "off" || code != 3 {
		t.Fatalf("hosts status of a machine without hopsesh (exit %d): %+v", code, st)
	}
	ls := e.mustHS(t, "ls", "--host", "box", "--no-local", "--json")
	if !bytes.Contains(ls, []byte(sessionID)) {
		t.Fatalf("ls did not list the session there:\n%s", ls)
	}

	target := filepath.Join(e.work, "here", "proj")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := e.mustHS(t, "plan", "box:"+sessionID, "--to", target, "--json")
	if v, _ := Lookup(plan, "unprotected"); v != true {
		t.Fatalf("the plan does not say the original there is unprotected:\n%s", plan)
	}
	if !bytes.Contains(plan, []byte("won't be protected")) {
		t.Errorf("the plan's warnings do not explain it:\n%s", plan)
	}
	if v, _ := Lookup(plan, "cannotProtect"); !strings.Contains(fmt.Sprint(v), "hopsesh isn't installed there") {
		t.Errorf("the plan does not say why it can't protect the original: %v", v)
	}
	pull := e.mustHS(t, "pull", "box:"+sessionID, "--to", target, "--yes", "--json")
	if v, _ := Lookup(pull, "plan.unprotected"); v != true {
		t.Errorf("the pull's plan lost unprotected:\n%s", pull)
	}
	if v, _ := Lookup(pull, "result.notice"); !strings.Contains(fmt.Sprint(v), "not protected") {
		t.Errorf("the pull's notice claims a protection nothing there gives: %v", v)
	}
	if out, err := e.remote(t, `test ! -e ~/.hopsesh && echo clean`); err != nil || out != "clean" {
		listing, _ := e.remote(t, `find ~/.hopsesh -maxdepth 2`)
		t.Fatalf("reaching a machine without hopsesh wrote hopsesh's files there:\n%s", listing)
	}
	kept := e.kept(t)
	if !ValidEndpoint(kept) {
		t.Fatalf("this machine keeps no identity for box: %q", kept)
	}
}

// (b) hopsesh hosts setup: installed in ~/.hopsesh/bin, receiving, registered to start at
// login; with nobody logged in it answers protocol 6 from outside the login session.
func (e *env) setUp(t *testing.T) {
	out, stderr, err := e.hs(t, "hosts", "setup", "box", "--json")
	if err != nil {
		t.Fatalf("hosts setup: %v\n%s\n%s", err, out, stderr)
	}
	program := e.home + "/.hopsesh/bin/hopsesh"
	if v, _ := Lookup(out, "program"); v != program {
		t.Fatalf("installed at %v, want %s:\n%s", v, program, out)
	}
	if !bytes.Contains(out, []byte(`"name": "Receive sessions",`)) || bytes.Contains(out, []byte(`"state": "failed"`)) {
		t.Fatalf("a setup step failed:\n%s", out)
	}
	if v, _ := Lookup(out, "hello.protocol"); v != float64(peer.Protocol) {
		t.Fatalf("hopsesh there did not answer protocol %d:\n%s", peer.Protocol, out)
	}
	running, _ := Lookup(out, "service.running")
	login, _ := Lookup(out, "login")
	if running == true {
		// A runner with a systemd user manager for SSH logins started it for real: it is a
		// login session. Take it down, so the next scenarios start from nobody logged in.
		if login != true {
			t.Errorf("the service runs, but hopsesh there did not answer from it:\n%s", out)
		}
		t.Logf("the user's systemd manager started hopsesh serve; uninstalling it")
		if o, err := e.remote(t, ShQuote(program)+" serve uninstall"); err != nil {
			t.Fatal(err, o)
		}
	} else {
		if login == true {
			t.Fatalf("hopsesh there claims the login session while its service does not run:\n%s", out)
		}
		if !bytes.Contains(out, []byte(`"state": "waiting"`)) {
			t.Errorf("starting at login is not reported as waiting:\n%s", out)
		}
	}

	// The identity this machine kept is that machine's own now.
	there, err := e.remote(t, `cat ~/.hopsesh/endpoint-id`)
	if err != nil || strings.TrimSpace(there) != e.kept(t) {
		t.Fatalf("the kept identity was not handed over: there %q, kept %q (%v)", there, e.kept(t), err)
	}

	c, done := e.dial(t)
	defer done()
	hr := e.hello(t, c, peer.Hello{Protocol: peer.Protocol, Min: peer.MinProtocol, Version: "remote-scenarios", From: "here"})
	if hr.Protocol != peer.Protocol || hr.Login || hr.Endpoint != e.kept(t) || !hr.Receive {
		t.Fatalf("hello from outside the login session: %+v", hr)
	}
	e.wantNoLogin(t, c.Call(context.Background(), peer.MethodTools, struct{}{}, nil))

	m := e.machine(t, "box")
	if m.Presence != "installed" || !strings.Contains(m.PresenceWhy, "not running in the login session") {
		t.Fatalf("set up, nobody logged in: %+v", m)
	}
	// Its hooks and skill are read over SSH (hopsesh there can't answer for itself yet).
	st, code := e.status(t, "box", true)
	if st.Version == "" || st.Login.State != "off" || st.Receiving.State != "off" || code != 3 {
		t.Fatalf("hosts status, set up with nobody logged in (exit %d): %+v", code, st)
	}
	hook := ""
	for _, a := range st.Agents {
		if a.Agent == "claude" {
			hook = a.Hook.State
		}
	}
	if hook != "off" {
		t.Fatalf("the Claude Code hook there is not installed yet: %+v", st.Agents)
	}
	proj2 := filepath.Join(e.work, "here", "proj2")
	plan := e.mustHS(t, "plan", "box:"+sessionID, "--to", proj2, "--json")
	if v, _ := Lookup(plan, "unprotected"); v != true {
		t.Errorf("without the hook there, the original can't be blocked:\n%s", plan)
	}
	if v, _ := Lookup(plan, "cannotProtect"); !strings.Contains(fmt.Sprint(v), "hook isn't installed") {
		t.Errorf("the plan does not name the missing hook: %v", v)
	}
	// The hook as hopsesh there writes it: the plan reads the machine again and protects it.
	settings := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"'` + program + `' notice-hook --agent 'claude' --profile ''"}]}]}}`
	if err := e.remoteWrite(t, e.home+"/.claude/settings.json", []byte(settings)); err != nil {
		t.Fatal(err)
	}
	plan = e.mustHS(t, "plan", "box:"+sessionID, "--to", proj2, "--json")
	if v, _ := Lookup(plan, "unprotected"); v == true {
		t.Errorf("with hopsesh and its hook there, the original is still called unprotected:\n%s", plan)
	}
}

// (c) hopsesh serve in the login session (started by hand, as a login would): presence
// login, the doctor says so, and it runs sessions in terminals it owns.
func (e *env) loginSession(t *testing.T) {
	e.startServe(t)
	c, done := e.dial(t)
	hr := e.hello(t, c, peer.Hello{Protocol: peer.Protocol, Min: peer.MinProtocol, Version: "remote-scenarios", From: "here"})
	done()
	if hr.Protocol != peer.Protocol || !hr.Login {
		t.Fatalf("hello through hopsesh serve: %+v", hr)
	}
	if m := e.machine(t, "box"); m.Presence != "login" {
		t.Fatalf("hopsesh serve runs there: %+v", m)
	}
	doctor, _, err := e.hs(t, "doctor", "--all-machines", "--json")
	if Crashed(string(doctor)) {
		t.Fatalf("doctor crashed: %v\n%s", err, doctor)
	}
	if ch, err := FindCheck(doctor, "box: hopsesh"); err != nil || !ch.OK || !strings.Contains(ch.Detail, "in the login session") {
		t.Fatalf("doctor --all-machines: %+v %v\n%s", ch, err, doctor)
	}
	if _, err := FindCheck(doctor, "box: skill"); err != nil {
		t.Errorf("doctor --all-machines did not check protection there: %v\n%s", err, doctor)
	}

	// A new stand-in Claude Code session there, shown here (no terminal here: it ends).
	out, stderr, err := e.hs(t, "terminals", "run", "box", "--agent", "claude")
	if err != nil {
		t.Fatalf("terminals run: %v\n%s\n%s", err, out, stderr)
	}
	_, id, found := strings.Cut(stderr, "(terminal ")
	id, _, _ = strings.Cut(id, ")")
	if !found || id == "" {
		t.Fatalf("terminals run named no terminal:\n%s", stderr)
	}
	c, done = e.dial(t)
	defer done()
	e.hello(t, c, peer.Hello{Protocol: peer.Protocol, Min: peer.MinProtocol, Version: "remote-scenarios", From: "here"})
	var ts []peer.Terminal
	if err := c.Call(context.Background(), peer.MethodTerminalList, struct{}{}, &ts); err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, x := range ts {
		listed = listed || x.ID == id && x.Agent == "claude" && x.Kind == peer.TerminalNew
	}
	if !listed {
		t.Fatalf("terminal %s is not in hopsesh serve's list: %+v", id, ts)
	}
	if err := c.Call(context.Background(), peer.MethodTerminalClose, peer.TerminalID{ID: id}, nil); err != nil {
		t.Errorf("closing terminal %s: %v", id, err)
	}
}

// (d) Nobody logged in again: hopsesh is there, the login methods are refused.
func (e *env) loggedOut(t *testing.T) {
	e.stopServe(t)
	if m := e.machine(t, "box"); m.Presence != "installed" {
		t.Fatalf("hopsesh serve stopped: %+v", m)
	}
	c, done := e.dial(t)
	defer done()
	hr := e.hello(t, c, peer.Hello{Protocol: peer.Protocol, Min: peer.MinProtocol, Version: "remote-scenarios", From: "here"})
	if hr.Login {
		t.Fatalf("hello claims the login session: %+v", hr)
	}
	for _, method := range []string{peer.MethodTerminalList, peer.MethodAccounts, peer.MethodProtection, peer.MethodRemoteDefault} {
		e.wantNoLogin(t, c.Call(context.Background(), method, struct{}{}, nil))
	}
	out, stderr, err := e.hs(t, "terminals", "run", "box", "--agent", "claude")
	if err == nil || !strings.Contains(stderr, "not running in the login session on box") || Crashed(stderr) {
		t.Fatalf("terminals run with nobody logged in: %v\n%s\n%s", err, out, stderr)
	}
}

// (e) hopsesh 0.4.5 there (peer protocol 5): this one speaks it, calls it older, and
// still sends it sessions.
func (e *env) versionSkew(t *testing.T) {
	if e.oldBin == "" {
		t.Skip("HOPSESH_REMOTE_OLD_BIN (hopsesh 0.4.5 for the other machine) is not set")
	}
	b, err := os.ReadFile(e.oldBin)
	if err != nil {
		t.Fatal(err)
	}
	program := e.home + "/.hopsesh/bin/hopsesh"
	if err := e.remoteWrite(t, program, b); err != nil {
		t.Fatal(err)
	}
	if out, err := e.remote(t, "chmod 755 "+ShQuote(program)+" && "+ShQuote(program)+" receive on >/dev/null && "+ShQuote(program)+" version"); err != nil || !strings.Contains(out, "0.4.5") {
		t.Fatalf("hopsesh 0.4.5 there: %v %s", err, out)
	}
	c, done := e.dial(t)
	err = c.Call(context.Background(), peer.MethodHello, peer.Hello{Protocol: peer.Protocol, Min: peer.MinProtocol, Version: "remote-scenarios", From: "here"}, nil)
	var pe *peer.Error
	if !errors.As(err, &pe) || pe.Code != peer.CodeProtocol || pe.Protocol != 5 {
		done()
		t.Fatalf("hopsesh 0.4.5 answered a protocol-6 hello with %v", err)
	}
	var hr peer.HelloReply
	if err := c.Call(context.Background(), peer.MethodHello, peer.Hello{Protocol: pe.Protocol, Version: "remote-scenarios", From: "here"}, &hr); err != nil || hr.Protocol != 5 {
		done()
		t.Fatalf("the hello again in protocol 5: %+v %v", hr, err)
	}
	done()
	if m := e.machine(t, "box"); m.Presence != "older" || m.Hopsesh != "0.4.5" {
		t.Fatalf("hopsesh 0.4.5 there: %+v", m)
	}
	if st, code := e.status(t, "box", false); st.Version != "0.4.5" || st.Login.State != "error" || st.Login.Fix != "update" || code != 3 {
		t.Fatalf("hosts status of hopsesh 0.4.5 (exit %d): %+v", code, st)
	}

	// A session of this machine goes there.
	const id = "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a11"
	here, err := filepath.EvalSymlinks(filepath.Join(e.work, "here"))
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(here, "mine")
	dir := filepath.Join(e.work, "claude", "projects", ClaudeSlug(cwd))
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), ClaudeSession(id, cwd, "pushed to an older hopsesh"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, stderr, err := e.hs(t, "push", id, "box", "--to", e.home+"/proj", "--yes", "--json")
	if err != nil || Crashed(stderr) {
		t.Fatalf("push to hopsesh 0.4.5: %v\n%s\n%s", err, out, stderr)
	}
	journal, _ := Lookup(out, "result.journal")
	if s, _ := journal.(string); s == "" {
		t.Fatalf("the push printed no journal:\n%s", out)
	}
	if got, err := e.remote(t, `grep -rl 'pushed to an older hopsesh' ~/.claude/projects | head -n 1`); err != nil || got == "" {
		t.Fatalf("the session did not arrive there: %v %q", err, got)
	}
	if _, stderr, err := e.hs(t, "undo", journal.(string), "--yes"); err != nil {
		t.Errorf("undoing the push: %v\n%s", err, stderr)
	}
}

// (f) A machine nothing answers at: unreachable, said plainly, nothing crashes.
func (e *env) offlineMachine(t *testing.T) {
	e.mustHS(t, "hosts", "add", "off", e.offline)
	out, stderr, err := e.hs(t, "ls", "--host", "off", "--no-local", "--json")
	if Crashed(stderr) {
		t.Fatalf("ls crashed: %v\n%s", err, stderr)
	}
	m, ferr := FindMachine(out, "off")
	if ferr != nil || m.Status != "unreachable" || m.Hint == "" {
		t.Fatalf("an unreachable machine: %+v %v (exit %v)\n%s\n%s", m, ferr, err, out, stderr)
	}
	if st, code := e.status(t, "off", false); st.Reach.State != "error" || code != 3 {
		t.Fatalf("hosts status of an unreachable machine (exit %d): %+v", code, st)
	}
	doctor, stderr, err := e.hs(t, "doctor", "--all-machines", "--json")
	if Crashed(stderr) {
		t.Fatalf("doctor crashed: %v\n%s", err, stderr)
	}
	if ch, ferr := FindCheck(doctor, "off: ssh + sftp"); ferr != nil || ch.OK || !strings.HasPrefix(ch.Detail, "unreachable") {
		t.Fatalf("doctor --all-machines on an unreachable machine: %+v %v\n%s", ch, ferr, doctor)
	}
	_, stderr, err = e.hs(t, "terminals", "run", "off")
	if err == nil || Crashed(stderr) || strings.TrimSpace(stderr) == "" {
		t.Fatalf("terminals run on an unreachable machine: %v\n%s", err, stderr)
	}
}

// ----- helpers -----

var sshOptions = []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
	"-o", "LogLevel=ERROR", "-o", "ConnectTimeout=10"}

// remote runs a shell command line there and returns its output, trimmed.
func (e *env) remote(t *testing.T, line string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ssh", append(append([]string{}, sshOptions...), e.there, line)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (e *env) remoteWrite(t *testing.T, path string, data []byte) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	q := ShQuote(path)
	cmd := exec.CommandContext(ctx, "ssh", append(append([]string{}, sshOptions...), e.there,
		"mkdir -p \"$(dirname "+q+")\" && cat > "+q+".tmp && mv "+q+".tmp "+q)...)
	cmd.Stdin = bytes.NewReader(data)
	if out, err := cmd.CombinedOutput(); err != nil {
		return errors.New(err.Error() + ": " + string(out))
	}
	return nil
}

// hs runs hopsesh here, with its own folders and agents' folders.
func (e *env) hs(t *testing.T, args ...string) ([]byte, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.bin, args...)
	cmd.Env = append(os.Environ(), "HOPSESH_CONFIG_DIR="+e.config, "HOPSESH_STATE_DIR="+e.state, "HOPSESH_MACHINE=here",
		"CLAUDE_CONFIG_DIR="+filepath.Join(e.work, "claude"), "CODEX_HOME="+filepath.Join(e.work, "codex"), "HOPSESH_APP_RUNTIMES=off")
	cmd.Stdin = nil
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	return out.Bytes(), stderr.String(), err
}

func (e *env) mustHS(t *testing.T, args ...string) []byte {
	t.Helper()
	out, stderr, err := e.hs(t, args...)
	if err != nil {
		t.Fatalf("hopsesh %s: %v\n%s\n%s", strings.Join(args, " "), err, out, stderr)
	}
	return out
}

// status is what `hosts status <name> --json` says of a machine; exit 3 (it needs
// something) is an answer, not a failure.
func (e *env) status(t *testing.T, name string, refresh bool) (Status, int) {
	t.Helper()
	args := []string{"hosts", "status", name, "--json"}
	if refresh {
		args = append(args, "--refresh")
	}
	out, stderr, err := e.hs(t, args...)
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("hosts status: %v\n%s", err, stderr)
	}
	if Crashed(stderr) || code != 0 && code != 3 {
		t.Fatalf("hosts status exited %d:\n%s\n%s", code, out, stderr)
	}
	st, ferr := FindStatus(out, name)
	if ferr != nil {
		t.Fatal(ferr)
	}
	return st, code
}

func (e *env) machine(t *testing.T, name string) Machine {
	t.Helper()
	out := e.mustHS(t, "ls", "--host", name, "--no-local", "--no-git", "--json")
	m, err := FindMachine(out, name)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (e *env) kept(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(KeptEndpointPath(e.state, "box"))
	if err != nil {
		t.Fatalf("the identity kept for box: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// dial starts hopsesh peer there over plain ssh, as hopsesh here does.
func (e *env) dial(t *testing.T) (*peer.Client, func()) {
	t.Helper()
	cmd := exec.Command("ssh", append(append([]string{}, sshOptions...), e.there, `exec "$HOME/.hopsesh/bin/hopsesh" peer --stdio`)...)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return peer.NewClient(out, in), func() {
		_ = in.Close()
		done := make(chan error, 1)
		go func() { _, _ = io.Copy(io.Discard, out); done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
			t.Errorf("hopsesh peer there did not end with its input; stderr: %s", stderr.String())
		}
	}
}

func (e *env) hello(t *testing.T, c *peer.Client, hi peer.Hello) peer.HelloReply {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var hr peer.HelloReply
	if err := c.Call(ctx, peer.MethodHello, hi, &hr); err != nil {
		t.Fatalf("hello: %v", err)
	}
	return hr
}

func (e *env) wantNoLogin(t *testing.T, err error) {
	t.Helper()
	var pe *peer.Error
	if !errors.As(err, &pe) || pe.Code != peer.CodeNoLogin || !errors.Is(err, peer.ErrNoLogin) {
		t.Fatalf("a login method outside the login session: %v, want the %s error", err, peer.CodeNoLogin)
	}
}

// startServe runs hopsesh serve there in the background, as its login service would, and
// waits for its socket.
func (e *env) startServe(t *testing.T) {
	t.Helper()
	line := `cd ~ && (nohup "$HOME/.hopsesh/bin/hopsesh" serve > "$HOME/.hopsesh/serve.out" 2>&1 < /dev/null & echo $! > "$HOME/.hopsesh/serve.pid")`
	if out, err := e.remote(t, line); err != nil {
		t.Fatal(err, out)
	}
	for i := 0; i < 60; i++ {
		if out, _ := e.remote(t, `test -S ~/.hopsesh/run/serve.sock && echo up`); out == "up" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	log, _ := e.remote(t, `cat ~/.hopsesh/serve.out`)
	t.Fatalf("hopsesh serve did not listen there:\n%s", log)
}

// stopServe ends hopsesh serve there, however it was started, and waits until it is gone.
func (e *env) stopServe(t *testing.T) {
	t.Helper()
	_, _ = e.remote(t, `systemctl --user stop hopsesh.service >/dev/null 2>&1; f=~/.hopsesh/serve.pid; if [ -f "$f" ]; then kill "$(cat "$f")" 2>/dev/null; rm -f "$f"; fi; `+
		`i=0; while [ -S ~/.hopsesh/run/serve.sock ] && pgrep -u "$(id -u)" -f 'hopsesh serve' >/dev/null && [ $i -lt 40 ]; do sleep 0.25; i=$((i+1)); done; true`)
}
