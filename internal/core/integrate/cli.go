package integrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// States of the command-line tool link at ~/.local/bin/hopsesh.
const (
	CLIMissing    = "missing"    // nothing there
	CLIOurs       = "ours"       // a link to this app's tool
	CLIOtherApp   = "other-app"  // a link into another copy of hopsesh.app
	CLIDangling   = "dangling"   // a link whose target is gone (the app moved or was deleted)
	CLIStandalone = "standalone" // a regular file (install script, go install)
	CLIForeign    = "foreign"    // a link somewhere else (e.g. Homebrew)
)

// CLIStatus describes the command-line tool link.
type CLIStatus struct {
	State      string `json:"state"`
	Path       string `json:"path"`             // ~/.local/bin/hopsesh
	Target     string `json:"target,omitempty"` // where the link points
	AppCLI     string `json:"appCli,omitempty"` // the tool inside this app
	DirOnPath  bool   `json:"dirOnPath"`        // ~/.local/bin is on the login PATH
	Resolves   string `json:"resolves,omitempty"`
	Profile    string `json:"profile,omitempty"` // file "Add to PATH" would change
	PathLine   string `json:"pathLine"`
	PathAdded  bool   `json:"pathAdded,omitempty"` // hopsesh's block is in the profile
	CanInstall string `json:"cannotInstall,omitempty"`
}

// LinkPath is where the command-line tool is linked.
func LinkPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin", "hopsesh")
}

// AppCLI is the command-line tool inside the running app bundle, or an error explaining
// why it cannot be linked (not an app, running from a disk image or a translocated copy).
func AppCLI() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	i := strings.Index(exe, ".app/Contents/MacOS/")
	if i < 0 {
		return "", errors.New("not running as hopsesh.app")
	}
	app := exe[:i+len(".app")]
	switch {
	case strings.Contains(app, "/AppTranslocation/"):
		return "", errors.New("macOS is running hopsesh from a temporary copy; move hopsesh.app to Applications and open it from there")
	case strings.HasPrefix(app, "/Volumes/"):
		return "", errors.New("hopsesh is running from the disk image; drag it to Applications and open it from there")
	}
	cli := filepath.Join(app, "Contents", "Resources", "bin", "hopsesh")
	if _, err := os.Stat(cli); err != nil {
		return "", fmt.Errorf("the tool is missing from the app (%s)", cli)
	}
	return cli, nil
}

func isHopseshApp(target string) bool {
	return strings.Contains(target, ".app/Contents/") && strings.Contains(strings.ToLower(target), "hopsesh")
}

// CheckCLI reports the state of ~/.local/bin/hopsesh without following it.
func CheckCLI() CLIStatus {
	st := CLIStatus{Path: LinkPath()}
	st.AppCLI, _ = AppCLI()
	if _, err := AppCLI(); err != nil {
		st.CanInstall = err.Error()
	}
	st.DirOnPath = LoginPathHas(filepath.Dir(st.Path))
	st.Resolves = LookLoginPath("hopsesh")
	st.Profile = profileFile()
	st.PathLine = `export PATH="$HOME/.local/bin:$PATH"`
	if b, err := os.ReadFile(st.Profile); err == nil {
		st.PathAdded = strings.Contains(string(b), blockStart)
	}
	fi, err := os.Lstat(st.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		st.State = CLIMissing
	case err != nil:
		st.State = CLIForeign
	case fi.Mode()&os.ModeSymlink != 0:
		st.Target, _ = os.Readlink(st.Path)
		_, terr := os.Stat(st.Path)
		switch {
		case terr != nil:
			st.State = CLIDangling
		case st.AppCLI != "" && filepath.Clean(st.Target) == filepath.Clean(st.AppCLI):
			st.State = CLIOurs
		case isHopseshApp(st.Target):
			st.State = CLIOtherApp
		default:
			st.State = CLIForeign
		}
	default:
		st.State = CLIStandalone
	}
	return st
}

