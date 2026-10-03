package integrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestLookInFollowsPATHEXT(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT")
	if lookIn(dir, "hopsesh") != "" {
		t.Fatal("nothing there yet")
	}
	os.WriteFile(filepath.Join(dir, "hopsesh.exe"), []byte("MZ"), 0o644)
	if got := lookIn(dir, "hopsesh"); !strings.EqualFold(got, filepath.Join(dir, "hopsesh.exe")) {
		t.Fatalf("got %q", got)
	}
}

func TestSkillBinIsNeverTheApp(t *testing.T) {
	if !isAppExe(`C:\Users\x\AppData\Local\Programs\hopsesh\HOPSESH-APP.EXE`) || isAppExe(`C:\x\hopsesh.exe`) {
		t.Fatal("isAppExe")
	}
}

// The app's folder goes on the user's PATH and comes off again; the registry value is
// restored afterwards. Changes the user environment, so it runs only in CI.
func TestInstallCLIOnUserPath(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("changes the user's PATH; CI only")
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	old, typ, oldErr := k.GetStringValue("Path")
	t.Cleanup(func() {
		if oldErr != nil {
			k.DeleteValue("Path")
		} else if typ == registry.EXPAND_SZ {
			k.SetExpandStringValue("Path", old)
		} else {
			k.SetStringValue("Path", old)
		}
	})

	dir := t.TempDir()
	app := filepath.Join(dir, "hopsesh-app.exe")
	os.WriteFile(app, []byte("MZ"), 0o755)
	os.WriteFile(filepath.Join(dir, "hopsesh.exe"), []byte("MZ"), 0o755)
	executable = func() (string, error) { return app, nil }
	defer func() { executable = os.Executable }()

	if st := CheckCLI(); st.State != CLIMissing && st.State != CLIStandalone {
		t.Fatalf("before: %+v", st)
	}
	st, err := InstallCLI(true)
	if err != nil || st.State != CLIOurs || !st.DirOnPath {
		t.Fatalf("install: %+v %v", st, err)
	}
	if v, _, _ := k.GetStringValue("Path"); !strings.HasPrefix(v, dir+";") && v != dir {
		t.Fatalf("user PATH: %q", v)
	}
	if SkillBin() != "hopsesh" {
		t.Fatalf("skill bin: %s", SkillBin())
	}
	if err := UninstallCLI(); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := k.GetStringValue("Path"); strings.Contains(strings.ToLower(v), strings.ToLower(dir)) {
		t.Fatalf("still on PATH: %q", v)
	}
}
