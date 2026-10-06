package agent

import (
	"os/exec"
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
