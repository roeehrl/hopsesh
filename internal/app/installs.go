package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/installs"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/internal/version"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Every hopsesh on a machine (installs.List) and bringing them to one version: setting a
// machine up or updating it updates each copy hopsesh may replace there, the app with its
// own updater, and runs the machine's own hopsesh at login; a copy a package manager owns
// is left to it, with the command that updates it.

// remoteInstalls lists hopsesh on m.
func (a *App) remoteInstalls(ctx context.Context, m *host.Machine) (installs.List, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out []byte
	var err error
	if m.Facts.OS == "windows" {
		out, err = m.Conn.RunPowerShell(ctx, installs.Script("windows"))
	} else {
		term := ""
		if r, e := m.Exec().Run(ctx, []string{"sh", "-c", loginWhich}, agent.RunOptions{Timeout: 10 * time.Second}); e == nil {
			term = lastPath(string(r.Stdout))
		}
		out, err = m.Conn.RunSh(ctx, installs.Script(m.Facts.OS), term)
	}
	if err != nil {
		return installs.List{}, fmt.Errorf("could not list hopsesh on %s: %w", m.Name, err)
	}
	return installs.Parse(out, m.Facts.OS, m.Facts.Arch, m.Facts.Home), nil
}

// LocalInstalls lists hopsesh on this machine.
func LocalInstalls(ctx context.Context) installs.List {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	home, _ := os.UserHomeDir()
	var out []byte
	if runtime.GOOS == "windows" {
		out, _ = proc.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", installs.Script("windows")).Output()
	} else {
		out, _ = proc.CommandContext(ctx, "sh", "-c", installs.Script(runtime.GOOS), "sh", integrate.LookLoginPath("hopsesh")).Output()
	}
	return installs.Parse(out, runtime.GOOS, runtime.GOARCH, home)
}

func lastPath(s string) string {
	got := ""
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "/") {
			got = l
		}
	}
	return got
}

// installer does an update's steps on one machine.
type installer interface {
	// run runs program with args there.
	run(ctx context.Context, program string, args ...string) ([]byte, error)
	// put writes this hopsesh, for that machine, at path (replacing what is there).
	put(ctx context.Context, path string) error
	// link makes path a link to target.
	link(ctx context.Context, path, target string) error
	// list lists hopsesh there again.
	list(ctx context.Context) (installs.List, error)
}

// applyInstalls does steps (installs.Plan of l) and reports each, and returns the program
// that should run the machine now: its own hopsesh, unless that could not be brought up to
// the newest one there (an app whose updater failed), which it would downgrade.
func applyInstalls(ctx context.Context, in installer, l installs.List, steps []installs.Step, report func(SetupStep)) (string, installs.List, error) {
	var firstErr error
	failed := func(name string, err error) {
		report(SetupStep{Name: name, State: "failed", Detail: err.Error()})
		if firstErr == nil {
			firstErr = err
		}
	}
	changed := false
	for _, s := range steps {
		switch s.Do {
		case installs.DoInstall, installs.DoReplace:
			name := "Install hopsesh"
			if s.Do == installs.DoReplace {
				name = "Update hopsesh"
			}
			if err := in.put(ctx, s.Path); err != nil {
				failed(name, err)
				continue
			}
			changed = true
			report(SetupStep{Name: name, State: "done", Detail: strings.TrimPrefix(s.Text, "Update ")})
		case installs.DoApp:
			out, err := in.run(ctx, s.Path, "update", "--yes")
			if err != nil {
				failed("Update the app", errors.New(outputOr(string(out), err)))
				continue
			}
			changed = true
			v, _ := in.run(ctx, s.Path, "version")
			detail := "the app at " + installs.Tilde(s.Bundle, l.Home)
			if f := strings.Fields(string(v)); len(f) >= 2 {
				detail = "hopsesh " + f[1] + " in " + installs.Tilde(s.Bundle, l.Home)
			}
			for _, o := range l.Installs {
				if o.Path == s.Path && o.Open {
					detail += "; it is open there: reopen it to use the new version"
				}
			}
			report(SetupStep{Name: "Update the app", State: "done", Detail: detail})
		case installs.DoManual:
			report(SetupStep{Name: "Update hopsesh", State: "waiting", Detail: s.Text})
		case installs.DoKeep:
			report(SetupStep{Name: "Update hopsesh", State: "skipped", Detail: s.Text})
		}
	}
	if changed {
		if nl, err := in.list(ctx); err == nil {
			l = nl
		}
	}
	program := installs.Program(l, steps)
	main := l.Main()
	newest := main
	for i := range l.Installs {
		if newest == nil || installs.Newer(l.Installs[i].Version, newest.Version) {
			newest = &l.Installs[i]
		}
	}
	for _, s := range steps {
		if s.Do != installs.DoLink {
			continue
		}
		if main == nil || newest != main {
			// The machine's own hopsesh is behind (its update failed): keep hopsesh's own
			// copy, at this version, and run it rather than the older one.
			program = s.Path
			if err := in.put(ctx, s.Path); err != nil {
				failed("Update hopsesh", err)
			}
			report(SetupStep{Name: "One hopsesh", State: "waiting", Detail: "kept hopsesh's own copy at " + installs.Tilde(s.Path, l.Home) + ", updated, until " + nonEmptyStr(describeInstall(main, l.Home), "the other hopsesh") + " is updated"})
			continue
		}
		if err := in.link(ctx, s.Path, main.Path); err != nil {
			failed("One hopsesh", err)
			continue
		}
		report(SetupStep{Name: "One hopsesh", State: "done", Detail: installs.Tilde(s.Path, l.Home) + " runs " + describeInstall(main, l.Home) + " now"})
	}
	if main != nil && newest != main && program == main.Path {
		program = newest.Path
	}
	return program, l, firstErr
}

