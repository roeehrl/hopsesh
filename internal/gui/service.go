// Package gui is the backend of the hopsesh desktop app (Wails v3). The frontend in
// assets/ calls these methods; all work is done by the same engine as the CLI.
package gui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/hosts"
	"github.com/roeehrl/hopsesh/internal/core/link"
	"github.com/roeehrl/hopsesh/internal/core/lnp"
	"github.com/roeehrl/hopsesh/internal/core/moved"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/engine"
	"github.com/roeehrl/hopsesh/internal/inventory"
	"github.com/roeehrl/hopsesh/internal/update"
	"github.com/roeehrl/hopsesh/internal/version"
)

// App is the service bound to the frontend.
type App struct {
	mu       sync.Mutex
	cfg      config.Config
	log      *audit.Log
	machines []*inventory.Machine
	plan     *engine.Plan
	planSrc  engine.Source
	App      *application.App `json:"-"`
}

// NewApp loads configuration.
func NewApp() (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	log, _ := audit.Open(filepath.Join(config.StateDir(), "log"))
	return &App{cfg: cfg, log: log}, nil
}

// HostDTO is one machine on the consent screen.
type HostDTO struct {
	Name        string   `json:"name"`
	Destination string   `json:"destination"`
	Via         []string `json:"via"`
	OS          string   `json:"os"`
	Online      *bool    `json:"online"`
	OtherOwner  bool     `json:"otherOwner"`
	Owner       string   `json:"owner"`
	Allowed     bool     `json:"allowed"`
}

// Info is static information for the window.
type Info struct {
	Version   string `json:"version"`
	Host      string `json:"host"`
	ReposDir  string `json:"reposDir"`
	AuditDir  string `json:"auditDir"`
	HasHosts  bool   `json:"hasHosts"`
	ClaudeVer string `json:"claudeVersion"`
	// LocalNetwork describes macOS local network privacy: gated (macOS 15+) and firstRun
	// (the prompt has probably not been answered yet).
	LocalNetwork struct {
		Gated    bool `json:"gated"`
		FirstRun bool `json:"firstRun"`
	} `json:"localNetwork"`
	UpdateCheck string `json:"updateCheck"` // "", "on" or "off"
	// Defaults for the round-trip choices on the preflight screen.
	Defaults struct {
		MarkMoved  bool `json:"markMoved"`
		SyncCode   bool `json:"syncCode"`
		PushSource bool `json:"pushSource"`
	} `json:"defaults"`
}

// Info returns app and machine information.
func (a *App) Info() Info {
	a.mu.Lock()
	defer a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, cv := inventory.LocalClaude(ctx)
	has := false
	for _, h := range a.cfg.Hosts {
		has = has || h.Allowed
	}
	info := Info{Version: version.Version, Host: inventory.LocalHostName(), ReposDir: a.cfg.ReposDir,
		AuditDir: filepath.Join(config.StateDir(), "log"), HasHosts: has, ClaudeVer: cv}
	info.UpdateCheck = a.cfg.UpdateCheck
	info.Defaults.MarkMoved, info.Defaults.SyncCode, info.Defaults.PushSource = a.cfg.MarkMovedOn(), a.cfg.SyncCodeOn(), a.cfg.PushSource
	info.LocalNetwork.Gated = lnp.Gated()
	info.LocalNetwork.FirstRun = lnp.FirstRun(config.StateDir())
	return info
}

// SetUpdateCheck records whether the app may look for new releases once a day.
func (a *App) SetUpdateCheck(on bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.UpdateCheck = map[bool]string{true: "on", false: "off"}[on]
	return config.Save(a.cfg)
}

// UpdateDTO is the result of an update check.
type UpdateDTO struct {
	Latest string `json:"latest"`
	Newer  bool   `json:"newer"`
	URL    string `json:"url"`
}

