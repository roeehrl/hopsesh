package gui

import (
	"encoding/base64"
	"os/exec"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// openWindowsTerminal runs a PowerShell line in a new window: Windows Terminal when it is
// installed, else a PowerShell console. The line goes in as -EncodedCommand, so no quoting
// (cmd's, Windows Terminal's or PowerShell's) can change it.
func openWindowsTerminal(line string) error {
	shell := "powershell.exe"
	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		shell = p
	}
	args := []string{shell, "-NoLogo", "-NoExit", "-EncodedCommand", encodePS(line)}
	if wt, err := exec.LookPath("wt.exe"); err == nil {
		return exec.Command(wt, args...).Start()
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
	return cmd.Start()
}

// encodePS is a PowerShell -EncodedCommand: base64 of the UTF-16LE text.
func encodePS(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return base64.StdEncoding.EncodeToString(b)
}
