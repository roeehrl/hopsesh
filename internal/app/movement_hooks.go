package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// MovementHookStatus reports installation, not vendor trust or actual delivery.
// Vendor/session/project policy can still disable an installed hook.
type MovementHookStatus struct {
	Agent        agent.ID `json:"agent"`
	Profile      string   `json:"profile"`
	ProfileLabel string   `json:"profileLabel,omitempty"`
	Path         string   `json:"path,omitempty"`
	Installed    bool     `json:"installed"`
	Enabled      bool     `json:"enabled"`
	Supported    bool     `json:"supported"`
	Reason       string   `json:"reason,omitempty"`
	Evidence     string   `json:"evidence,omitempty"`
	// Trust is the agent's own answer for agents that skip untrusted hooks (Codex): an
	// installed hook delivers nothing until the user trusts it there.
	Trust *agent.HookTrust `json:"trust,omitempty"`
	// Result is this item's outcome (HookInstalled ... HookAbsent); Reason says why for a
	// failed or unsupported one. One item's failure never hides the others.
	Result string `json:"result,omitempty"`
	// What the hook covers here: the agent's version, whether that is its CLI or the
	// runtime bundled in its desktop app (agent.RuntimeApp), and the desktop app's name.
	Version string `json:"version,omitempty"`
	Runtime string `json:"runtime,omitempty"`
	CLI     bool   `json:"cli,omitempty"`
	App     string `json:"app,omitempty"`
}

// Per-item hook results.
const (
	HookInstalled     = "installed"
	HookNeedsApproval = "needs-approval" // installed; the agent runs it only after the user trusts it there
	HookRemoved       = "removed"
	HookAbsent        = "absent"
	HookUnsupported   = "unsupported"
	HookFailed        = "failed"
)

// MovementHookErrors is the error a command-line action reports: every failed item, and
// for install every agent that cannot take the hook.
func MovementHookErrors(action string, statuses []MovementHookStatus) error {
	var errs []error
	for _, st := range statuses {
		if st.Result == HookFailed || action == "install" && st.Result == HookUnsupported {
			errs = append(errs, fmt.Errorf("%s: %s", st.Agent, st.Reason))
		}
	}
	return errors.Join(errs...)
}

// result is an item's outcome after action.
func (st *MovementHookStatus) result(action string, err error) {
	switch {
	case err != nil:
		st.Result, st.Reason = HookFailed, err.Error()
	case action == "remove" && !st.Installed:
		st.Result = HookRemoved
	case !st.Supported:
		st.Result = HookUnsupported
	case st.Installed && st.Trust != nil && (st.Trust.State == agent.HookNeedsReview || st.Trust.State == agent.HookDisabled):
		st.Result = HookNeedsApproval
	case st.Installed:
		st.Result = HookInstalled
	default:
		st.Result = HookAbsent
	}
}

// coverage describes what the hook covers on this install.
func (st *MovementHookStatus) coverage(in agent.Install) {
	st.Version, st.Runtime = in.Version, in.Runtime
	st.CLI = in.Binary != "" && in.Runtime == ""
	if strings.HasSuffix(in.Desktop, ".app") || strings.HasSuffix(strings.ToLower(in.Desktop), ".exe") {
		st.App = strings.TrimSuffix(strings.TrimSuffix(filepath.Base(in.Desktop), ".app"), ".exe")
	} else if in.Desktop != "" {
		st.App = "desktop"
	}
}