// CheckUpdate looks for a newer release, at most once a day, and only when allowed.
func (a *App) CheckUpdate() (*UpdateDTO, error) {
	a.mu.Lock()
	on := a.cfg.UpdateCheck == "on"
	a.mu.Unlock()
	if !on {
		return &UpdateDTO{}, nil
	}
	stamp := filepath.Join(config.StateDir(), "update-check.json")
	var last struct {
		At  time.Time `json:"at"`
		DTO UpdateDTO `json:"result"`
	}
	if b, err := os.ReadFile(stamp); err == nil && json.Unmarshal(b, &last) == nil && time.Since(last.At) < 24*time.Hour {
		last.DTO.Newer = update.Newer(last.DTO.Latest, version.Version)
		return &last.DTO, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rel, err := update.Latest(ctx)
	if err != nil {
		return &UpdateDTO{}, nil // quiet: offline or nothing published
	}
	last.At, last.DTO = time.Now(), UpdateDTO{Latest: rel.Version, URL: rel.URL, Newer: update.Newer(rel.Version, version.Version)}
	if b, err := json.Marshal(last); err == nil {
		_ = os.WriteFile(stamp, b, 0o600)
	}
	return &last.DTO, nil
}

// OpenURL opens a web page in the default browser (release notes only).
func (a *App) OpenURL(url string) error {
	if !strings.HasPrefix(url, "https://github.com/"+update.Repo+"/") {
		return errors.New("only hopsesh release pages can be opened")
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Run()
	}
	return exec.Command("xdg-open", url).Run()
}

// OpenLocalNetworkSettings opens System Settings at Privacy & Security, where the Local
// Network list is (macOS offers no link to the list itself).
func (a *App) OpenLocalNetworkSettings() error {
	if runtime.GOOS != "darwin" {
		return errors.New("only macOS has a Local Network setting")
	}
	return exec.Command("open", lnp.SettingsURL).Run()
}

// RetryLocalNetwork makes the next scan wait for the macOS prompt again, for someone who
// just changed the setting or wants to answer a prompt they dismissed.
func (a *App) RetryLocalNetwork() {
	lnp.ResetWait(config.StateDir()) // each scan makes new connections, so nothing else is cached
}

// Discover lists machines (connecting to none) with their consent state.
func (a *App) Discover() []HostDTO {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cands, _ := hosts.Discover(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []HostDTO
	seen := map[string]bool{}
	for _, c := range cands {
		if c.Self {
			continue
		}
		d := HostDTO{Name: c.Name, Destination: c.Destination, Via: c.Via, OS: c.OS, Online: c.Online, OtherOwner: c.OtherOwner, Owner: c.Owner}
		if h := a.cfg.FindHost(c.Name); h != nil {
			d.Allowed = h.Allowed
			if h.TailscaleName == "" && c.DNSName != "" {
				h.TailscaleName = c.DNSName
			}
		}
		seen[c.Name] = true
		out = append(out, d)
	}
	for _, h := range a.cfg.Hosts {
		if !seen[h.Name] {
			out = append(out, HostDTO{Name: h.Name, Destination: h.Destination, Via: []string{h.Via}, OS: h.OS, Allowed: h.Allowed})
		}
	}
	return out
}

// SetAllowed records consent for a machine.
func (a *App) SetAllowed(name, destination string, allowed bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := a.cfg.FindHost(name)
	if h == nil {
		a.cfg.Hosts = append(a.cfg.Hosts, config.Host{Name: name, Destination: destination, Via: "gui"})
		h = a.cfg.FindHost(name)
	}
	h.Allowed = allowed
	return config.Save(a.cfg)
}

// AddHost adds and allows a machine by ssh destination.
func (a *App) AddHost(name, destination string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.UpsertHost(config.Host{Name: name, Destination: destination, Via: "manual", Allowed: true})
	return config.Save(a.cfg)
}

// KeysDTO describes host keys awaiting confirmation.
type KeysDTO struct {
	Host         string   `json:"host"`
	Address      string   `json:"address"`
	Fingerprints []string `json:"fingerprints"`
	Verified     string   `json:"verified"` // how it was verified, "" if not
}

func (a *App) conn(name string) (*transport.Conn, error) {
	h := a.cfg.FindHost(name)
	dest := name
	if h != nil {
		dest = h.Destination
	}
	c, err := transport.NewConn(dest, config.StateDir(), a.log)
	if err != nil {
		return nil, err
	}
	if h != nil && h.TailscaleName != "" && h.TailscaleName != dest {
		c.Fallbacks = []string{h.TailscaleName}
	}
	return c, nil
}

// ScanKeys fetches a machine's host keys for the trust dialog.
func (a *App) ScanKeys(name string) (*KeysDTO, error) {
	a.mu.Lock()
	c, err := a.conn(name)
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second) // room for the macOS prompt
	defer cancel()
	keys, r, err := c.ScanHostKeys(ctx)
	if err != nil {
		var ln *transport.LocalNetworkError
		if errors.As(err, &ln) {
			return nil, fmt.Errorf("%v. %s", err, ln.Hint())
		}
		return nil, err
	}
	d := &KeysDTO{Host: name, Address: r.HostName + ":" + r.Port}
	for _, k := range keys {
		d.Fingerprints = append(d.Fingerprints, k.Type+" "+k.Fingerprint)
	}
	cands, _ := hosts.Discover(ctx)
	for _, cd := range cands {
		if cd.Name == name && transport.MatchesTailscale(keys, cd.SSHHostKeys) {
			d.Verified = "matches the key Tailscale reports for this machine"
		}
	}
	if d.Verified == "" {
		home, _ := os.UserHomeDir()
		if k := transport.KnownElsewhere(keys, filepath.Join(home, ".ssh", "known_hosts")); len(k) > 0 {
			d.Verified = "matches a key you already trust for " + strings.Join(dedupe(k), ", ")
		}
	}
	return d, nil
}

// TrustHost re-scans and records the machine's host keys (after the user confirmed).
func (a *App) TrustHost(name string) error {
	a.mu.Lock()
	c, err := a.conn(name)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	keys, _, err := c.ScanHostKeys(ctx)
	if err != nil {
		return err
	}
	return c.Trust(keys)
}

// MachineDTO summarises one scanned machine.
type MachineDTO struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Hint     string `json:"hint"`
	Error    string `json:"error"`
	OS       string `json:"os"`
	Sessions int    `json:"sessions"`
	Local    bool   `json:"local"`
	Claude   string `json:"claude"`
}

