package integrate

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// isAppExe reports whether exe is the desktop app (a window program), never something to
// tell agents to run.
func isAppExe(exe string) bool {
	return strings.EqualFold(filepath.Base(exe), "hopsesh-app.exe")
}

// loginPATH is the PATH a newly opened terminal gets: the system PATH, then the user's,
// from the registry (this process's PATH predates any change made since it started).
func loginPATH() string {
	var parts []string
	for _, k := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
		{registry.CURRENT_USER, `Environment`},
	} {
		if v := readPath(k.root, k.path); v != "" {
			parts = append(parts, v)
		}
	}
	if len(parts) == 0 {
		return os.Getenv("PATH")
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

func readPath(root registry.Key, path string) string {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Path")
	if err != nil {
		return ""
	}
	if x, err := registry.ExpandString(v); err == nil {
		return x
	}
	return v
}

// lookIn finds name in dir with any of the PATHEXT extensions.
func lookIn(dir, name string) string {
	exts := strings.Split(strings.ToLower(os.Getenv("PATHEXT")), ";")
	if len(exts) == 1 && exts[0] == "" {
		exts = []string{".com", ".exe", ".bat", ".cmd"}
	}
	for _, e := range exts {
		if e == "" {
			continue
		}
		f := filepath.Join(dir, name+e)
		if fi, err := os.Stat(f); err == nil && !fi.IsDir() {
			return f
		}
	}
	return ""
}

// setUserPath rewrites the user's PATH in the registry, tells running programs (Explorer,
// new terminals) about it, and updates this process's PATH to match.
func setUserPath(change func([]string) []string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	old, _, err := k.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	var dirs []string
	for _, d := range strings.Split(old, ";") {
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	if err := k.SetExpandStringValue("Path", strings.Join(change(dirs), ";")); err != nil {
		return err
	}
	broadcastEnvironment()
	return os.Setenv("PATH", loginPATH())
}

// broadcastEnvironment sends WM_SETTINGCHANGE "Environment", as the system settings
// dialog does, so Explorer gives new windows the new PATH.
func broadcastEnvironment() {
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xffff, 0x1a, 0x2
	env, _ := syscall.UTF16PtrFromString("Environment")
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	var result uintptr
	_, _, _ = proc.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result)))
}