// MovementNoticeHooks manages hooks only on this machine. Each agent profile is one item
// with its own Result; the error is only for a request that could not be attempted. A nonempty profile is
// an exact registered ID (never a prefix, label, path, or remote registration).
// An empty profile selects all registered local profiles plus an unregistered
// default root. Empty id selects all enabled agents; a profile requires an agent.
// bin must be the absolute installed CLI executable, including when called by GUI.
func (a *App) MovementNoticeHooks(ctx context.Context, action string, id agent.ID, profile, bin string) ([]MovementHookStatus, error) {
	if action != "install" && action != "remove" && action != "status" {
		return nil, errors.New("use install, remove, or status")
	}
	if !filepath.IsAbs(bin) {
		return nil, errors.New("notice hook executable must be an absolute path")
	}
	if profile != "" && id == "" {
		return nil, errors.New("an exact profile requires an agent")
	}
	if action == "install" && !a.Cfg.MovementNoticesOn() {
		return nil, errors.New("movement notices are disabled in hopsesh settings")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	m := a.localMachine(ctx)
	defer m.Close()
	return a.movementNoticeHooks(ctx, m, action, id, profile, bin)
}

func (a *App) movementNoticeHooks(ctx context.Context, m *host.Machine, action string, id agent.ID, profile, bin string) ([]MovementHookStatus, error) {
	if !m.Local {
		return nil, errors.New("movement hooks are local only")
	}
	var out []MovementHookStatus
	for _, mod := range a.Modules() {
		if id != "" && mod.Spec().ID != id {
			continue
		}
		integrator, ok := mod.(agent.MovementHookIntegrator)
		if !ok {
			continue
		}
		h, err := m.For(ctx, mod.Spec(), agent.Install{}, nil)
		if err != nil {
			return out, err
		}
		in, err := mod.Detect(ctx, h)
		if err != nil {
			return out, err
		}
		installs, err := a.movementHookInstalls(ctx, m, mod, in, profile)
		if err != nil {
			if profile != "" {
				return out, err // the exact profile asked for is not this machine's
			}
			st := MovementHookStatus{Agent: mod.Spec().ID, Enabled: a.Cfg.MovementNoticesOn()}
			st.coverage(in)
			st.result(action, err)
			out = append(out, st)
			continue
		}
		defaultRoot, defaultRootErr := filepath.EvalSymlinks(in.Main())
		for _, in := range installs {

			hookHost, err := m.For(ctx, mod.Spec(), in, nil)
			if err != nil {
				return out, err
			}
			dirs, err := a.movementHookDirArgs()
			if err != nil {
				return out, err
			}
			hi := integrator.MovementHooks(hookHost, in, bin, dirs...)
			// Migrate only exact handlers generated by earlier installations. The
			// default root may have acquired a registered profile since setup.
			if hi.File != nil {
				legacy := []*agent.HookFile{integrator.MovementHooks(hookHost, in, bin).File}
				if defaultRootErr == nil && defaultRoot == in.Main() && in.Profile != nil {
					unregistered := in
					unregistered.Profile = nil
					legacy = append(legacy, integrator.MovementHooks(hookHost, unregistered, bin).File,
						integrator.MovementHooks(hookHost, unregistered, bin, dirs...).File)
				}
				hi.File = migratingMovementHookFile(hi.File, legacy)
			}
			st := MovementHookStatus{Agent: mod.Spec().ID, Profile: in.ProfileID(), ProfileLabel: profileLabel(in.Profile), Enabled: a.Cfg.MovementNoticesOn() && hi.DisabledReason == "", Supported: hi.Reason == "", Reason: strings.TrimSpace(hi.Reason + " " + hi.DisabledReason), Evidence: hi.Evidence}
			st.coverage(in)
			changed := false
			switch {
			case hi.Dir != nil:
				changed, err = movementHookDir(ctx, action, &st, in, hi.Dir)
			case hi.File != nil:
				err = movementHookFile(ctx, action, &st, in, hi.File)
			default:
				st.Supported = false
				st.result(action, nil)
				out = append(out, st)
				continue
			}
			if err == nil && st.Installed && st.Enabled {
				if tr, ok := mod.(agent.HookTrustReporter); ok {
					if command, e := agent.NoticeHookCommand(bin, mod.Spec().ID, in.ProfileID(), dirs...); e == nil {
						trust := tr.HookTrust(ctx, hookHost, in, []string{command})
						if changed && trust.State == agent.HookMissing {
							// The agent loads a changed hook folder (a plugin) on its own, a
							// moment later: what it said may be from before.
							trust = agent.HookTrust{State: agent.HookUnknown, Detail: strings.TrimSpace("hopsesh just installed the plugin; the agent usually loads it within seconds (check with hopsesh notices status). It said: " + trust.Detail)}
						}
						st.Trust = &trust
					}
				}
			}
			st.result(action, err)
			out = append(out, st)
		}
	}
	if len(out) == 0 {
		return out, errors.New("no matching local agent supports movement hooks")
	}
	return out, nil
}

// movementHookFile reads (and for install or remove, edits) a hook file shared with the
// agent's settings.
func movementHookFile(ctx context.Context, action string, st *MovementHookStatus, in agent.Install, f *agent.HookFile) error {
	st.Path = f.Path
	// A module may only choose a file directly in the root it names.
	if !f.In(in, agent.PathFor(runtime.GOOS)) {
		return errors.New("hook settings path is outside the selected root")
	}
	old, err := readMovementHookFile(st.Path)
	if err != nil {
		return err
	}
	st.Installed = f.Has(old)
	if _, err = f.Merge(old); err != nil { // validate shared structure even for status
		return err
	}
	if action == "status" || action == "install" && !st.Supported {
		return nil
	}
	edit := f.Merge
	if action == "remove" {
		edit = f.Remove
	}
	if err = editMovementHookFile(ctx, st.Path, edit); err != nil {
		return err
	}
	old, err = readMovementHookFile(st.Path)
	st.Installed = err == nil && f.Has(old)
	return err
}

// movementHookDir reads (and for install or remove, replaces) a hook folder hopsesh owns
// whole (an agent plugin), and reports whether it changed it. Changes hold the machine's
// hook lock, a file in the folder's staging folder, so two hopsesh never share the
// staging copies.
func movementHookDir(ctx context.Context, action string, st *MovementHookStatus, in agent.Install, d *agent.HookDir) (bool, error) {
	st.Path = d.Path
	pa := agent.PathFor(runtime.GOOS)
	// A module may only choose a folder inside the root it names, staged in that root.
	if !d.In(in, pa) {
		return false, errors.New("hook folder is outside the selected root")
	}
	fsys := localHookDirFS{}
	files, err := agent.ReadHookDir(fsys, pa, d)
	if err != nil {
		return false, err
	}
	st.Installed = d.Has(files)
	want, err := d.Merge(files)
	if err != nil {
		return false, err
	}
	interrupted := len(files) == 0 && agent.HookDirInterrupted(fsys, pa, d)
	if action == "status" || action == "install" && !st.Supported {
		switch {
		case interrupted:
			st.Reason = strings.TrimSpace(st.Reason + " An install or removal of the hook folder stopped halfway; install the hooks again to finish it.")
		case st.Installed && !hookFilesIn(files, want):
			st.Reason = strings.TrimSpace(st.Reason + " The hook folder holds hopsesh files other than this hopsesh's (an older hopsesh's, or edited); install the hooks again to replace them.")
		}
		return false, nil
	}
	if action == "remove" && len(files) == 0 && !interrupted {
		return false, nil // nothing there: no lock, no staging folder
	}
	unlock, err := lockHookDir(ctx, d.Staging)
	if err != nil {
		return false, err
	}
	defer unlock()
	var changed bool
	if action == "remove" {
		changed, err = agent.RemoveHookDir(fsys, pa, d)
	} else {
		changed, err = agent.InstallHookDir(fsys, pa, d)
	}
	if err != nil {
		return changed, err
	}
	files, err = agent.ReadHookDir(fsys, pa, d)
	st.Installed = err == nil && d.Has(files)
	return changed, err
}

// hookFilesIn reports whether every file of want is in have as it is in want.
func hookFilesIn(have, want map[string][]byte) bool {
	for name, b := range want {
		if got, ok := have[name]; !ok || !bytes.Equal(got, b) {
			return false
		}
	}
	return true
}

// lockHookDir takes the machine's hook lock for a hook folder staged in staging.
func lockHookDir(ctx context.Context, staging string) (func(), error) {
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return nil, fmt.Errorf("lock hook folder: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(staging, ".hopsesh-notice-lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock hook folder: %w", err)
	}
	// Never unlinked, as for hook files: concurrent writers must lock the same inode.
	for {
		if err = lockMovementHookFile(lock); err == nil {
			return func() { _ = lock.Close() }, nil
		}
		select {
		case <-ctx.Done():
			_ = lock.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// localHookDirFS is this machine's files, for hook folders. ReadDir describes a link as a
// link, so ReadHookDir refuses it rather than following it.
type localHookDirFS struct{}

func (localHookDirFS) ReadDir(p string) ([]fs.FileInfo, error) {
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]fs.FileInfo, 0, len(entries))
	for _, e := range entries {
		fi, err := e.Info() // Lstat
		if err != nil {
			return nil, err
		}
		out = append(out, fi)
	}
	return out, nil
}

func (localHookDirFS) ReadFile(p string, limit int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is too large", p)
	}
	return b, err
}

func (localHookDirFS) WriteFile(p string, b []byte, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, perm)
}

