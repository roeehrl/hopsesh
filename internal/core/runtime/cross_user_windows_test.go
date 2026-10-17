package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

type windowsUserProbe struct {
	Namespace Namespace `json:"namespace"`
	Secret    string    `json:"secret"`
}

type windowsUserResult struct {
	SID          string `json:"sid"`
	Namespace    string `json:"namespace"`
	StatusDenied bool   `json:"statusDenied"`
	SecretDenied bool   `json:"secretDenied"`
	PipeDenied   bool   `json:"pipeDenied"`
	Error        string `json:"error,omitempty"`
}

// This runs only in the explicitly enabled native lane. LocalService is an
// existing low-privilege identity: no user/password is created, and the unique
// scheduled task executes only this synthetic probe, never the installed app.
func TestRuntimeDifferentOSUserIsolation(t *testing.T) {
	if os.Getenv("HOPSESH_RUNTIME_CROSS_USER") != "1" {
		t.Skip("requires an elevated disposable runner for LocalService qualification")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	public, err := windows.KnownFolderPath(windows.FOLDERID_Public, 0)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(public, "hopsesh-cross-user-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error("remove disposable cross-user files", err)
		}
	})
	sid, err := currentSID()
	if err != nil {
		t.Fatal(err)
	}
	// Only the owner can write the executable/input. LocalService can traverse
	// and read this fixture, and write a separate results directory.
	fixtureUserACL(t, root, sid.String(), "GRGX")
	reports := filepath.Join(root, "reports")
	if err := os.Mkdir(reports, 0700); err != nil {
		t.Fatal(err)
	}
	fixtureUserACL(t, reports, sid.String(), "FA")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	binary := filepath.Join(root, "probe.exe")
	destination, err := os.OpenFile(binary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatal(copyErr, closeErr)
	}
	n, err := NewNamespace(filepath.Join(root, "config"), filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	n.Directory = filepath.Join(root, "private")
	testHost(t, n, func(context.Context) (json.RawMessage, error) {
		return json.RawMessage(`{"sessions":["PRIVATE-CROSS-USER-FIXTURE"]}`), nil
	}, nil)
	secret := filepath.Join(n.Directory, "private-fixture")
	if err := os.WriteFile(secret, []byte("PRIVATE-CROSS-USER-FIXTURE"), 0600); err != nil {
		t.Fatal(err)
	}
	probe := windowsUserProbe{Namespace: n, Secret: secret}
	self, err := probeWindowsUser(ctx, probe)
	if err != nil || self.SID != sid.String() || self.Namespace != n.ID || self.StatusDenied || self.SecretDenied || self.PipeDenied {
		t.Fatal("same-user positive control failed", self, err)
	}
	input, output := filepath.Join(root, "input.json"), filepath.Join(reports, "result.json")
	data, _ := json.Marshal(probe)
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestRuntimeCrossUserHelper$", "--", "runtime-cross-user-helper", input, output}
	for i := range args {
		args[i] = syscall.EscapeArg(args[i])
	}
	name := "dev.codonic.hopsesh.cross-user." + n.ID
	runTask := func(ctx context.Context, script string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; "+script)
		cmd.Env = append(os.Environ(), "HOPSESH_CROSS_USER_TASK="+name, "HOPSESH_CROSS_USER_BINARY="+binary, "HOPSESH_CROSS_USER_ARGS="+strings.Join(args, " "))
		cmd.WaitDelay = time.Second
		return cmd.CombinedOutput()
	}
	// Register cleanup before dispatch, including a partial register/start
	// failure. Never stop/delete a task whose action is not our exact probe.
	t.Cleanup(func() {
		bounded, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		out, err := runTask(bounded, `$task=Get-ScheduledTask -TaskName $env:HOPSESH_CROSS_USER_TASK -ErrorAction SilentlyContinue; if($task){if(@($task.Actions).Count -ne 1 -or $task.Actions[0].Execute -ne $env:HOPSESH_CROSS_USER_BINARY){throw 'foreign task action'}; Stop-ScheduledTask -TaskName $env:HOPSESH_CROSS_USER_TASK; Unregister-ScheduledTask -TaskName $env:HOPSESH_CROSS_USER_TASK -Confirm:$false}; if(Get-ScheduledTask -TaskName $env:HOPSESH_CROSS_USER_TASK -ErrorAction SilentlyContinue){throw 'task survived cleanup'}`)
		if err != nil {
			t.Errorf("remove disposable LocalService task: %v %s", err, out)
		}
	})
	out, err := runTask(ctx, `$action=New-ScheduledTaskAction -Execute $env:HOPSESH_CROSS_USER_BINARY -Argument $env:HOPSESH_CROSS_USER_ARGS; $principal=New-ScheduledTaskPrincipal -UserId 'S-1-5-19' -LogonType ServiceAccount -RunLevel Limited; $settings=New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Seconds 30) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries; Register-ScheduledTask -TaskName $env:HOPSESH_CROSS_USER_TASK -Action $action -Principal $principal -Settings $settings | Out-Null; Start-ScheduledTask -TaskName $env:HOPSESH_CROSS_USER_TASK`)
	if err != nil {
		t.Fatalf("start disposable LocalService probe: %v %s", err, out)
	}
	var foreign windowsUserResult
	for {
		body, err := os.ReadFile(output)
		if err == nil && json.Unmarshal(body, &foreign) == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("LocalService probe did not publish its result", ctx.Err())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if foreign.Error != "" || foreign.SID != "S-1-5-19" || foreign.SID == sid.String() || foreign.Namespace == n.ID || !foreign.StatusDenied || !foreign.SecretDenied || !foreign.PipeDenied {
		t.Fatal("another OS user accessed or impersonated the runtime", foreign)
	}
	var status Status
	if err := (Client{n}).Call(ctx, "status", nil, &status); err != nil {
		t.Fatal("foreign probe damaged the owner's runtime", err)
	}
	t.Logf("verified owner SID %s and LocalService have distinct namespaces; private files and actual named-pipe access denied", sid)
}

func fixtureUserACL(t *testing.T, path, owner, rights string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + owner + ")(A;OICI;" + rights + ";;;S-1-5-19)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func probeWindowsUser(ctx context.Context, probe windowsUserProbe) (windowsUserResult, error) {
	var result windowsUserResult
	sid, err := currentSID()
	if err != nil {
		return result, err
	}
	result.SID = sid.String()
	n, err := NewNamespace(probe.Namespace.Config, probe.Namespace.State)
	if err != nil {
		return result, err
	}
	result.Namespace = n.ID
	var status Status
	result.StatusDenied = (Client{probe.Namespace}).Call(ctx, "status", nil, &status) != nil
	_, err = os.ReadFile(probe.Secret)
	result.SecretDenied = errors.Is(err, os.ErrPermission)
	conn, err := winio.DialPipeContext(ctx, probe.Namespace.Address)
	if err == nil {
		_ = conn.Close()
	} else if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return result, err // A timeout/missing pipe is not proof of isolation.
	}
	result.PipeDenied = errors.Is(err, windows.ERROR_ACCESS_DENIED)
	return result, nil
}

func TestRuntimeCrossUserHelper(t *testing.T) {
	if len(os.Args) < 4 || os.Args[len(os.Args)-3] != "runtime-cross-user-helper" {
		t.Skip("cross-user subprocess helper")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var probe windowsUserProbe
	body, err := os.ReadFile(os.Args[len(os.Args)-2])
	if err == nil {
		err = json.Unmarshal(body, &probe)
	}
	var result windowsUserResult
	if err == nil {
		result, err = probeWindowsUser(ctx, probe)
	}
	if err != nil {
		result.Error = err.Error()
	}
	body, _ = json.Marshal(result)
	if err := os.WriteFile(os.Args[len(os.Args)-1], body, 0600); err != nil {
		t.Fatal(err)
	}
}
