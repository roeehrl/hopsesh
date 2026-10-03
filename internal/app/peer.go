package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/version"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// peerScript starts hopsesh peer on a Unix-like machine, wherever hopsesh is installed
// (a non-interactive SSH session often lacks ~/.local/bin on its PATH).
const peerScript = `for c in hopsesh "$HOME/.local/bin/hopsesh" /opt/homebrew/bin/hopsesh /usr/local/bin/hopsesh /Applications/hopsesh.app/Contents/Resources/bin/hopsesh; do
  if command -v "$c" >/dev/null 2>&1; then exec "$c" peer --stdio; fi
done
echo "hopsesh is not installed" >&2
exit 127`

// peerFindWindows prints where hopsesh.exe is on a Windows machine (its PATH, else where
// install.ps1 puts it) and the shell its OpenSSH server runs commands with.
const peerFindWindows = `$c = Get-Command hopsesh.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
$p = if ($c) { $c.Source } else { Join-Path $env:LOCALAPPDATA 'Programs\hopsesh\hopsesh.exe' }
if (-not (Test-Path -LiteralPath $p)) { [Console]::Error.WriteLine('hopsesh is not installed'); exit 127 }
$sh = (Get-ItemProperty -Path 'HKLM:\SOFTWARE\OpenSSH' -Name DefaultShell -ErrorAction SilentlyContinue).DefaultShell
Write-Output $p
Write-Output $sh`

