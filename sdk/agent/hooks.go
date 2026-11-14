package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
)

// MovementHookIntegrator describes passive, local lifecycle hooks. It never invokes
// an agent or requests a model turn. Unsupported installations retain a file contract
// for removal, with a human-readable Reason that prevents installation. args
// pins launcher settings/state directories through literal CLI flags.
type MovementHookIntegrator interface {
	MovementHooks(h Host, in Install, bin string, args ...string) HookIntegration
}

type HookIntegration struct {
	File *HookFile
	// Dir is a folder hopsesh owns whole (an agent plugin), for an agent whose hooks are
	// not entries in a shared file; nil when File is used.
	Dir            *HookDir
	Reason         string
	Evidence       string
	DisabledReason string // vendor setting prevents delivery; install/remove must preserve it
}

// HookFile edits only hooks owned by this integration in a shared vendor file.
// All functions are pure; the app owns bounded local reads and atomic writes.
type HookFile struct {
	// Root names the Spec root the file sits directly in ("" for the main root): the only
	// place the app edits it.
	Root   string
	Path   string
	Merge  func([]byte) ([]byte, error)
	Remove func([]byte) ([]byte, error)
	Has    func([]byte) bool
	// Runs finds this agent's notice hook in the file whatever its arguments, and returns
	// the program it runs: how another machine's hook is checked from here, where the
	// exact arguments hopsesh there used are unknown. nil: only Has is known.
	Runs func([]byte) (program string, ok bool)
}

// In reports whether the file sits directly in its root's folder of in.
func (f *HookFile) In(in Install, pa Path) bool {
	return DirectlyIn(in, f.Root, f.Path, pa)
}

// DirectlyIn reports whether p is a file directly in the folder of in's root ("" for the
// main root).
func DirectlyIn(in Install, root, p string, pa Path) bool {
	dir := in.Main()
	if root != "" {
		dir = in.Root(root)
	}
	if dir == "" || p == "" {
		return false
	}
	rel, ok := pa.Rel(dir, p)
	return ok && rel != "." && !strings.ContainsAny(rel, `/\`)
}

// noticeHookIn finds a notice hook command (NoticeHookCommand) and its agent.
var noticeHookIn = regexp.MustCompile(`'((?:[^'\\]|\\.)+)' notice-hook --agent '([a-z0-9-]+)'`)

// NoticeHookProgram finds agent id's notice hook command (NoticeHookCommand, also inside
// a JSON string) in a hook file, and returns the program it runs.
func NoticeHookProgram(b []byte, id ID) (string, bool) {
	for _, m := range noticeHookIn.FindAllSubmatch(bytes.ReplaceAll(b, []byte(`\"`), []byte(`"`)), -1) {
		if string(m[2]) == string(id) {
			return string(m[1]), true
		}
	}
	return "", false
}

