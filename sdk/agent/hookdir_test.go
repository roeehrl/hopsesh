package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// osDirFS is a HookDirFS on this machine; failRename makes the first rename to a path
// ending so fail.
type osDirFS struct {
	failRename string
	failed     *bool
}

func (osDirFS) ReadDir(p string) ([]fs.FileInfo, error) {
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	var out []fs.FileInfo
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, fi)
	}
	return out, nil
}

func (osDirFS) ReadFile(p string, limit int64) ([]byte, error) {
	b, err := os.ReadFile(p)
	if err == nil && int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is too large", p)
	}
	return b, err
}

func (osDirFS) WriteFile(p string, b []byte, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, perm)
}

func (f osDirFS) Rename(from, to string) error {
	if f.failRename != "" && strings.HasSuffix(to, f.failRename) && !*f.failed {
		*f.failed = true
		return errors.New("rename failed")
	}
	return os.Rename(from, to)
}

func (osDirFS) RemoveAll(p string) error { return os.RemoveAll(p) }
func (osDirFS) MkdirAll(p string) error  { return os.MkdirAll(p, 0o755) }

func pluginDir(root string, version string) *HookDir {
	want := map[string][]byte{"index.ts": []byte("export default 1\n"), "config.json": []byte(`{"argv":["/bin/hopsesh","` + version + `"]}`), "lib/util.ts": []byte("x\n")}
	return &HookDir{Root: "config", Path: filepath.Join(root, "plugins", "hopsesh"), Staging: filepath.Join(root, ".hopsesh"),
		Merge: func(map[string][]byte) (map[string][]byte, error) { return want, nil },
		Has:   func(have map[string][]byte) bool { return sameHookFiles(have, want) },
	}
}

func TestHookDirInstallReplaceRemove(t *testing.T) {
	root := t.TempDir()
	pa := PathFor(runtime.GOOS)
	fsys := osDirFS{}
	d := pluginDir(root, "v1")
	if have, err := ReadHookDir(fsys, pa, d); err != nil || len(have) != 0 || d.Has(have) {
		t.Fatalf("a missing folder reads as empty: %v, %v", have, err)
	}
	// An empty folder left there (by the user, or an earlier removal) is replaced.
	os.MkdirAll(filepath.Join(d.Path, "empty"), 0o755)
	if changed, err := InstallHookDir(fsys, pa, d); err != nil || !changed {
		t.Fatalf("install: %v, %v", changed, err)
	}
	have, err := ReadHookDir(fsys, pa, d)
	if err != nil || !d.Has(have) || string(have["lib/util.ts"]) != "x\n" {
		t.Fatalf("installed: %v, %v", have, err)
	}
	if changed, err := InstallHookDir(fsys, pa, d); err != nil || changed {
		t.Fatalf("installing again changes nothing: %v, %v", changed, err)
	}
	// Nothing is left beside the folder for the agent to load.
	entries, _ := os.ReadDir(filepath.Join(root, "plugins"))
	if len(entries) != 1 || entries[0].Name() != "hopsesh" {
		t.Fatalf("plugins folder holds %v", entries)
	}

	// A new version replaces the whole folder: a file it no longer has goes.
	os.WriteFile(filepath.Join(d.Path, "stale.ts"), []byte("old"), 0o644)
	d2 := pluginDir(root, "v2")
	if changed, err := InstallHookDir(fsys, pa, d2); err != nil || !changed {
		t.Fatalf("replace: %v, %v", changed, err)
	}
	if have, _ := ReadHookDir(fsys, pa, d2); !d2.Has(have) || have["stale.ts"] != nil {
		t.Fatalf("replaced: %v", have)
	}

	// A failed swap leaves the installed folder as it was.
	d3 := pluginDir(root, "v3")
	if _, err := InstallHookDir(osDirFS{failRename: "hopsesh", failed: new(bool)}, pa, d3); err == nil {
		t.Fatal("the failed rename must be reported")
	}
	if have, _ := ReadHookDir(fsys, pa, d3); !d2.Has(have) {
		t.Fatalf("a failed replace changed the folder: %v", have)
	}
	if _, err := os.Stat(filepath.Join(d3.Staging, ".hopsesh.hopsesh-new")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a failed replace left its new copy")
	}

	// Remove with no Remove function takes the folder away.
	if changed, err := RemoveHookDir(fsys, pa, d2); err != nil || !changed {
		t.Fatalf("remove: %v, %v", changed, err)
	}
	if _, err := os.Stat(d2.Path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the folder is still there")
	}
	if changed, err := RemoveHookDir(fsys, pa, d2); err != nil || changed {
		t.Fatalf("removing again changes nothing: %v, %v", changed, err)
	}
}

