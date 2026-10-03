//go:build !windows

package integrate

import (
	"os"
	"path/filepath"
)

func isAppExe(string) bool { return false } // the macOS app's tool is found through AppCLI

// loginPATH is the login shell's PATH, or this process's when the shell cannot be read.
func loginPATH() string {
	if p := LoginEnv()["PATH"]; p != "" {
		return p
	}
	return os.Getenv("PATH")
}

// lookIn finds an executable name in dir.
func lookIn(dir, name string) string {
	f := filepath.Join(dir, name)
	if fi, err := os.Stat(f); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
		return f
	}
	return ""
}