func (localHookDirFS) MkdirAll(p string) error  { return os.MkdirAll(p, 0o755) }
func (localHookDirFS) RemoveAll(p string) error { return os.RemoveAll(p) }

// Rename moves a folder. On Windows a file the agent's folder watcher or an antivirus
// holds open fails it for a moment, so it is tried again for up to two seconds.
func (localHookDirFS) Rename(from, to string) error {
	err := os.Rename(from, to)
	for i := 0; err != nil && runtime.GOOS == "windows" && i < 40 && !errors.Is(err, fs.ErrNotExist); i++ {
		time.Sleep(50 * time.Millisecond)
		err = os.Rename(from, to)
	}
	return err
}

// Include registered roots even if the environment's default is absent. The
// endpoint ID, not an alias or machine label, decides which registrations are local.
func (a *App) movementHookInstalls(ctx context.Context, m *host.Machine, mod agent.Module, base agent.Install, want string) ([]agent.Install, error) {
	if want != "" {
		in, err := a.movementHookProfile(ctx, m, mod, base, want)
		if err != nil {
			return nil, err
		}
		return []agent.Install{in}, nil
	}
	endpoint, err := m.ReadIdentity(ctx)
	if err != nil {
		return nil, err
	}
	ps, err := a.Accounts()
	if err != nil {
		return nil, err
	}
	var out []agent.Install
	seen := map[string]bool{}
	add := func(profile string) error {
		in, err := a.movementHookProfile(ctx, m, mod, base, profile)
		if err != nil {
			return err
		}
		root := in.Main()
		if !seen[root] {
			seen[root] = true
			out = append(out, in)
		}
		return nil
	}
	if base.Present {
		if err = add(""); err != nil {
			return nil, err
		}
	}
	for _, p := range ps {
		if endpoint != "" && p.Endpoint == endpoint && p.Agent == mod.Spec().ID {
			if err = add(p.ID); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// Resolve only known default and registered roots, without account probes,
// registration writes, session listing, or network access.
func (a *App) movementHookProfile(ctx context.Context, m *host.Machine, mod agent.Module, base agent.Install, want string) (agent.Install, error) {
	endpoint, err := m.ReadIdentity(ctx)
	if err != nil {
		return base, err
	}
	ps, err := a.Accounts()
	if err != nil {
		return base, err
	}
	root := base.Main()
	if want == "" {
		if !filepath.IsAbs(root) {
			return base, errors.New("default agent root is not absolute")
		}
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil {
			return base, fmt.Errorf("default agent root: %w", err)
		}
		root = canonical
	}
	var selected *agent.RuntimeProfile
	for _, p := range ps {
		if p.Agent != mod.Spec().ID || endpoint == "" || p.Endpoint != endpoint {
			continue
		}
		if want != "" && p.ID == want || want == "" && p.Root == root {
			copy := p
			selected = &copy
			break
		}
	}
	if want != "" && selected == nil {
		return base, errors.New("profile is not registered for this agent on this machine")
	}
	if selected != nil {
		root = selected.Root
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil || !filepath.IsAbs(root) || canonical != filepath.Clean(root) {
			return base, errors.New("registered profile root changed; register it again")
		}
	}
	st, err := os.Stat(root)
	if err != nil {
		return base, err
	}
	if !st.IsDir() {
		return base, errors.New("agent root is not a directory")
	}
	base.Profile = selected
	base.Accounts = mod.Spec().Accounts
	base = mod.Spec().PinMain(base, root)
	h, err := m.For(ctx, mod.Spec(), base, nil)
	if err != nil {
		return base, err
	}
	in, err := mod.Detect(ctx, h)
	if err != nil {
		return base, err
	}
	// Pin canonical root even when the unregistered default was a symlink.
	in.Roots[mod.Spec().MainRoot().Name] = root
	in.Profile = selected
	return in, nil
}

const movementHookFileLimit = 1 << 20

func readMovementHookFile(path string) ([]byte, error) {
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("hook settings must be a regular file, not a symlink")
	}
	if st.Size() > movementHookFileLimit {
		return nil, errors.New("hook settings exceed 1 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, movementHookFileLimit+1))
	if len(b) > movementHookFileLimit {
		return nil, errors.New("hook settings exceed 1 MiB")
	}
	return b, err
}

// Serialize hopsesh writers, avoid rewriting a no-op, and use a unique atomic
// replacement. Re-read immediately before replacement to detect outside edits.
func editMovementHookFile(ctx context.Context, path string, edit func([]byte) ([]byte, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".hopsesh-notice-lock", os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("lock hook settings: %w", err)
	}
	defer lock.Close()
	// Kernel locks are released if the bounded hook process is killed. Never
	// unlink this file: doing so would let concurrent writers lock different inodes.
	for {
		if err = lockMovementHookFile(lock); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}

	old, err := readMovementHookFile(path)
	if err != nil {
		return err
	}
	next, err := edit(old)
	if err != nil {
		return err
	}
	if bytes.Equal(old, next) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".hopsesh-notice-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(next); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	current, err := readMovementHookFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(old, current) {
		return errors.New("hook settings changed during update; retry")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// MovementNoticeIdentity comes from the same validated lookup as the notice
// text, or from the inventory notice itself. Delivery never reads scan caches.
type MovementNoticeIdentity struct {
	Operation string
	Status    string
}

// MovementNoticeClaim is an attempted delivery. Only Written, called after a
// complete successful stdout write, records supplied-to-hook evidence.
// Attempts never suppress retries; the token fences concurrent/resumed attempts.
type MovementNoticeClaim struct {
	file, token, digest string
	generation          uint64
}

type movementNoticeReceipt struct {
	Digest          string `json:"writtenDigest,omitempty"`
	AttemptedDigest string `json:"attemptedDigest,omitempty"`
	Attempt         string `json:"attempt,omitempty"`
	Generation      uint64 `json:"generation"`
}

func (c *MovementNoticeClaim) Written(ctx context.Context) error {
	if c == nil {
		return nil
	}
	return editMovementHookFile(ctx, c.file, func(old []byte) ([]byte, error) {
		var receipt movementNoticeReceipt
		if err := json.Unmarshal(old, &receipt); err != nil {
			return nil, err
		}
		if receipt.Attempt != c.token || receipt.Generation != c.generation {
			return old, nil
		}
		receipt.Digest = c.digest
		return json.Marshal(receipt)
	})
}

// ClaimMovementNotice starts a resume generation or prepares a changed prompt
// notice. An abandoned, failed, or timed-out attempt remains pending. Old receipts
// that predate write acknowledgments are deliberately not delivery evidence.
func (a *App) ClaimMovementNotice(ctx context.Context, id agent.ID, profile, sessionID, event, notice string, identity ...MovementNoticeIdentity) (*MovementNoticeClaim, error) {
	if event != "SessionStart" && event != "UserPromptSubmit" {
		return nil, errors.New("unsupported notice event")
	}
	if !a.Cfg.MovementNoticesOn() {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file := a.movementDeliveryPath(id, profile, sessionID)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return nil, err
	}
	digest := movementNoticeDeliveryDigest(notice, identity...)
	var claim *MovementNoticeClaim
	err := editMovementHookFile(ctx, file, func(old []byte) ([]byte, error) {
		var receipt movementNoticeReceipt
		_ = json.Unmarshal(old, &receipt)
		if event == "SessionStart" {
			receipt.Generation++
			receipt.Digest = ""
		}
		if notice == "" {
			receipt.Digest = ""
			receipt.Attempt = ""
			receipt.AttemptedDigest = ""
		} else if receipt.Digest != digest {
			receipt.Attempt = rand.Text()
			receipt.AttemptedDigest = digest
			claim = &MovementNoticeClaim{file: file, token: receipt.Attempt, digest: digest, generation: receipt.Generation}
		}
		return json.Marshal(receipt)
	})
	return claim, err
}

// Pin both directories even when they came from platform defaults: the agent's
// launching environment may differ from the GUI or setup terminal.
func (a *App) movementHookDirArgs() ([]string, error) {
	cfg, err := filepath.Abs(config.Dir())
	if err != nil {
		return nil, err
	}
	state, err := filepath.Abs(a.StateDir)
	if err != nil {
		return nil, err
	}
	return []string{"--config-dir", cfg, "--state-dir", state}, nil
}

func migratingMovementHookFile(current *agent.HookFile, legacy []*agent.HookFile) *agent.HookFile {
	clean := func(b []byte) ([]byte, error) {
		var err error
		for _, old := range legacy {
			if old != nil {
				b, err = old.Remove(b)
				if err != nil {
					return nil, err
				}
			}
		}
		return b, nil
	}
	merge := func(b []byte) ([]byte, error) {
		b, err := clean(b)
		if err != nil {
			return nil, err
		}
		return current.Merge(b)
	}
	remove := func(b []byte) ([]byte, error) {
		b, err := clean(b)
		if err != nil {
			return nil, err
		}
		return current.Remove(b)
	}
	return &agent.HookFile{Path: current.Path, Merge: merge, Remove: remove, Has: func(b []byte) bool {
		cleaned, err := clean(b)
		return err == nil && bytes.Equal(b, cleaned) && current.Has(b)
	}}
}

// NoticeHooks is the GUI's local hook setup status. Installed means the exact
// current integration is present; it does not imply vendor trust or delivery.
func (a *App) NoticeHooks(ctx context.Context) ([]MovementHookStatus, error) {
	return a.NoticeHooksFor(ctx, "", "")
}

// NoticeHooksFor reports one agent's (or every agent's) hooks, as installed by this app.
func (a *App) NoticeHooksFor(ctx context.Context, id agent.ID, profile string) ([]MovementHookStatus, error) {
	bin, err := movementHookExecutable()
	if err != nil {
		// Without the command no hook can run: report every agent as not installed (and
		// whether it could take the hook) against a command that is never written.
		bin = filepath.Join(os.TempDir(), "hopsesh-command-not-installed")
	}
	return a.MovementNoticeHooks(ctx, "status", id, profile, bin)
}

// InstallNoticeHooks is an explicit setup action; defaults being on does not
// bypass the user's/vendor's hook trust flow. Empty selectors cover all local profiles.
func (a *App) InstallNoticeHooks(ctx context.Context, id agent.ID, profile string) ([]MovementHookStatus, error) {
	bin, err := movementHookExecutable()
	if err != nil {
		return nil, err
	}
	return a.MovementNoticeHooks(ctx, "install", id, profile, bin)
}

func (a *App) RemoveNoticeHooks(ctx context.Context, id agent.ID, profile string) ([]MovementHookStatus, error) {
	bin, err := movementHookExecutable()
	if err != nil {
		return nil, err
	}
	return a.MovementNoticeHooks(ctx, "remove", id, profile, bin)
}

// CLIExecutable is the hopsesh command-line program on this machine (the app's own when
// in the app).
func CLIExecutable() (string, error) { return movementHookExecutable() }

func movementHookExecutable() (string, error) {
	// Prefer the bundled CLI when in the GUI. Never install a hook that starts
	// hopsesh-app, and never source a login shell to discover the executable.
	if bin, err := integrate.AppCLI(); err == nil {
		return bin, nil
	}
	// The command is usually a link (~/.local/bin/hopsesh) to the app's bundled binary.
	// Hooks name the binary itself, so a link resolves to it: otherwise status reports
	// the app's hooks absent and install would add a second set.
	if bin, err := os.Executable(); err == nil {
		name := strings.ToLower(filepath.Base(bin))
		if name == "hopsesh" || name == "hopsesh.exe" {
			return realExecutable(bin), nil
		}
	}
	if bin, err := exec.LookPath("hopsesh"); err == nil {
		abs, err := filepath.Abs(bin)
		if err != nil {
			return "", err
		}
		return realExecutable(abs), nil
	}
	return "", errors.New("install the hopsesh command before installing the protection hooks")
}

func realExecutable(bin string) string {
	if real, err := filepath.EvalSymlinks(bin); err == nil {
		return real
	}
	return bin
}

func (a *App) movementDeliveryPath(id agent.ID, profile, sessionID string) string {
	key, _ := json.Marshal([]string{string(id), profile, sessionID})
	sum := sha256.Sum256(key)
	return filepath.Join(a.StateDir, "movement-hook-delivery", hex.EncodeToString(sum[:])+".json")
}

// MovementNoticeDelivery is evidence that this exact notice was supplied to the
// lifecycle hook. It never means the model or user read it. Read-only: no new
// directories, receipts, account probes, or scan, even when nothing was delivered.
func (a *App) MovementNoticeDelivery(id agent.ID, profile, sessionID, notice string, identity ...MovementNoticeIdentity) bool {
	if notice == "" {
		return false
	}
	b, err := readMovementHookFile(a.movementDeliveryPath(id, profile, sessionID))
	if err != nil {
		return false
	}
	var receipt movementNoticeReceipt
	if json.Unmarshal(b, &receipt) != nil {
		return false
	}
	return receipt.Digest == movementNoticeDeliveryDigest(notice, identity...)
}

// movementNoticeDeliveryDigest binds identical words to their operation/status.
// Identity must come from the validated notice, never a possibly absent or stale
// scan cache. Observation timestamps and whole sidecar digests are not delivery
// events. Callers without lineage identity retain text-only compatibility.
func movementNoticeDeliveryDigest(notice string, identity ...MovementNoticeIdentity) string {
	payload := []byte(notice)
	if notice != "" && len(identity) > 0 && identity[0].Operation != "" {
		payload, _ = json.Marshal([]string{"movement-hook-v1", identity[0].Operation, identity[0].Status, notice})
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