func TestHookDirStagingAndPartialRemove(t *testing.T) {
	root := t.TempDir()
	pa := PathFor(runtime.GOOS)
	d := pluginDir(root, "v1")
	d.Staging = filepath.Join(root, "staging")
	d.Remove = func(have map[string][]byte) (map[string][]byte, error) {
		return map[string][]byte{"user.ts": have["user.ts"]}, nil // keeps a file the user added
	}
	if _, err := InstallHookDir(osDirFS{}, pa, d); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "plugins")); len(entries) != 1 {
		t.Fatalf("staging copies landed beside the folder: %v", entries)
	}
	os.WriteFile(filepath.Join(d.Path, "user.ts"), []byte("mine"), 0o644)
	if _, err := RemoveHookDir(osDirFS{}, pa, d); err != nil {
		t.Fatal(err)
	}
	have, _ := ReadHookDir(osDirFS{}, pa, d)
	if len(have) != 1 || string(have["user.ts"]) != "mine" {
		t.Fatalf("after remove: %v", have)
	}
	bad := &HookDir{Path: filepath.Join(root, "bad"), Staging: filepath.Join(root, "staging"), Merge: func(map[string][]byte) (map[string][]byte, error) {
		return map[string][]byte{"../escape": []byte("x")}, nil
	}}
	if _, err := InstallHookDir(osDirFS{}, pa, bad); err == nil {
		t.Fatal("a file outside the folder must be refused")
	}
	if _, err := os.Stat(filepath.Join(root, "escape")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a file was written outside the folder")
	}
	// Staging is required, and never inside the folder it stages.
	for _, s := range []string{"", d.Path, filepath.Join(d.Path, "sub")} {
		if _, err := InstallHookDir(osDirFS{}, pa, &HookDir{Path: d.Path, Staging: s, Merge: d.Merge}); err == nil {
			t.Errorf("staging %q must be refused", s)
		}
	}
}

// A crash between the two renames leaves no folder and its old copy in Staging; the next
// install puts the old copy back before merging, so nothing the user added is lost.
func TestHookDirRecoversFromACrash(t *testing.T) {
	root := t.TempDir()
	pa := PathFor(runtime.GOOS)
	fsys := osDirFS{}
	d := pluginDir(root, "v1")
	keep := d.Merge
	d.Merge = func(have map[string][]byte) (map[string][]byte, error) {
		out, _ := keep(have)
		if note, ok := have["user-note"]; ok {
			out = map[string][]byte{"user-note": note, "index.ts": out["index.ts"], "config.json": out["config.json"], "lib/util.ts": out["lib/util.ts"]}
		}
		return out, nil
	}
	if _, err := InstallHookDir(fsys, pa, d); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(d.Path, "user-note"), []byte("mine"), 0o644)
	// The crash: the folder moved out to the old copy, the new copy never moved in.
	os.MkdirAll(d.Staging, 0o755)
	if err := os.Rename(d.Path, filepath.Join(d.Staging, ".hopsesh.hopsesh-old")); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHookDir(fsys, pa, d); err != nil {
		t.Fatal(err)
	}
	have, err := ReadHookDir(fsys, pa, d)
	if err != nil || string(have["user-note"]) != "mine" || have["index.ts"] == nil {
		t.Fatalf("after recovery: %v, %v", have, err)
	}
	// The same for a removal.
	os.Rename(d.Path, filepath.Join(d.Staging, ".hopsesh.hopsesh-old"))
	if changed, err := RemoveHookDir(fsys, pa, d); err != nil || !changed {
		t.Fatalf("remove after a crash: %v, %v", changed, err)
	}
	for _, p := range []string{d.Path, filepath.Join(d.Staging, ".hopsesh.hopsesh-old")} {
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is still there", p)
		}
	}
}

// A hook file sits directly in the root it names; a hook folder anywhere inside it.
func TestHookPlacement(t *testing.T) {
	pa := PosixPath{}
	in := Install{Roots: map[string]string{"data": "/d", "config": "/c"}, MainRoot: "data"}
	for _, c := range []struct {
		f    HookFile
		want bool
	}{
		{HookFile{Path: "/d/settings.json"}, true},
		{HookFile{Path: "/d/sub/settings.json"}, false},
		{HookFile{Path: "/c/settings.json"}, false},
		{HookFile{Root: "config", Path: "/c/settings.json"}, true},
		{HookFile{Root: "nope", Path: "/c/settings.json"}, false},
		{HookFile{Path: "/d"}, false},
	} {
		if got := c.f.In(in, pa); got != c.want {
			t.Errorf("%+v: In = %v", c.f, got)
		}
	}
	if !(&HookDir{Root: "config", Path: "/c/plugins/hopsesh", Staging: "/c/.hopsesh"}).In(in, pa) {
		t.Error("a plugin folder inside its root")
	}
	for _, d := range []HookDir{
		{Root: "config", Path: "/c", Staging: "/c/.hopsesh"},                          // the root itself
		{Path: "/c/plugins/hopsesh", Staging: "/c/.hopsesh"},                          // another root's folder
		{Root: "config", Path: "/c/plugins/hopsesh"},                                  // no staging
		{Root: "config", Path: "/c/plugins/hopsesh", Staging: "/tmp/x"},               // staging outside the root
		{Root: "config", Path: "/c/plugins/hopsesh", Staging: "/c/plugins/hopsesh/s"}, // staging inside the folder
	} {
		if d.In(in, pa) {
			t.Errorf("%+v is not a hook folder", d)
		}
	}
}
