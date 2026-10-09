package runtime

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"
)

// ServicePlan is a reviewable per-user login registration. It never requests
// stored passwords, system privileges or availability before OS-user login.
type ServicePlan struct {
	Namespace  string     `json:"namespace"`
	Platform   string     `json:"platform"`
	Name       string     `json:"name"`
	Path       string     `json:"path"`
	Definition string     `json:"definition"`
	Enable     [][]string `json:"enable"`
	Disable    [][]string `json:"disable"`
	Stop       [][]string `json:"stop"`
	Scope      string     `json:"scope"`
}

func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func unitQuote(s string) (string, error) {
	if strings.ContainsAny(s, "\x00\n\r") {
		return "", errors.New("service path contains a control character")
	}
	s = strings.ReplaceAll(s, "%", "%%")
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`, nil
}
func PlanService(n Namespace, exe, platform, home, uid string) (ServicePlan, error) {
	p := ServicePlan{Namespace: n.ID, Platform: platform, Name: "dev.codonic.hopsesh.runtime." + n.ID, Scope: "Current OS user; starts at login. No stored login credentials or system service."}
	if !filepath.IsAbs(exe) || !filepath.IsAbs(n.Config) || !filepath.IsAbs(n.State) || !filepath.IsAbs(home) {
		return p, errors.New("service paths must be absolute")
	}
	for _, s := range []string{exe, n.Config, n.State, home, uid} {
		if strings.ContainsAny(s, "\x00\r\n") {
			return p, errors.New("service values contain control characters")
		}
	}
	switch platform {
	case "darwin":
		p.Path = filepath.Join(home, "Library", "LaunchAgents", p.Name+".plist")
		p.Definition = `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>` + p.Name + `</string><key>ProgramArguments</key><array><string>` + xmlText(exe) + `</string><string>runtime</string><string>serve</string></array><key>EnvironmentVariables</key><dict><key>HOPSESH_CONFIG_DIR</key><string>` + xmlText(n.Config) + `</string><key>HOPSESH_STATE_DIR</key><string>` + xmlText(n.State) + `</string></dict><key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>ThrottleInterval</key><integer>30</integer><key>StandardOutPath</key><string>/dev/null</string><key>StandardErrorPath</key><string>/dev/null</string></dict></plist>`
		domain := "gui/" + uid
		p.Enable = [][]string{{"launchctl", "bootstrap", domain, p.Path}}
		p.Disable = [][]string{{"launchctl", "bootout", domain + "/" + p.Name}}
		p.Stop = p.Disable
	case "linux":
		p.Name = "hopsesh-runtime-" + n.ID + ".service"
		p.Path = filepath.Join(home, ".config", "systemd", "user", p.Name)
		command, err := unitQuote(exe)
		if err != nil {
			return p, err
		}
		cfg, _ := unitQuote("HOPSESH_CONFIG_DIR=" + n.Config)
		state, _ := unitQuote("HOPSESH_STATE_DIR=" + n.State)
		p.Definition = "[Unit]\nDescription=Hopsesh shared runtime\n[Service]\nType=simple\nExecStart=" + command + " runtime serve\nEnvironment=" + cfg + " " + state + "\nRestart=on-failure\nRestartSec=30s\nTimeoutStopSec=60s\n[Install]\nWantedBy=default.target\n"
		p.Enable = [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "--now", p.Name}}
		p.Disable = [][]string{{"systemctl", "--user", "disable", "--now", p.Name}}
		p.Stop = [][]string{{"systemctl", "--user", "stop", p.Name}}
	case "windows":
		// Environment arguments use CLI global flags so Task Scheduler never captures
		// the invoking shell's credentials, proxy tokens or unrelated environment.
		p.Path = filepath.Join(n.Directory, "login-task.xml")
		args := `--config-dir ` + windowsQuote(n.Config) + ` --state-dir ` + windowsQuote(n.State) + ` runtime serve`
		p.Definition = `<?xml version="1.0"?><Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Triggers><LogonTrigger><Enabled>true</Enabled><UserId>` + xmlText(uid) + `</UserId></LogonTrigger></Triggers><Principals><Principal id="Owner"><UserId>` + xmlText(uid) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><ExecutionTimeLimit>PT0S</ExecutionTimeLimit><RestartOnFailure><Interval>PT1M</Interval><Count>3</Count></RestartOnFailure></Settings><Actions Context="Owner"><Exec><Command>` + xmlText(exe) + `</Command><Arguments>` + xmlText(args) + `</Arguments></Exec></Actions></Task>`
		p.Enable = [][]string{{"schtasks", "/Create", "/TN", p.Name, "/XML", p.Path, "/F"}, {"schtasks", "/Run", "/TN", p.Name}}
		p.Disable = [][]string{{"schtasks", "/Delete", "/TN", p.Name, "/F"}}
		p.Stop = [][]string{{"schtasks", "/End", "/TN", p.Name}}
	default:
		return p, fmt.Errorf("login runtime registration is unsupported on %s", platform)
	}
	return p, nil
}