// NoticeHookCommand quotes every argument; no hook payload is interpolated into
// the shell. The executable must be an absolute installed CLI path.
func NoticeHookCommand(bin string, id ID, profile string, args ...string) (string, error) {
	if bin == "" || strings.ContainsAny(bin+string(id)+profile+strings.Join(args, ""), "\x00\r\n") {
		return "", fmt.Errorf("invalid notice hook command")
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	command := quote(bin) + " notice-hook --agent " + quote(string(id)) + " --profile " + quote(profile)
	for _, arg := range args {
		command += " " + quote(arg)
	}
	return command, nil
}

// NoticeHookCall is one call of the notice hook, as the agent's hook sent it. Every field
// is untrusted input; the core checks them after decoding.
type NoticeHookCall struct {
	Session string // the session's id
	Path    string // its transcript file, when the agent names one ("" for a database)
	Event   string // "SessionStart" or "UserPromptSubmit"
	Source  string // SessionStart: startup, resume, clear or compact, when the agent says
}

// NoticeHookAnswer is hopsesh's answer to a call: a notice for the session, or with Block
// the reason the session's original is blocked. Both empty: nothing to say.
type NoticeHookAnswer struct {
	Text  string
	Block bool
}

// NoticeHookProtocol is for agents whose notice hook speaks other JSON than the command
// hooks of Claude Code and Codex (an agent plugin that runs the notice hook itself).
// Decode reads a call (false: not one to answer). Encode writes the answer, or nil for
// none; it is called with an empty answer too, so a plugin can tell "nothing to say" from
// a hook that failed (which writes nothing). Without it the core speaks the command-hook
// JSON.
type NoticeHookProtocol interface {
	DecodeNoticeHook(raw []byte) (NoticeHookCall, bool)
	EncodeNoticeHook(call NoticeHookCall, answer NoticeHookAnswer) []byte
}

// NoticeHookPowerShellScript uses single-quoted literals (doubled apostrophes),
// explicitly passes UTF-8 stdin, and combines --profile= so Windows PowerShell
// 5.1 cannot discard an empty native argument. Suppress progress before any
// command can trigger first-use module initialization. Allocate the input buffer
// through .NET so the wrapper itself needs no cmdlet module. Genuine errors keep
// their stderr stream; no payload becomes script text.
func NoticeHookPowerShellScript(bin string, id ID, profile string, args ...string) (string, error) {
	if _, err := NoticeHookCommand(bin, id, profile, args...); err != nil {
		return "", err
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	extra := ""
	for _, arg := range args {
		extra += " " + quote(arg)
	}
	return "$ProgressPreference='SilentlyContinue'; $ErrorActionPreference='Stop'; $OutputEncoding=[System.Text.UTF8Encoding]::new($false); [Console]::InputEncoding=$OutputEncoding; [Console]::OutputEncoding=$OutputEncoding; $buffer=[char[]]::new(65537); $count=[Console]::In.ReadBlock($buffer,0,$buffer.Length); $payload=[string]::new($buffer,0,$count); $payload | & " + quote(bin) + " notice-hook --agent " + quote(string(id)) + " " + quote("--profile="+profile) + extra + "; exit $LASTEXITCODE", nil
}

// EncodedPowerShellHook is a whitespace-only command launch usable under Codex's
// selected Windows shell (PowerShell, cmd fallback, or Git Bash). Encoding the
// fixed script avoids outer-shell expansion of paths, %, $, ! and embedded quotes.
// Explicit Text output avoids implicit CLIXML serialization by nested PowerShell.
func EncodedPowerShellHook(script string) string {
	chars := utf16.Encode([]rune(script))
	raw := make([]byte, len(chars)*2)
	for i, c := range chars {
		binary.LittleEndian.PutUint16(raw[i*2:], c)
	}
	return "powershell.exe -NoLogo -NoProfile -NonInteractive -OutputFormat Text -EncodedCommand " + base64.StdEncoding.EncodeToString(raw)
}

// HookDir is a folder hopsesh owns whole, holding several files: an agent plugin
// ({config}/plugins/hopsesh) rather than entries in a shared settings file. Its functions
// are pure, over the folder's files by slash-separated path. InstallHookDir and
// RemoveHookDir build the new folder aside and swap it in with renames, so the agent never
// loads a half-written one. The caller holds the machine's hook lock around them: two at
// once would share the staging copies.
type HookDir struct {
	// Root names the Spec root the folder is inside ("" for the main root); Path is the
	// folder, anywhere inside that root's folder.
	Root string
	Path string
	// Staging is where the new and old copies wait while the folder is replaced: a folder
	// inside the same root, outside Path, that the agent does not scan for plugins (not
	// beside Path in plugins/). Required.
	Staging string
	// Merge returns the folder's files with hopsesh's installed, from what is there now
	// (empty when there is no folder).
	Merge func(existing map[string][]byte) (map[string][]byte, error)
	// Remove returns what stays after hopsesh's are taken out; nil or empty removes the
	// folder. A nil Remove always removes it.
	Remove func(existing map[string][]byte) (map[string][]byte, error)
	// Has reports whether the folder runs this hopsesh, with these arguments.
	Has func(existing map[string][]byte) bool
	// Runs returns the program the folder's notice hook runs, whatever its arguments, as
	// HookFile.Runs.
	Runs func(existing map[string][]byte) (program string, ok bool)
}

// In reports whether the folder is inside its root's folder of in, and its Staging is in
// that root too and not inside the folder.
func (d *HookDir) In(in Install, pa Path) bool {
	dir := in.Main()
	if d.Root != "" {
		dir = in.Root(d.Root)
	}
	if dir == "" || d.Path == "" || d.Staging == "" {
		return false
	}
	rel, ok := pa.Rel(dir, d.Path)
	if !ok || rel == "." {
		return false
	}
	if _, ok := pa.Rel(dir, d.Staging); !ok {
		return false
	}
	_, inside := pa.Rel(d.Path, d.Staging)
	return !inside
}

// HookDirFS is the filesystem a HookDir is read and replaced through, with that
// machine's paths. WriteFile creates parent folders; Rename moves a folder. ReadDir
// describes a symbolic link as a link (Lstat), never as what it points to, so a link is
// refused rather than copied.
type HookDirFS interface {
	ReadDir(p string) ([]fs.FileInfo, error)
	ReadFile(p string, limit int64) ([]byte, error)
	WriteFile(p string, b []byte, perm fs.FileMode) error
	MkdirAll(p string) error
	Rename(from, to string) error
	RemoveAll(p string) error
}

// Limits on a HookDir, which holds a few small files.
const (
	hookDirMaxFiles = 64
	hookDirMaxBytes = 4 << 20
)

// ReadHookDir reads the folder's files (none when it does not exist).
func ReadHookDir(fsys HookDirFS, pa Path, d *HookDir) (map[string][]byte, error) {
	out := map[string][]byte{}
	var total int64
	var walk func(dir, rel string) error
	walk = func(dir, rel string) error {
		entries, err := fsys.ReadDir(dir)
		if err != nil {
			if rel == "" && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		for _, e := range entries {
			name := path.Join(rel, e.Name())
			switch {
			case e.IsDir():
				if err := walk(pa.Join(dir, e.Name()), name); err != nil {
					return err
				}
			case e.Mode().IsRegular():
				if len(out) >= hookDirMaxFiles {
					return fmt.Errorf("%s holds more than %d files; not changing it", d.Path, hookDirMaxFiles)
				}
				b, err := fsys.ReadFile(pa.Join(dir, e.Name()), hookDirMaxBytes-total)
				if err != nil {
					return err
				}
				total += int64(len(b))
				out[name] = b
			default:
				return fmt.Errorf("%s holds %s, which is not a file; not changing it", d.Path, name)
			}
		}
		return nil
	}
	return out, walk(d.Path, "")
}

// InstallHookDir puts hopsesh's files in the folder (Merge) and reports whether it changed
// anything.
func InstallHookDir(fsys HookDirFS, pa Path, d *HookDir) (bool, error) {
	if err := recoverHookDir(fsys, pa, d); err != nil {
		return false, err
	}
	old, err := ReadHookDir(fsys, pa, d)
	if err != nil {
		return false, err
	}
	files, err := d.Merge(old)
	if err != nil {
		return false, err
	}
	if sameHookFiles(old, files) {
		return false, nil
	}
	return true, replaceHookDir(fsys, pa, d, len(old) > 0, files)
}

// RemoveHookDir takes hopsesh's files out (Remove, or the whole folder) and reports
// whether it changed anything.
func RemoveHookDir(fsys HookDirFS, pa Path, d *HookDir) (bool, error) {
	if err := recoverHookDir(fsys, pa, d); err != nil {
		return false, err
	}
	old, err := ReadHookDir(fsys, pa, d)
	if err != nil || len(old) == 0 {
		return false, err
	}
	var files map[string][]byte
	if d.Remove != nil {
		if files, err = d.Remove(old); err != nil {
			return false, err
		}
		if sameHookFiles(old, files) {
			return false, nil
		}
	}
	return true, replaceHookDir(fsys, pa, d, true, files)
}

// hookDirStaging is where the new and the old copy of the folder wait.
func hookDirStaging(pa Path, d *HookDir) (next, prev string, err error) {
	if d.Path == "" || d.Staging == "" || !pa.IsAbs(d.Path) || !pa.IsAbs(d.Staging) {
		return "", "", fmt.Errorf("the hook folder %q needs an absolute path and staging folder", d.Path)
	}
	if _, inside := pa.Rel(d.Path, d.Staging); inside {
		return "", "", fmt.Errorf("the staging folder %s is inside the hook folder", d.Staging)
	}
	base := "." + pa.Base(d.Path) + ".hopsesh"
	return pa.Join(d.Staging, base+"-new"), pa.Join(d.Staging, base+"-old"), nil
}

// recoverHookDir finishes a replace that stopped between its two renames: the folder is
// gone and its old copy waits in Staging, so the old copy goes back.
func recoverHookDir(fsys HookDirFS, pa Path, d *HookDir) error {
	_, prev, err := hookDirStaging(pa, d)
	if err != nil {
		return err
	}
	if _, err := fsys.ReadDir(d.Path); !errors.Is(err, fs.ErrNotExist) {
		return nil // the folder is there (or unreadable, which reading it reports)
	}
	if _, err := fsys.ReadDir(prev); err != nil {
		return nil // no old copy
	}
	return fsys.Rename(prev, d.Path)
}

// replaceHookDir writes files into a new copy in Staging, moves the folder there, moves
// the new copy in, then deletes the old one. With no files the folder only moves out.
// A failure puts the old folder back; a crash between the renames is undone by the next
// install or removal (recoverHookDir).
func replaceHookDir(fsys HookDirFS, pa Path, d *HookDir, exists bool, files map[string][]byte) error {
	next, prev, err := hookDirStaging(pa, d)
	if err != nil {
		return err
	}
	staging := d.Staging
	for _, p := range []string{next, prev} {
		if err := fsys.RemoveAll(p); err != nil {
			return err
		}
	}
	for _, p := range []string{staging, pa.Dir(d.Path)} {
		if err := fsys.MkdirAll(p); err != nil {
			return err
		}
	}
	if len(files) > 0 {
		names := make([]string, 0, len(files))
		for name := range files {
			if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, `\`) {
				return fmt.Errorf("invalid hook file name %q", name)
			}
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := fsys.WriteFile(pa.Join(append([]string{next}, strings.Split(name, "/")...)...), files[name], 0o644); err != nil {
				_ = fsys.RemoveAll(next)
				return err
			}
		}
	}
	if exists {
		if err := fsys.Rename(d.Path, prev); err != nil {
			_ = fsys.RemoveAll(next)
			return err
		}
	} else if err := fsys.RemoveAll(d.Path); err != nil { // empty folders only: no files were read there
		_ = fsys.RemoveAll(next)
		return err
	}
	if len(files) > 0 {
		if err := fsys.Rename(next, d.Path); err != nil {
			if exists {
				_ = fsys.Rename(prev, d.Path)
			}
			_ = fsys.RemoveAll(next)
			return err
		}
	}
	return fsys.RemoveAll(prev)
}

func sameHookFiles(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok || !bytes.Equal(v, w) {
			return false
		}
	}
	return true
}
