package agent

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func TestNoticeHookWindowsShellTransport(t *testing.T) {
	bin := hookHelperExecutable(t)
	for _, profile := range []string{"", "profile ' $x ; echo bad"} {
		script, err := NoticeHookPowerShellScript(bin, "claude", profile, hookShellDirArgs...)
		if err != nil {
			t.Fatal(err)
		}
		// Exercise progress under each real shell even on warmed-up CI machines.
		// Without the script preference, encoded PowerShell can serialize it as
		// CLIXML on stderr. stdout must stay JSON and this progress must be quiet.
		script = strings.Replace(script, "$buffer=", "Write-Progress -Activity 'hook regression progress' -Status 'first use' -PercentComplete 50; $buffer=", 1)
		// Claude shell=powershell; Codex selected PowerShell or cmd fallback.
		assertHookShellTransport(t, exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script), profile)
		wrapper := EncodedPowerShellHook(script)
		assertHookShellTransport(t, exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", wrapper), profile)
		cmd := exec.Command("cmd.exe")
		// Match the pinned Codex command_runner.rs raw_arg outer quoting exactly.
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /C "` + wrapper + `"`}
		assertHookShellTransport(t, cmd, profile)
	}
}