// peerCommandWindows is the command line that starts hopsesh peer on a Windows machine,
// in the form its ssh shell takes: cmd.exe (the default), PowerShell, or a bash (Git Bash,
// MSYS2). The program runs directly, so its input and output are the connection's.
func peerCommandWindows(exe, shell string) string {
	s := strings.ToLower(shell)
	switch {
	case strings.Contains(s, "powershell") || strings.Contains(s, "pwsh"):
		return "& " + transport.PSQuote(exe) + " peer --stdio"
	case strings.HasSuffix(s, "bash.exe") || strings.HasSuffix(s, `\sh.exe`):
		return transport.ShQuote(strings.ReplaceAll(exe, `\`, "/")) + " peer --stdio" // C:/… works there
	}
	return `""` + exe + `" peer --stdio"` // cmd /c strips the outer quotes
}

// maxPackage bounds the files of one pushed session.
const maxPackage = 2 << 30

// ----- receiving -----

// Serve answers another machine's hopsesh on r and w until r ends (hopsesh peer --stdio).
// It receives sessions only when this machine opted in ([peer] receive = true).
func (a *App) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	s := &peerSession{a: a}
	return peer.Serve(ctx, r, w, s.handle)
}

// peerSession is one conversation with another machine's hopsesh.
type peerSession struct {
	a     *App
	snap  *host.Machine
	from  string
	plan  *move.Plan
	input move.Input
}

func (s *peerSession) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method != peer.MethodHello && !s.a.Cfg.Peer.Receive {
		return nil, peer.Refused(LocalName())
	}
	switch method {
	case peer.MethodHello:
		var hi peer.Hello
		if err := json.Unmarshal(params, &hi); err != nil {
			return nil, err
		}
		s.from = hi.From
		return s.hello(ctx), nil
	case peer.MethodPlan:
		var req peer.PlanRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		return s.planReceive(ctx, req)
	case peer.MethodApply:
		return s.applyReceive(ctx)
	case peer.MethodUndo:
		var req peer.UndoRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		_, err := s.a.Undo(ctx, req.Journal, req.Force)
		return struct{}{}, err
	}
	return nil, fmt.Errorf("unknown request %q", method)
}

func (s *peerSession) hello(ctx context.Context) peer.HelloReply {
	m := s.a.localMachine(ctx)
	r := peer.HelloReply{Protocol: peer.Protocol, Version: version.Version, Machine: m.Name, OS: m.Facts.OS, Receive: s.a.Cfg.Peer.Receive, Agents: []peer.AgentInfo{}}
	for _, mod := range s.a.Modules() {
		spec := mod.Spec()
		in := agent.Install{}
		if h, err := m.For(ctx, spec, agent.Install{}, nil); err == nil {
			in, _ = mod.Detect(ctx, h)
		}
		r.Agents = append(r.Agents, peer.AgentInfo{ID: spec.ID, Name: spec.Name, Version: in.Version, Present: in.Present, Write: agent.Has(mod, agent.CapWrite)})
	}
	return r
}

// receiveOptions are the sender's choices on top of this machine's own defaults (where
// repositories go is this machine's business).
func (a *App) receiveOptions(o move.Options) move.Options {
	d := a.DefaultOptions()
	d.TargetDir, d.Clone, d.Worktree = o.TargetDir, o.Clone, o.Worktree
	d.Fork, d.RemoteControl, d.Notify, d.Redact = o.Fork, o.RemoteControl, o.Notify, o.Redact
	d.Mark, d.SyncCode, d.StopLocal, d.Conflict = o.Mark, o.SyncCode, o.StopLocal, o.Conflict
	d.Fidelity, d.Native, d.Note, d.Go, d.CarryRules, d.Via = o.Fidelity, o.Native, o.Note, o.Go, o.CarryRules, o.Via
	return d // never Push: the sender pushed before sending, if asked to
}

func (s *peerSession) planReceive(ctx context.Context, req peer.PlanRequest) (*peer.PlanReply, error) {
	pkg := req.Package
	if pkg.Location == "" || strings.EqualFold(pkg.Location, LocalName()) {
		return nil, errors.New("the session is already on this machine")
	}
	mod, ok := s.a.Module(pkg.Agent)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not enabled on %s", agent.ErrUnsupported, pkg.Agent, LocalName())
	}
	var size int64
	for _, f := range pkg.Files {
		size += int64(len(f.Data))
	}
	if size > maxPackage {
		return nil, fmt.Errorf("the session's files are larger than %s", move.Human(maxPackage))
	}
	s.snap = host.NewSnapshot(pkg.Location, host.Facts{OS: pkg.Facts.OS, Arch: pkg.Facts.Arch, Home: pkg.Facts.Home}, pkg.Files)
	inv := s.a.Scan(ctx, ScanOptions{Hosts: []string{LocalName()}})
	in := pkg.Install
	in.Present = true
	src := &Machine{Name: pkg.Location, Status: StatusOK, OS: pkg.Facts.OS, host: s.snap, account: pkg.Account,
		Agents: []AgentState{{Agent: pkg.Agent, Name: mod.Spec().Name, Install: in}}}
	e := Entry{Machine: pkg.Location, Agent: pkg.Agent, AgentName: mod.Spec().Name, Session: pkg.Session, Live: pkg.Live, Git: pkg.Git, Lineage: pkg.Lineage}
	inv.Machines = append(inv.Machines, src)
	inv.Entries = append(inv.Entries, e)
	p, input, err := s.a.Plan(ctx, inv, e, req.Target, s.a.receiveOptions(req.Options))
	if err != nil {
		return nil, err
	}
	s.plan, s.input = p, input
	s.a.Audit.Write(audit.Entry{Action: "peer.plan", Host: pkg.Location, Session: pkg.Session.Key.String(), Detail: map[string]any{"kind": p.Kind, "blockers": len(p.Blockers)}})
	return &peer.PlanReply{Plan: p}, nil
}

func (s *peerSession) applyReceive(ctx context.Context) (*peer.ApplyReply, error) {
	if s.plan == nil {
		return nil, errors.New("plan first")
	}
	res, err := s.a.Apply(ctx, s.plan, s.input, nil)
	if err != nil {
		return nil, err
	}
	// The sender journals the writes meant for its copy when it replays them.
	if j, err := journal.Load(s.a.StateDir, res.Journal); err == nil {
		_ = j.Forget(s.snap.Name)
	}
	reply := &peer.ApplyReply{Result: res, Writes: s.snap.Writes(), Journal: res.Journal}
	s.a.Audit.Write(audit.Entry{Action: "peer.apply", Host: s.snap.Name, Session: s.plan.Key.String(), Detail: map[string]any{"journal": res.Journal}})
	s.plan = nil
	return reply, nil
}

// ----- sending -----

// Push is a session on this machine on its way to another machine's hopsesh: planned
// there, not yet carried out.
type Push struct {
	Plan   *move.Plan
	Peer   peer.HelloReply
	Pushed string // the branch was pushed first ("" when not)

	a      *App
	to     config.Host
	e      Entry
	client *peer.Client
	close  func()
}

// PushResult is a finished push.
type PushResult struct {
	Result  *move.Result `json:"result"`
	Journal string       `json:"journal"` // undo here undoes both machines
	Machine string       `json:"machine"`
	Pushed  string       `json:"pushed,omitempty"`
}

// StartPush connects to hopsesh on another machine and has it plan receiving a session
// from this machine, in its own agent or (target) another. Nothing changes until Commit.
func (a *App) StartPush(ctx context.Context, inv *Inventory, e Entry, to config.Host, target agent.ID, opt move.Options) (*Push, error) {
	here := inv.Local()
	if here == nil || e.Machine != here.Name {
		return nil, errors.New("only a session on this machine can be pushed; to bring one here, pull it")
	}
	c, hr, closeFn, err := a.dialPeer(ctx, to)
	if err != nil {
		return nil, err
	}
	p := &Push{a: a, to: to, e: e, client: c, Peer: hr, close: closeFn}
	if !hr.Receive {
		p.Close()
		return nil, peer.Refused(to.Name)
	}
	if target == "" {
		target = e.Agent
	}
	var ai *peer.AgentInfo
	for i := range p.Peer.Agents {
		if p.Peer.Agents[i].ID == target {
			ai = &p.Peer.Agents[i]
		}
	}
	switch {
	case ai == nil:
		p.Close()
		return nil, fmt.Errorf("hopsesh on %s does not know %s", to.Name, target)
	case !ai.Present:
		p.Close()
		return nil, fmt.Errorf("%w: %s has no data folder on %s yet; start it there once, then try again", agent.ErrNotInstalled, ai.Name, to.Name)
	}
	if opt.Push && e.Git != nil && e.Git.IsRepo {
		if up, _ := e.Git.LeftBehind(); up > 0 {
			out, err := pushFunc(here)(ctx, e.Session.CWD)
			if err != nil {
				p.Close()
				return nil, fmt.Errorf("pushing %s first: %w", e.Git.Branch, err)
			}
			p.Pushed = out
			if states, err := here.host.GitProbe(ctx, []string{e.Session.CWD}, a.Reg.Worktrees()); err == nil && len(states) == 1 {
				p.e.Git = &states[0]
			}
		}
	}
	pkg, err := a.packageOf(ctx, here, p.e)
	if err != nil {
		p.Close()
		return nil, err
	}
	var reply peer.PlanReply
	if err := p.client.Call(ctx, peer.MethodPlan, peer.PlanRequest{Package: pkg, Target: target, Options: opt}, &reply); err != nil {
		p.Close()
		return nil, err
	}
	p.Plan = reply.Plan
	return p, nil
}

// PeerConn is a connection to hopsesh on another machine: its output, its input, and how
// to end it (stderr is what it reported, for errors).
type PeerConn struct {
	Out    io.Reader
	In     io.Writer
	Close  func()
	Stderr func() string
}

// dialPeer starts hopsesh peer on a configured machine (over SSH, or a.PeerDial) and says
// hello.
func (a *App) dialPeer(ctx context.Context, to config.Host) (*peer.Client, peer.HelloReply, func(), error) {
	var hr peer.HelloReply
	dial := a.PeerDial
	if dial == nil {
		dial = a.sshPeer
	}
	pc, err := dial(ctx, to)
	if err != nil {
		return nil, hr, nil, err
	}
	c := peer.NewClient(pc.Out, pc.In)
	err = c.Call(ctx, peer.MethodHello, peer.Hello{Protocol: peer.Protocol, Version: version.Version, From: LocalName()}, &hr)
	if err == nil {
		return c, hr, pc.Close, nil
	}
	stderr := ""
	if pc.Stderr != nil {
		stderr = pc.Stderr()
	}
	pc.Close()
	switch {
	case strings.Contains(stderr, "hopsesh is not installed"):
		return nil, hr, nil, fmt.Errorf("hopsesh is not installed on %s; install it there to send sessions to it", to.Name)
	case strings.Contains(stderr, `unknown command "peer"`):
		return nil, hr, nil, fmt.Errorf("hopsesh on %s is too old to work with this one; update it there", to.Name)
	case errors.Is(err, peer.ErrProtocol):
		return nil, hr, nil, fmt.Errorf("%w (on %s)", err, to.Name)
	case strings.TrimSpace(stderr) != "":
		return nil, hr, nil, fmt.Errorf("hopsesh on %s: %s", to.Name, strings.SplitN(strings.TrimSpace(stderr), "\n", 2)[0])
	}
	return nil, hr, nil, err
}

// sshPeer runs hopsesh peer on a machine over SSH.
func (a *App) sshPeer(ctx context.Context, to config.Host) (*PeerConn, error) {
	m, err := a.Connect(ctx, to)
	if err != nil {
		return nil, err
	}
	line := "sh -c " + transport.ShQuote(peerScript)
	if m.Facts.OS == "windows" {
		out, err := m.Conn.RunPowerShell(ctx, peerFindWindows)
		if err != nil {
			m.Close()
			var re *transport.RemoteError
			if errors.As(err, &re) && re.Code == 127 {
				return nil, fmt.Errorf("hopsesh is not installed on %s; install it there to send sessions to it", to.Name)
			}
			return nil, err
		}
		f := strings.SplitN(strings.ReplaceAll(strings.TrimSpace(string(out)), "\r", ""), "\n", 2)
		shell := ""
		if len(f) == 2 {
			shell = strings.TrimSpace(f[1])
		}
		line = peerCommandWindows(strings.TrimSpace(f[0]), shell)
	}
	pipe, err := m.Conn.StartPipe(ctx, line)
	if err != nil {
		m.Close()
		return nil, err
	}
	return &PeerConn{Out: pipe.Out, In: pipe.In, Stderr: pipe.Stderr, Close: func() { _ = pipe.Close(); m.Close() }}, nil
}

// packageOf collects a local session's files for a peer: exactly its bundle.
func (a *App) packageOf(ctx context.Context, here *Machine, e Entry) (peer.Package, error) {
	mod, ok := a.Module(e.Agent)
	if !ok {
		return peer.Package{}, fmt.Errorf("%s is not enabled", e.Agent)
	}
	in, ok := here.Install(e.Agent)
	if !ok {
		return peer.Package{}, fmt.Errorf("%w: %s", agent.ErrNotInstalled, mod.Spec().Name)
	}
	h, err := here.host.For(ctx, mod.Spec(), in, nil)
	if err != nil {
		return peer.Package{}, err
	}
	b, err := mod.Bundle(ctx, h, in, e.Session)
	if err != nil {
		return peer.Package{}, err
	}
	f := here.host.Facts
	pkg := peer.Package{Location: here.Name, Facts: host.Facts{OS: f.OS, Arch: f.Arch, Home: f.Home}, Agent: e.Agent, Install: in,
		Session: e.Session, Live: e.Live, Git: e.Git, Lineage: e.Lineage, Account: a.account(ctx, here, mod, in)}
	var size int64
	for _, bf := range b.Files {
		p := h.Path().Join(in.Root(bf.Root), bf.Rel)
		fi, err := h.FS().Stat(p)
		if err != nil {
			return peer.Package{}, err
		}
		if size += fi.Size(); size > maxPackage {
			return peer.Package{}, fmt.Errorf("the session's files are larger than %s", move.Human(maxPackage))
		}
		data, err := h.FS().ReadFile(p, fi.Size()+1<<20) // it may still grow while open
		if err != nil {
			return peer.Package{}, err
		}
		pkg.Files = append(pkg.Files, host.SnapshotFile{Path: p, Data: data, Mode: fi.Mode().Perm(), ModTime: fi.ModTime()})
	}
	// The user's instructions for every project, for --carry-rules there.
	for _, g := range mod.Spec().GlobalInstructions {
		p := agent.Expand(g, h.Facts().Home, in.Roots, h.Path())
		if data, err := h.FS().ReadFile(p, 1<<20); err == nil {
			pkg.Files = append(pkg.Files, host.SnapshotFile{Path: p, Data: data, Mode: 0o600})
		}
	}
	return pkg, nil
}

// Commit carries out the push: the other machine installs or converts the session, then
// this machine records, in its own journal, what changes for its copy (the mark and the
// lineage), and keeps a mark owed while the copy here is still open.
func (p *Push) Commit(ctx context.Context) (*PushResult, error) {
	var reply peer.ApplyReply
	if err := p.client.Call(ctx, peer.MethodApply, struct{}{}, &reply); err != nil {
		return nil, err
	}
	a, e := p.a, p.e
	out := &PushResult{Result: reply.Result, Machine: p.to.Name, Pushed: p.Pushed}
	j, err := journal.New(a.StateDir, journal.KindPush, fmt.Sprintf("%s to %s", e.Session.Title, p.to.Name))
	if err != nil {
		return out, err
	}
	out.Journal = j.ID
	j.AddKey(e.Session.Key)
	if err := j.AddRemote(p.to.Name, reply.Journal); err != nil {
		return out, err
	}
	if err := a.replay(ctx, e, j, reply.Writes); err != nil {
		reply.Result.Warnings = append(reply.Result.Warnings, "could not update the copy here: "+err.Error())
	}
	if err := j.Seal(func(string) (host.FS, error) { return host.LocalFS(), nil }); err != nil {
		reply.Result.Warnings = append(reply.Result.Warnings, "could not record what this changed here, for a safe undo: "+err.Error())
	}
	if o := reply.Result.Owed; o != nil {
		if err := lineage.AddPending(a.StateDir, *o); err != nil {
			reply.Result.Mark, reply.Result.MarkError = "failed", err.Error()
		}
	}
	a.Audit.Write(audit.Entry{Action: "push", Host: p.to.Name, Session: e.Session.Key.String(), Detail: map[string]any{"journal": j.ID, "remote": reply.Journal, "writes": len(reply.Writes)}})
	return out, nil
}

// replay applies the writes another machine made to its snapshot of a session here,
// confined like the module's own writes (inside the agent's folders, never its secrets).
func (a *App) replay(ctx context.Context, e Entry, j *journal.Journal, ws []host.SnapshotWrite) error {
	if len(ws) == 0 {
		return nil
	}
	mod, ok := a.Module(e.Agent)
	if !ok {
		return fmt.Errorf("%s is not enabled", e.Agent)
	}
	m := a.localMachine(ctx)
	h0, err := m.For(ctx, mod.Spec(), agent.Install{}, nil)
	if err != nil {
		return err
	}
	in, err := mod.Detect(ctx, h0)
	if err != nil {
		return err
	}
	h, err := m.For(ctx, mod.Spec(), in, j)
	if err != nil {
		return err
	}
	for _, w := range ws {
		perm := w.Perm
		if perm == 0 {
			perm = 0o600
		}
		switch w.Op {
		case "write":
			err = h.FS().WriteFile(w.Path, w.Data, perm)
		case "append":
			err = h.FS().Append(w.Path, w.Data, w.Append)
		case "rename":
			err = h.FS().Rename(w.From, w.Path)
		default:
			err = fmt.Errorf("unknown write %q", w.Op)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", w.Path, err)
		}
	}
	return nil
}

// Close ends the connection (an uncommitted push changes nothing).
func (p *Push) Close() {
	if p.close != nil {
		p.close()
		p.close = nil
	}
}

// undoRemote undoes a journal another machine's hopsesh keeps. One already undone there
// counts as done.
func (a *App) undoRemote(ctx context.Context, r journal.Remote, force bool) error {
	h := a.Cfg.FindHost(r.Machine)
	if h == nil {
		return fmt.Errorf("%s is not a configured machine", r.Machine)
	}
	c, _, closeFn, err := a.dialPeer(ctx, *h)
	if err != nil {
		return err
	}
	defer closeFn()
	err = c.Call(ctx, peer.MethodUndo, peer.UndoRequest{Journal: r.ID, Force: force}, nil)
	if err != nil && strings.HasPrefix(err.Error(), errNothingToUndo) {
		return nil
	}
	return err
}
