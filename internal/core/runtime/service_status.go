package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

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
	cmd.WaitDelay = time.Second
	var b, stderr boundedServiceOutput
	cmd.Stdout, cmd.Stderr = &b, &stderr
	err := cmd.Run()
	if b.overflow || stderr.overflow {
		return nil, errors.New("OS service response exceeded its size limit")
	}
	if len(args) > 0 && args[0] == "powershell.exe" {
		if err != nil {
			cause := err
			if ctx.Err() != nil {
				cause = ctx.Err()
			}
			err = fmt.Errorf("%w during %s", cause, schedulerPhase(stderr.text.String()))
		}
		return []byte(b.text.String()), err
	}
	return []byte(b.text.String() + stderr.text.String()), err
}

// Only fixed phase labels can enter diagnostics; never expose PowerShell's raw
// stderr, which may include task definitions, paths or environment values.
func schedulerPhase(stderr string) string {
	phase := "PowerShell startup"
	for _, line := range strings.Split(stderr, "\n") {
		switch strings.TrimSpace(line) {
		case "hopsesh-service-phase=create", "hopsesh-service-phase=connect", "hopsesh-service-phase=folder", "hopsesh-service-phase=task", "hopsesh-service-phase=properties":
			phase = "Task Scheduler " + strings.TrimPrefix(strings.TrimSpace(line), "hopsesh-service-phase=")
		}
	}
	return phase
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
		// Query Task Scheduler directly. CIM's missing-task error identifiers
		// differ by Windows version and must not be mistaken for a broken service.
		script := windowsScheduler(p.Name) + `
[Console]::Error.WriteLine('hopsesh-service-phase=task')
try { $t=$folder.GetTask($name) } catch {
  $cause=$_.Exception
  while ($cause.InnerException) { $cause=$cause.InnerException }
  if ($cause.HResult -ne -2147024894) { throw }
  @{registered=$false;enabled=$false;running=$false} | ConvertTo-Json -Compress
  exit 0
}
[Console]::Error.WriteLine('hopsesh-service-phase=properties')
@{registered=$true;enabled=[bool]$t.Enabled;running=($t.State -eq 4)} | ConvertTo-Json -Compress`
		args = []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script}
	default:
		s.Error = "Login service is unsupported on this OS"
		return s
	}
	queryContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	b, queryErr := run(queryContext, args)
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
		var value struct{ Registered, Enabled, Running *bool }
		if queryErr == nil && json.Unmarshal(b, &value) == nil && value.Registered != nil && value.Enabled != nil && value.Running != nil {
			s.Known, s.Registered, s.Enabled, s.Running = true, *value.Registered, *value.Enabled, *value.Running
		}
	}
	if !s.Known {
		s.Error = "OS login service status is unavailable"
		if queryErr != nil {
			// Process errors contain the exit status, not raw OS output which
			// may expose paths or task definitions in diagnostics.
			s.Error += ": " + queryErr.Error()
		} else if queryContext.Err() != nil {
			s.Error += ": " + queryContext.Err().Error()
		} else {
			s.Error += ": invalid supervisor response"
		}
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
		return runService(ctx, [][]string{{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", windowsScheduler(p.Name) + "$t=$folder.GetTask($name); $t.Enabled=$true"}, {"schtasks", "/Run", "/TN", p.Name}})
	}
	return fmt.Errorf("unsupported service platform %s", p.Platform)
}

// The scheduler connection always targets the local machine and current user.
// Only GetTask's ERROR_FILE_NOT_FOUND is absence; connection/access errors fail.
func windowsScheduler(name string) string {
	return "$ErrorActionPreference='Stop'; [Console]::Error.WriteLine('hopsesh-service-phase=create'); $service=New-Object -ComObject Schedule.Service; [Console]::Error.WriteLine('hopsesh-service-phase=connect'); $service.Connect(); [Console]::Error.WriteLine('hopsesh-service-phase=folder'); $folder=$service.GetFolder('\\'); $name='" + strings.ReplaceAll(name, "'", "''") + "'; "
}
