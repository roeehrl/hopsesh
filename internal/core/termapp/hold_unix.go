//go:build !windows

package termapp

import (
	"os"
	"path/filepath"
	"syscall"
)

// ExecShell replaces this process with the user's login shell in dir: a tab whose
// command has ended stays open as a shell instead of closing.
func ExecShell(dir string) error {
	sh := os.Getenv("SHELL")
	if sh == "" || !filepath.IsAbs(sh) {
		sh = "/bin/sh"
	}
	if dir != "" {
		_ = os.Chdir(dir)
	}
	return syscall.Exec(sh, []string{"-" + filepath.Base(sh)}, os.Environ()) //nolint:gosec // the user's own login shell
}