// definitionBytes is the exact file representation used for publication and
// ownership checks. The reviewable Definition remains ordinary Unicode text.
// schtasks imports UTF-16LE with a BOM; omit a contradictory XML encoding
// declaration so the same definition also parses correctly in UTF-8 review JSON.
func (p ServicePlan) definitionBytes() []byte {
	if p.Platform != "windows" {
		return []byte(p.Definition)
	}
	units := utf16.Encode([]rune(p.Definition))
	b := make([]byte, 2+2*len(units))
	b[0], b[1] = 0xff, 0xfe
	for i, unit := range units {
		binary.LittleEndian.PutUint16(b[2+2*i:], unit)
	}
	return b
}

func windowsQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range s {
		if r == '\\' {
			slashes++
			continue
		}
		if r == '"' {
			b.WriteString(strings.Repeat("\\", slashes*2+1))
		} else {
			b.WriteString(strings.Repeat("\\", slashes))
		}
		slashes = 0
		b.WriteRune(r)
	}
	b.WriteString(strings.Repeat("\\", slashes*2))
	b.WriteByte('"')
	return b.String()
}
func CurrentServicePlan(n Namespace) (ServicePlan, error) {
	exe, err := os.Executable()
	if err != nil {
		return ServicePlan{}, err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return ServicePlan{}, err
	}
	u, err := user.Current()
	if err != nil {
		return ServicePlan{}, err
	}
	return PlanService(n, exe, runtime.GOOS, u.HomeDir, u.Uid)
}
func runService(ctx context.Context, commands [][]string) error {
	for _, args := range commands {
		out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}
func (p ServicePlan) EnableAtLogin(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(p.Path), 0700); err != nil {
		return err
	}
	// An unrelated definition at our path is refused instead of overwritten.
	present, err := p.definitionPresent()
	if err != nil {
		return err
	}
	status := p.Status(ctx)
	if !status.Known {
		return errors.New(status.Error)
	}
	if status.Registered {
		if !present {
			return errors.New("OS login service has no verified matching definition; refusing to replace it")
		}
		if status.Enabled && status.Running {
			return nil
		}
		return p.startRegistered(ctx)
	}
	f, err := os.CreateTemp(filepath.Dir(p.Path), ".runtime-service-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(p.definitionBytes())
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), p.Path); err != nil {
		return err
	}
	return runService(ctx, p.Enable)
}
func (p ServicePlan) DisableAtLogin(ctx context.Context) error {
	present, err := p.definitionPresent()
	if err != nil {
		return err
	}
	s := p.Status(ctx)
	if !s.Known {
		return errors.New(s.Error)
	}
	if s.Registered {
		if !present {
			return errors.New("OS login service has no verified matching definition; refusing to remove it")
		}
		if err = runService(ctx, p.Disable); err != nil {
			return err
		}
		// launchctl can return while the job is still draining. Keep its
		// verified definition until the supervisor has actually unregistered it.
		if p.Platform != "linux" {
			wait, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			tick := time.NewTicker(50 * time.Millisecond)
			defer tick.Stop()
			for {
				state := p.Status(wait)
				if !state.Known {
					return errors.New(state.Error)
				}
				if !state.Registered {
					break
				}
				select {
				case <-wait.Done():
					return wait.Err()
				case <-tick.C:
				}
			}
		}
	}
	if err = os.Remove(p.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if p.Platform == "linux" {
		return runService(ctx, [][]string{{"systemctl", "--user", "daemon-reload"}})
	}
	return nil
}
func (p ServicePlan) StopNow(ctx context.Context) error { return runService(ctx, p.Stop) }
