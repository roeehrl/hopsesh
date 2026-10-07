package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

// ServiceStatus describes the OS supervisor, independently of the IPC owner.
// A definition on disk alone never establishes that a service is registered.
type ServiceStatus struct {
	Known      bool   `json:"known"`
	Registered bool   `json:"registered"`
	Enabled    bool   `json:"enabled"`
	Running    bool   `json:"running"`
	Definition bool   `json:"definition"`
	Error      string `json:"error,omitempty"`
}

type serviceRunner func(context.Context, []string) ([]byte, error)

func serviceQuery(ctx context.Context, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var b boundedServiceOutput
	cmd.Stdout, cmd.Stderr = &b, &b
	err := cmd.Run()
	if b.overflow {
		return nil, errors.New("OS service response exceeded its size limit")
	}
	return []byte(b.text.String()), err
}

type boundedServiceOutput struct {
	text     strings.Builder
	overflow bool
}

func (b *boundedServiceOutput) Write(p []byte) (int, error) {
	if b.text.Len()+len(p) > 64<<10 {
		b.overflow = true
		return 0, errors.New("OS service response exceeded its size limit")
	}
	return b.text.Write(p)
}

func (p ServicePlan) definitionPresent() (bool, error) {
	b, err := localstate.ReadPrivateFile(p.Path, 64<<10)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(b) > 64<<10 || string(b) != p.Definition {
		return false, errors.New("login registration differs from this plan; disable it with its original executable before replacing")
	}
	return true, nil
}

func (p ServicePlan) Status(ctx context.Context) ServiceStatus {
	return p.status(ctx, serviceQuery)
}
func (p ServicePlan) status(ctx context.Context, run serviceRunner) ServiceStatus {
	s := ServiceStatus{}
	var err error
	s.Definition, err = p.definitionPresent()
	if err != nil {
		s.Error = "Login definition could not be verified"
		return s
	}
	var args []string
	switch p.Platform {
	case "darwin":
		args = []string{"launchctl", "print", p.Disable[0][2]}
	case "linux":
		args = []string{"systemctl", "--user", "show", p.Name, "--property=LoadState,ActiveState,UnitFileState"}
	case "windows":
		name := strings.ReplaceAll(p.Name, "'", "''")
		script := "$ErrorActionPreference='Stop'; try { $t=Get-ScheduledTask -TaskPath '\\' -TaskName '" + name + "'; @{registered=$true;enabled=$t.Settings.Enabled;running=($t.State -eq 'Running')} | ConvertTo-Json -Compress } catch { if ($_.FullyQualifiedErrorId -like 'NoMatchingMSFT_ScheduledTask*') { @{registered=$false;enabled=$false;running=$false} | ConvertTo-Json -Compress } else { throw } }"
		args = []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script}
	default:
		s.Error = "Login service is unsupported on this OS"
		return s
	}
	b, queryErr := run(ctx, args)
	text := string(b)
	switch p.Platform {
	case "darwin":
		var exit *exec.ExitError
		if errors.As(queryErr, &exit) && exit.ExitCode() == 113 && strings.Contains(text, "Could not find service") {
			s.Known = true
			return s
		}
		if queryErr == nil {
			s.Known, s.Registered, s.Enabled = true, true, true
			s.Running = strings.Contains(text, "state = running")
		}
	case "linux":
		values := map[string]string{}
		for _, line := range strings.Split(text, "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				values[k] = v
			}
		}
		if values["LoadState"] == "not-found" {
			s.Known = true
			return s
		}
		if queryErr == nil && values["LoadState"] != "" {
			s.Known = true
			s.Registered = values["LoadState"] == "loaded"
			s.Enabled = values["UnitFileState"] == "enabled" || values["UnitFileState"] == "enabled-runtime"
			s.Running = values["ActiveState"] == "active" || values["ActiveState"] == "activating"
		}
	case "windows":
		var value struct{ Registered, Enabled, Running bool }
		if queryErr == nil && json.Unmarshal(b, &value) == nil {
			s.Known, s.Registered, s.Enabled, s.Running = true, value.Registered, value.Enabled, value.Running
		}
	}
	if !s.Known {
		s.Error = "OS login service status is unavailable"
	}
	return s
}

func (p ServicePlan) startRegistered(ctx context.Context) error {
	switch p.Platform {
	case "darwin":
		return runService(ctx, [][]string{{"launchctl", "kickstart", p.Disable[0][2]}})
	case "linux":
		return runService(ctx, p.Enable)
	case "windows":
		name := strings.ReplaceAll(p.Name, "'", "''")
		return runService(ctx, [][]string{{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Enable-ScheduledTask -TaskPath '\\' -TaskName '" + name + "' -ErrorAction Stop | Out-Null"}, {"schtasks", "/Run", "/TN", p.Name}})
	}
	return fmt.Errorf("unsupported service platform %s", p.Platform)
}
