package integrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var executable = os.Executable // a test stands in for the app

// On Windows the app's folder holds hopsesh-app.exe and hopsesh.exe side by side; the
// hopsesh command is that folder on the user's PATH (the installer adds it).

// AppCLI is the hopsesh.exe beside the running app, or an error explaining why there is
// none.
func AppCLI() (string, error) {
	exe, err := executable()
	if err != nil {
		return "", err
	}
	if !isAppExe(exe) {
		return "", errors.New("not running as the hopsesh app")
	}
	cli := filepath.Join(filepath.Dir(exe), "hopsesh.exe")
	if _, err := os.Stat(cli); err != nil {
		return "", fmt.Errorf("hopsesh.exe is missing beside the app (%s); reinstall the app", cli)
	}
	return cli, nil
}

// CheckCLI reports whether the app's folder is on the user's PATH, and which hopsesh new
// terminals run.
func CheckCLI() CLIStatus {
	st := CLIStatus{}
	cli, err := AppCLI()
	if err != nil {
		st.CanInstall = err.Error()
	}
	st.AppCLI, st.Path = cli, cli
	st.Resolves = LookLoginPath("hopsesh")
	if cli != "" {
		st.PathLine = filepath.Dir(cli)
		st.DirOnPath = LoginPathHas(st.PathLine)
	}
	switch {
	case cli != "" && st.DirOnPath && (st.Resolves == "" || strings.EqualFold(filepath.Clean(st.Resolves), filepath.Clean(cli))):
		st.State = CLIOurs
	case st.Resolves != "":
		st.State, st.Target = CLIStandalone, st.Resolves
	default:
		st.State = CLIMissing
	}
	return st
}

// InstallCLI puts the app's folder on the user's PATH (at the front with force, so it
// comes before another hopsesh on the user's PATH).
func InstallCLI(force bool) (CLIStatus, error) {
	cli, err := AppCLI()
	if err != nil {
		return CheckCLI(), err
	}
	st := CheckCLI()
	switch st.State {
	case CLIOurs:
		return st, nil
	case CLIStandalone:
		if !force {
			return st, fmt.Errorf("new terminals run another hopsesh (%s); use this app's only if you mean to", st.Resolves)
		}
	}
	dir := filepath.Dir(cli)
	if err := setUserPath(func(dirs []string) []string {
		dirs = withoutDir(dirs, dir)
		if force {
			return append([]string{dir}, dirs...)
		}
		return append(dirs, dir)
	}); err != nil {
		return st, err
	}
	st = CheckCLI()
	if st.State != CLIOurs {
		return st, fmt.Errorf("the app's folder is on your PATH, but new terminals still run %s first (it is on the system PATH)", st.Resolves)
	}
	return st, nil
}

// UninstallCLI takes the app's folder off the user's PATH.
func UninstallCLI() error {
	cli, err := AppCLI()
	if err != nil {
		return err
	}
	return setUserPath(func(dirs []string) []string { return withoutDir(dirs, filepath.Dir(cli)) })
}

// AddToPath is InstallCLI on Windows: the folder on PATH is the command.
func AddToPath() (string, error) {
	_, err := InstallCLI(false)
	return "your user PATH", err
}

func withoutDir(dirs []string, dir string) []string {
	out := dirs[:0:0]
	for _, d := range dirs {
		if !strings.EqualFold(filepath.Clean(os.ExpandEnv(d)), filepath.Clean(dir)) {
			out = append(out, d)
		}
	}
	return out
}