// EntryDTO is one session row.
type EntryDTO struct {
	Machine    string `json:"machine"`
	ID         string `json:"id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Live       bool   `json:"live"`
	LastActive string `json:"lastActive"`
	LastPrompt string `json:"lastPrompt"`
	CWD        string `json:"cwd"`
	Branch     string `json:"branch"`
	Worktree   string `json:"worktree"`
	MainBranch string `json:"mainBranch"`
	Unpushed   int    `json:"unpushed"`
	Dirty      int    `json:"dirty"`
	SizeKB     int64  `json:"sizeKB"`
	// Copies of this session on other machines (it was moved); this entry is the newest.
	Copies    []inventory.Copy `json:"copies,omitempty"`
	HereNewer bool             `json:"hereNewest,omitempty"` // the newest copy is on this machine
	StaleHere bool             `json:"staleHere,omitempty"`  // an older copy is on this machine
	ResumeCmd string           `json:"resumeCommand,omitempty"`
}

// GroupDTO is one repository.
type GroupDTO struct {
	Name    string     `json:"name"`
	Remote  string     `json:"remote"`
	Local   string     `json:"local"`
	NoRepo  bool       `json:"noRepo"`
	Entries []EntryDTO `json:"entries"`
}

// ScanDTO is the result of a scan.
type ScanDTO struct {
	Machines []MachineDTO `json:"machines"`
	Groups   []GroupDTO   `json:"groups"`
	Total    int          `json:"total"`
}

// Scan scans this machine and every allowed machine.
func (a *App) Scan() (*ScanDTO, error) {
	a.mu.Lock()
	for _, m := range a.machines {
		m.Close()
	}
	a.machines = nil
	cfg := a.cfg
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	sc := &inventory.Scanner{StateDir: config.StateDir(), Log: a.log}
	ms := sc.Scan(ctx, cfg.Hosts, true)
	home, _ := os.UserHomeDir()
	roots := append([]string{cfg.ReposDir}, repos.DefaultRoots(home)...)
	groups := inventory.GroupByRepo(ms, roots)
	a.mu.Lock()
	a.machines = ms
	a.mu.Unlock()
	out := &ScanDTO{}
	for _, m := range ms {
		d := MachineDTO{Name: m.Name, Status: m.Status, Hint: m.Hint, Error: m.Error, Sessions: len(m.Sessions), Local: m.Local}
		if m.Facts != nil {
			d.OS, d.Claude = m.Facts.OS, m.Facts.ClaudeVersion
		}
		out.Machines = append(out.Machines, d)
		out.Total += len(m.Sessions)
	}
	for _, g := range groups {
		gd := GroupDTO{Name: g.Name, Remote: g.Remote, Local: g.Local, NoRepo: g.Identity == ""}
		for _, e := range g.Entries {
			s := e.Session
			ed := EntryDTO{Machine: e.Machine, ID: s.ID, Title: s.Title, Status: "ended", LastActive: s.LastActivity.Format(time.RFC3339),
				LastPrompt: s.LastPrompt, CWD: s.CWD, SizeKB: s.Size / 1024}
			if s.Live != nil {
				ed.Live, ed.Status = true, "live "+s.Live.Status
			}
			ed.Copies = e.Copies
			for _, c := range e.Copies {
				if c.Local && c.Newest {
					ed.HereNewer = true
				} else if c.Local {
					ed.StaleHere = true
				}
			}
			if ed.HereNewer {
				ed.ResumeCmd = link.Resume{Dir: s.CWD, SessionID: s.ID}.Shell(link.DefaultShell())
			}
			if gs := s.Git; gs != nil && gs.IsRepo {
				ed.Branch, ed.MainBranch = gs.Branch, gs.MainBranch
				switch {
				case gs.ClaudeWorktree:
					ed.Worktree = "Claude worktree"
				case gs.LinkedWorktree:
					ed.Worktree = "worktree"
				}
				ed.Unpushed, ed.Dirty = gs.LeftBehind()
			}
			gd.Entries = append(gd.Entries, ed)
		}
		out.Groups = append(out.Groups, gd)
	}
	return out, nil
}

// OptsDTO are the preflight choices.
type OptsDTO struct {
	TargetDir    string `json:"targetDir"`
	Clone        bool   `json:"clone"`
	ReposDir     string `json:"reposDir"`
	Worktree     string `json:"worktree"`
	Fork         bool   `json:"fork"`
	RemoteCtl    bool   `json:"remoteControl"`
	NotifyOld    bool   `json:"notifyOld"`
	Redact       bool   `json:"redact"`
	OtherAccount bool   `json:"otherAccount"`
	Memory       bool   `json:"memory"`
	MarkSource   bool   `json:"markSource"`
	SyncCode     bool   `json:"syncCode"`
	PushSource   bool   `json:"pushSource"`
	StopLocal    bool   `json:"stopLocal"`
}

// PlanDTO is the rendered plan.
type PlanDTO struct {
	Plan  *engine.Plan `json:"plan"`
	Kinds []string     `json:"kinds"`
}

// Plan builds a transport plan for a session (nothing changes).
func (a *App) Plan(machine, sessionID string, o OptsDTO) (*PlanDTO, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var m *inventory.Machine
	for _, mc := range a.machines {
		if mc.Name == machine {
			m = mc
		}
	}
	if m == nil {
		return nil, errors.New("scan first")
	}
	var sess *inventory.Session
	for i := range m.Sessions {
		if m.Sessions[i].ID == sessionID {
			sess = &m.Sessions[i]
		}
	}
	if sess == nil {
		return nil, errors.New("session not found; refresh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	tgt := inventory.LocalTarget(ctx)
	opt := engine.Options{TargetDir: o.TargetDir, Clone: o.Clone, ReposDir: nonEmpty(o.ReposDir, a.cfg.ReposDir), GHQLayout: a.cfg.Layout == "ghq",
		Worktree: engine.WorktreeMode(nonEmpty(o.Worktree, "auto")), Fork: o.Fork, RemoteCtl: o.RemoteCtl, NotifyOld: o.NotifyOld,
		Redact: o.Redact, DropThinking: o.OtherAccount, CopyMemory: o.Memory,
		MarkSource: o.MarkSource, SyncCode: o.SyncCode, PushSource: o.PushSource, StopLocal: o.StopLocal}
	src := m.PlanSource(ctx)
	p, err := engine.BuildPlan(ctx, src, tgt, engine.Input{Summary: &sess.Summary, Git: sess.Git, Live: sess.Live}, opt)
	if err != nil {
		return nil, err
	}
	a.plan, a.planSrc = p, src
	kinds := map[string]int{}
	for _, f := range p.Files {
		kinds[f.Kind]++
	}
	var ks []string
	for _, k := range []string{"transcript", "subagent", "tool-result", "file-history", "memory", "sidecar"} {
		if kinds[k] > 0 {
			ks = append(ks, fmt.Sprintf("%d %s", kinds[k], k))
		}
	}
	return &PlanDTO{Plan: p, Kinds: ks}, nil
}

// DoneDTO reports a finished transport.
type DoneDTO struct {
	Title       string   `json:"title"`
	Command     string   `json:"command"`
	Paths       int      `json:"paths"`
	Files       int      `json:"files"`
	Bytes       string   `json:"bytes"`
	Secrets     int      `json:"secrets"`
	Cloned      bool     `json:"cloned"`
	Worktree    string   `json:"worktree"`
	SessionID   string   `json:"sessionId"`
	OldNotice   string   `json:"oldNotice"`
	NewName     string   `json:"newName"`
	RemoteCtl   bool     `json:"remoteControl"`
	NotifyOld   bool     `json:"notifyOld"`
	SetAside    []string `json:"setAside"`
	AuditDir    string   `json:"auditDir"`
	TargetDir   string   `json:"targetDir"`
	PromptFile  string   `json:"promptFile"`
	SourceHost  string   `json:"sourceHost"`
	Desktop     bool     `json:"desktop"` // the installed claude can open it in the desktop app
	Stopped     int      `json:"stoppedPid,omitempty"`
	Pushed      string   `json:"pushed,omitempty"`
	PushError   string   `json:"pushError,omitempty"`
	SyncNote    string   `json:"syncNote,omitempty"`
	SyncState   string   `json:"syncState,omitempty"`
	Mark        string   `json:"mark"`
	MarkError   string   `json:"markError,omitempty"`
	MarkedTitle string   `json:"markedTitle,omitempty"`
	ResumeArgv  []string `json:"-"`
	ResumeIntoD string   `json:"-"`
}

// Apply performs the last plan.
func (a *App) Apply() (*DoneDTO, error) {
	a.mu.Lock()
	p, src := a.plan, a.planSrc
	a.mu.Unlock()
	if p == nil {
		return nil, errors.New("no plan")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	res, err := engine.Apply(ctx, p, src, engine.Env{StateDir: config.StateDir(), Log: a.log})
	if err != nil {
		return nil, err
	}
	n := 0
	for _, v := range res.Rewrite.Replacements {
		n += v
	}
	d := &DoneDTO{Title: p.Title, Command: p.Resume.Shell(link.DefaultShell()), Paths: n, Files: res.Copied, Bytes: engine.Human(res.Bytes),
		Secrets: res.Secrets.Total, Cloned: res.Cloned, Worktree: res.Worktree, SessionID: p.SessionID,
		OldNotice: link.OldSessionNotice(inventory.LocalHostName(), p.TargetCWD, p.NewName, p.Resume.Fork),
		NewName:   p.NewName, RemoteCtl: p.Options.RemoteCtl, NotifyOld: p.Options.NotifyOld, SetAside: res.SetAside,
		AuditDir: filepath.Join(config.StateDir(), "log"), TargetDir: p.TargetCWD, PromptFile: p.Resume.PromptFile, SourceHost: p.SourceHost,
		Desktop: inventory.ClaudeSupports(ctx, inventory.LocalFacts(ctx).ClaudePath, "--desktop"),
		Stopped: res.Stopped, Pushed: res.Pushed, PushError: res.PushError, SyncNote: res.SyncNote, Mark: res.Mark, MarkError: res.MarkError}
	if res.Sync != nil {
		d.SyncState = res.Sync.State
	}
	if res.Mark == "done" {
		d.MarkedTitle = moved.Title(p.StartContext.TargetHost, p.Title)
	}
	return d, nil
}

// OpenInDesktop opens the moved session in the Claude desktop app.
func (a *App) OpenInDesktop() error {
	a.mu.Lock()
	p := a.plan
	a.mu.Unlock()
	if p == nil {
		return errors.New("no session was moved")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	claude := inventory.LocalFacts(ctx).ClaudePath
	if !inventory.ClaudeSupports(ctx, claude, "--desktop") {
		return errors.New("the Claude Code installed here cannot open sessions in the desktop app; update it, or use Open in Terminal")
	}
	r := p.Resume
	r.Desktop = true
	argv := r.Argv()
	cmd := exec.Command(claude, argv[1:]...)
	cmd.Dir = p.TargetCWD
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// OpenInTerminal runs the resume command in a new terminal window.
func (a *App) OpenInTerminal(command string) error {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`tell application "Terminal"
	activate
	do script %s
end tell`, appleScriptString(command))
		return exec.Command("osascript", "-e", script).Run()
	case "windows":
		return exec.Command("cmd", "/c", "start", "powershell", "-NoExit", "-Command", command).Run()
	}
	for _, t := range [][]string{{"x-terminal-emulator", "-e"}, {"gnome-terminal", "--"}, {"konsole", "-e"}, {"xterm", "-e"}} {
		if _, err := exec.LookPath(t[0]); err == nil {
			return exec.Command(t[0], append(t[1:], "sh", "-c", command+"; exec $SHELL")...).Start()
		}
	}
	return errors.New("no terminal emulator found; copy the command instead")
}

// CopyText puts text on the clipboard.
func (a *App) CopyText(text string) bool {
	if a.App == nil {
		return false
	}
	return a.App.Clipboard.SetText(text)
}

// ChooseFolder asks for a directory.
func (a *App) ChooseFolder(title string) (string, error) {
	if a.App == nil {
		return "", errors.New("no app")
	}
	return a.App.Dialog.OpenFile().CanChooseDirectories(true).CanChooseFiles(false).SetTitle(title).PromptForSingleSelection()
}

// SetReposDir changes the clone folder.
func (a *App) SetReposDir(dir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.ReposDir = dir
	return config.Save(a.cfg)
}

// Undo reverses the latest transport of a session.
func (a *App) Undo(sessionID string) error {
	_, err := engine.Undo(config.StateDir(), sessionID, a.log)
	return err
}

// Shutdown closes connections.
func (a *App) Shutdown() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, m := range a.machines {
		m.Close()
	}
}

func appleScriptString(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