// InstallCLI links ~/.local/bin/hopsesh to this app's tool. It replaces only links that
// hopsesh made (into a hopsesh.app, or broken ones); a standalone copy or someone else's
// link is replaced only with force, and kept as hopsesh.bak-<time>.
func InstallCLI(force bool) (CLIStatus, error) {
	if runtime.GOOS == "windows" {
		return CLIStatus{}, errors.New("not available on Windows")
	}
	cli, err := AppCLI()
	if err != nil {
		return CheckCLI(), err
	}
	st := CheckCLI()
	switch st.State {
	case CLIOurs:
		return st, nil
	case CLIStandalone, CLIForeign:
		if !force {
			return st, fmt.Errorf("%s is %s; replace it only if you mean to (it will be kept as a backup)", st.Path, map[string]string{CLIStandalone: "a separately installed copy of hopsesh", CLIForeign: "a link to " + st.Target}[st.State])
		}
		if err := os.Rename(st.Path, st.Path+".bak-"+time.Now().Format("20060102-150405")); err != nil {
			return st, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(st.Path), 0o755); err != nil {
		return st, err
	}
	tmp := st.Path + ".hopsesh-tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(cli, tmp); err != nil {
		return st, err
	}
	if err := os.Rename(tmp, st.Path); err != nil {
		_ = os.Remove(tmp)
		return st, err
	}
	return CheckCLI(), nil
}

// UninstallCLI removes the link when it points into a hopsesh.app (or is broken), and the
// PATH block hopsesh added, if any.
func UninstallCLI() error {
	st := CheckCLI()
	switch st.State {
	case CLIMissing:
	case CLIOurs, CLIOtherApp, CLIDangling:
		if err := os.Remove(st.Path); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%s was not installed by the hopsesh app; leaving it", st.Path)
	}
	return RemovePathBlock()
}

const (
	blockStart = "# >>> hopsesh >>>"
	blockEnd   = "# <<< hopsesh <<<"
)

// profileFile is the login-shell file "Add to PATH" changes.
func profileFile() string {
	home, _ := os.UserHomeDir()
	switch filepath.Base(loginShell()) {
	case "bash":
		return filepath.Join(home, ".bash_profile")
	case "fish":
		return filepath.Join(home, ".config", "fish", "conf.d", "hopsesh.fish")
	}
	return filepath.Join(home, ".zprofile")
}

// AddToPath appends a marked block that puts ~/.local/bin on PATH to the login-shell
// profile (only when the user asks). It returns the file it changed.
func AddToPath() (string, error) {
	f := profileFile()
	b, _ := os.ReadFile(f)
	if strings.Contains(string(b), blockStart) {
		return f, nil
	}
	line := `export PATH="$HOME/.local/bin:$PATH"`
	if strings.HasSuffix(f, ".fish") {
		line = "fish_add_path -g $HOME/.local/bin"
	}
	block := "\n" + blockStart + "\n# Added by the hopsesh app so the hopsesh command works in new terminals.\n" + line + "\n" + blockEnd + "\n"
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return f, err
	}
	fh, err := os.OpenFile(f, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return f, err
	}
	defer fh.Close()
	_, err = fh.WriteString(block)
	return f, err
}

// RemovePathBlock removes the block AddToPath added, if present.
func RemovePathBlock() error {
	f := profileFile()
	b, err := os.ReadFile(f)
	if err != nil {
		return nil
	}
	s := string(b)
	i := strings.Index(s, blockStart)
	j := strings.Index(s, blockEnd)
	if i < 0 || j < i {
		return nil
	}
	start := i
	if start > 0 && s[start-1] == '\n' {
		start--
	}
	end := j + len(blockEnd)
	if end < len(s) && s[end] == '\n' {
		end++
	}
	s = s[:start] + s[end:]
	return os.WriteFile(f, []byte(s), 0o644)
}

// SkillBin is how agents should run hopsesh: plain "hopsesh" when the user's login shell
// finds it, otherwise an absolute path (the linked tool, the app's tool, or this program).
func SkillBin() string {
	if LookLoginPath("hopsesh") != "" {
		return "hopsesh"
	}
	if st := CheckCLI(); st.State == CLIOurs || st.State == CLIOtherApp || st.State == CLIStandalone {
		return st.Path
	}
	if cli, err := AppCLI(); err == nil {
		return cli
	}
	exe, err := os.Executable()
	if err != nil {
		return "hopsesh"
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe
}
