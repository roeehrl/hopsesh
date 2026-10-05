//go:build !windows

package gui

import (
	"bufio"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/proc"
)

// loginShell is the user's login shell as the account database has it (macOS: Directory
// Services; elsewhere /etc/passwd), else $SHELL, else /bin/sh, started as a login shell.
func loginShell() []string {
	sh := ""
	if u, err := user.Current(); err == nil {
		if runtime.GOOS == "darwin" {
			sh = dsclShell(u.Username)
		} else {
			sh = passwdShell(u.Uid)
		}
	}
	if !usable(sh) {
		sh = os.Getenv("SHELL")
	}
	if !usable(sh) {
		sh = "/bin/sh"
	}
	return []string{sh, "-l"}
}

func usable(sh string) bool {
	if !filepath.IsAbs(sh) {
		return false
	}
	fi, err := os.Stat(sh)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}

// dsclShell is a macOS user's UserShell.
func dsclShell(name string) string {
	c := proc.Command("/usr/bin/dscl", ".", "-read", "/Users/"+name, "UserShell")
	done := make(chan struct{})
	t := time.AfterFunc(3*time.Second, func() {
		if c.Process != nil {
			_ = c.Process.Kill()
		}
	})
	defer t.Stop()
	defer close(done)
	out, err := c.Output()
	if err != nil {
		return ""
	}
	_, v, _ := strings.Cut(strings.TrimSpace(string(out)), "UserShell:")
	return strings.TrimSpace(v)
}

// passwdShell is the shell /etc/passwd gives the user uid.
func passwdShell(uid string) string {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), ":")
		if len(parts) >= 7 && parts[2] == uid {
			return parts[6]
		}
	}
	return ""
}

// screenReaderRunning reports whether the system's screen reader runs (VoiceOver on
// macOS; elsewhere unknown, so off).
func screenReaderRunning() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	out, err := proc.Command("/usr/bin/defaults", "read", "com.apple.universalaccess", "voiceOverOnOffKey").Output()
	return err == nil && strings.TrimSpace(string(out)) == "1"
}