func describeInstall(in *installs.Install, home string) string {
	if in == nil {
		return ""
	}
	if in.Kind == installs.App {
		return "the app's hopsesh"
	}
	return installs.Tilde(in.Path, home)
}

// remoteInstaller does the steps on a machine over SSH.
type remoteInstaller struct {
	a *App
	m *host.Machine
}

func (r remoteInstaller) run(ctx context.Context, program string, args ...string) ([]byte, error) {
	if r.m.Facts.OS == "windows" {
		line := `"` + program + `"`
		for _, x := range args {
			line += " " + x
		}
		return r.m.Conn.Run(ctx, line)
	}
	return r.m.Conn.RunSh(ctx, `exec "$@"`, append([]string{program}, args...)...)
}

func (r remoteInstaller) put(ctx context.Context, path string) error {
	bin, err := r.a.hopseshFor(ctx, r.m.Facts.OS, r.m.Facts.Arch)
	if err != nil {
		return err
	}
	rfs, ok := r.m.RemoteFS()
	if !ok {
		return errors.New("no file transfer to " + r.m.Name)
	}
	return rfs.WriteFile(path, bytes.NewReader(bin), 0o755)
}

func (r remoteInstaller) link(ctx context.Context, path, target string) error {
	out, err := r.m.Conn.RunSh(ctx, `ln -sfn "$2" "$1.link" && mv -f "$1.link" "$1"`, path, target)
	if err != nil {
		return errors.New(outputOr(string(out), err))
	}
	return nil
}

func (r remoteInstaller) list(ctx context.Context) (installs.List, error) {
	return r.a.remoteInstalls(ctx, r.m)
}

// localInstaller does the steps on this machine.
type localInstaller struct{}

func (localInstaller) run(ctx context.Context, program string, args ...string) ([]byte, error) {
	return proc.CommandContext(ctx, program, args...).CombinedOutput()
}

func (localInstaller) put(_ context.Context, path string) error {
	bin, err := movementHookExecutable()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(bin)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".part"
	if err := os.WriteFile(tmp, b, 0o755); err != nil {
		return err
	}
	_ = os.Remove(path)
	return os.Rename(tmp, path)
}

func (localInstaller) link(_ context.Context, path, target string) error {
	tmp := path + ".link"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (localInstaller) list(ctx context.Context) (installs.List, error) {
	return LocalInstalls(ctx), nil
}

// UpdatePlan is what updating hopsesh on a machine would do, for the dialog that asks.
type UpdatePlan struct {
	Machine  string          `json:"machine"`
	Version  string          `json:"version"` // this hopsesh's, which it brings
	Installs installs.List   `json:"installs"`
	Steps    []installs.Step `json:"steps"`
	Restarts bool            `json:"restarts"` // hopsesh restarts in the login session there
	Ends     int             `json:"ends"`     // terminals hopsesh runs there that the restart ends
}

// PlanUpdate lists hopsesh on machine and says what setting it up or updating it would do.
func (a *App) PlanUpdate(ctx context.Context, machine string) (*UpdatePlan, error) {
	h, err := a.allowedHost(machine)
	if err != nil {
		return nil, err
	}
	m, err := a.Connect(ctx, h)
	if err != nil {
		return nil, err
	}
	defer m.Close()
	l, err := a.remoteInstalls(ctx, m)
	if err != nil {
		return nil, err
	}
	p := &UpdatePlan{Machine: machine, Version: version.Version, Installs: l, Steps: installs.Plan(l, version.Version, remoteProgram(m)), Restarts: true}
	if ts, err := a.RemoteTerminals(ctx, machine); err == nil {
		for _, t := range ts {
			if t.State != "exited" {
				p.Ends++
			}
		}
	}
	return p, nil
}

// AlignHere brings the other copies of hopsesh on this machine to this one's version: the
// same steps as updating another machine, except that the login service is only moved,
// never added (starting at login here is the user's choice).
func (a *App) AlignHere(ctx context.Context, progress func(SetupStep)) (*MachineSetup, error) {
	out := &MachineSetup{Machine: LocalName(), Version: version.Version}
	step := func(s SetupStep) {
		out.Steps = append(out.Steps, s)
		if progress != nil {
			progress(s)
		}
	}
	l := LocalInstalls(ctx)
	steps := installs.Plan(l, version.Version, filepath.Join(l.Home, ".hopsesh", "bin", "hopsesh"+map[bool]string{true: ".exe"}[runtime.GOOS == "windows"]))
	if l.Main() == nil {
		return out, errors.New("hopsesh isn't installed here (this one runs from where it was unpacked)")
	}
	program, l, err := applyInstalls(ctx, localInstaller{}, l, steps, step)
	out.Program = program
	if l.Service != "" && !sameFile(l.Service, program) {
		b, serr := localInstaller{}.run(ctx, program, "serve", "install", "--json")
		if serr != nil {
			step(SetupStep{Name: "Start at login", State: "failed", Detail: outputOr(string(b), serr)})
			if err == nil {
				err = serr
			}
		} else {
			step(SetupStep{Name: "Start at login", State: "done", Detail: installs.Tilde(program, l.Home) + " starts at login now"})
		}
	}
	if len(out.Steps) == 0 {
		step(SetupStep{Name: "One hopsesh", State: "skipped", Detail: "every hopsesh here is " + version.Version + " already"})
	}
	return out, err
}

func sameFile(a, b string) bool {
	if a == b {
		return true
	}
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	return ea == nil && eb == nil && ra == rb
}
