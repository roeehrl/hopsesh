//go:build windows

package pty

import (
	"debug/pe"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A Windows tab runs in a pseudoconsole. The system's (kernel32's CreatePseudoConsole) is
// as old as the Windows build; Microsoft's own conpty.dll, from the
// Microsoft.Windows.Console.ConPTY package that Windows Terminal, VS Code and Zed ship,
// starts its newer OpenConsole.exe instead, which passes a program's control sequences
// through to the emulator unchanged. The app carries that pair in a "conpty" folder
// beside it (conpty.dll, and OpenConsole.exe in the folder for the machine's
// architecture: x64 or arm64); without it, the system's is used.
//
// charmbracelet/x/conpty (behind x/xpty) calls kernel32's functions directly and cannot
// load another conpty.dll, so this file creates the pseudoconsole and starts the program
// in it itself.

// conptyAPI is one pseudoconsole implementation's three functions.
type conptyAPI struct {
	create, resize, close uintptr
	name                  string
}

var (
	apiMu sync.Mutex
	apis  = map[string]*conptyAPI{}
)

// loadConpty is the bundled conpty.dll in dir when it and its OpenConsole.exe are there,
// else the system's pseudoconsole.
func loadConpty(dir string) (*conptyAPI, error) {
	apiMu.Lock()
	defer apiMu.Unlock()
	if a, ok := apis[dir]; ok {
		return a, nil
	}
	a, err := loadBundled(dir)
	if err != nil {
		if a, err = loadSystem(); err != nil {
			return nil, err
		}
	}
	apis[dir] = a
	return a, nil
}

// BundledConpty says whether dir holds a conpty.dll with the OpenConsole.exe it would
// start on this machine (and so whether a tab would use them), and where they are.
func BundledConpty(dir string) (dll, host string, ok bool) {
	if dir == "" || !filepath.IsAbs(dir) {
		return "", "", false
	}
	dll = filepath.Join(dir, "conpty.dll")
	host = filepath.Join(dir, hostArch(), "OpenConsole.exe")
	if !isFile(dll) || !isFile(host) {
		return "", "", false
	}
	return dll, host, true
}

func loadBundled(dir string) (*conptyAPI, error) {
	dll, _, ok := BundledConpty(dir)
	if !ok {
		return nil, errors.New("no bundled conpty")
	}
	h, err := windows.LoadLibraryEx(dll, 0, windows.LOAD_WITH_ALTERED_SEARCH_PATH)
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", dll, err)
	}
	a := &conptyAPI{name: "conpty (bundled)"}
	for name, p := range map[string]*uintptr{"ConptyCreatePseudoConsole": &a.create, "ConptyResizePseudoConsole": &a.resize, "ConptyClosePseudoConsole": &a.close} {
		if *p, err = windows.GetProcAddress(h, name); err != nil {
			_ = windows.FreeLibrary(h)
			return nil, fmt.Errorf("%s has no %s: %w", dll, name, err)
		}
	}
	return a, nil
}

func loadSystem() (*conptyAPI, error) {
	k := windows.NewLazySystemDLL("kernel32.dll")
	a := &conptyAPI{name: "conpty (system)"}
	for name, p := range map[string]*uintptr{"CreatePseudoConsole": &a.create, "ResizePseudoConsole": &a.resize, "ClosePseudoConsole": &a.close} {
		proc := k.NewProc(name)
		if err := proc.Find(); err != nil {
			return nil, errors.New("this Windows has no pseudoconsole (it needs Windows 10 1809 or later)")
		}
		*p = proc.Addr()
	}
	return a, nil
}

// hostArch is the folder of the OpenConsole.exe conpty.dll starts: the machine's own
// architecture (an x64 hopsesh on an ARM64 machine uses the arm64 one).
func hostArch() string {
	var process, native uint16
	if err := windows.IsWow64Process2(windows.CurrentProcess(), &process, &native); err == nil {
		switch native {
		case pe.IMAGE_FILE_MACHINE_ARM64:
			return "arm64"
		case pe.IMAGE_FILE_MACHINE_AMD64:
			return "x64"
		case pe.IMAGE_FILE_MACHINE_I386:
			return "x86"
		}
	}
	switch runtime.GOARCH {
	case "arm64":
		return "arm64"
	case "386":
		return "x86"
	}
	return "x64"
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// coord is a COORD passed by value.
func coord(cols, rows int) uintptr {
	c := windows.Coord{X: int16(cols), Y: int16(rows)} //nolint:gosec // clamped to 1000
	return uintptr(*(*uint32)(unsafe.Pointer(&c)))
}

// conTerm is a program in a pseudoconsole.
type conTerm struct {
	api     *conptyAPI
	in, out *os.File // the input pipe's write end, the output pipe's read end

	mu      sync.Mutex
	hpc     windows.Handle // 0 once closed
	process windows.Handle // 0 once waited for
	pid     uint32
}

func start(argv []string, dir string, env []string, cols, rows int, conptyDir string) (backend, error) {
	api, err := loadConpty(conptyDir)
	if err != nil {
		return nil, err
	}
	prog, err := lookPath(argv[0], dir, env)
	if err != nil {
		return nil, err
	}
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		_ = windows.CloseHandle(inR)
		_ = windows.CloseHandle(inW)
		return nil, err
	}
	var hpc windows.Handle
	r, _, _ := syscall.SyscallN(api.create, coord(cols, rows), uintptr(inR), uintptr(outW), 0, uintptr(unsafe.Pointer(&hpc)))
	// The pseudoconsole has its own copies of its ends of the pipes.
	_ = windows.CloseHandle(inR)
	_ = windows.CloseHandle(outW)
	t := &conTerm{api: api, in: os.NewFile(uintptr(inW), "conpty-in"), out: os.NewFile(uintptr(outR), "conpty-out")}
	if uint32(r) != 0 {
		_ = t.in.Close()
		_ = t.out.Close()
		return nil, fmt.Errorf("creating the pseudoconsole: HRESULT 0x%08x", uint32(r))
	}
	t.hpc = hpc
	if err := t.spawn(prog, argv, dir, env); err != nil {
		t.CloseTerminal()
		_ = t.out.Close()
		return nil, err
	}
	return t, nil
}

// spawn starts the program attached to the pseudoconsole.
func (t *conTerm) spawn(prog string, argv []string, dir string, env []string) error {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return err
	}
	defer attrs.Delete()
	// The attribute's value is the pseudoconsole handle itself (not a pointer to it).
	hpc := t.hpc
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&hpc)), unsafe.Sizeof(hpc)); err != nil {
		return err
	}
	si := new(windows.StartupInfoEx)
	si.Cb = uint32(unsafe.Sizeof(*si))
	// No standard handles of hopsesh's: the program's are the pseudoconsole's.
	si.Flags = windows.STARTF_USESTDHANDLES
	si.ProcThreadAttributeList = attrs.List()
	progp, err := windows.UTF16PtrFromString(prog)
	if err != nil {
		return err
	}
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(argv))
	if err != nil {
		return err
	}
	dirp, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	block, err := envBlock(env)
	if err != nil {
		return err
	}
	pi := new(windows.ProcessInformation)
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(progp, line, nil, nil, false, flags, &block[0], dirp, &si.StartupInfo, pi); err != nil {
		return fmt.Errorf("starting %s: %w", prog, err)
	}
	_ = windows.CloseHandle(pi.Thread)
	t.mu.Lock()
	t.process, t.pid = pi.Process, pi.ProcessId
	t.mu.Unlock()
	return nil
}

// lookPath finds the program as CreateProcess should run it: a path as given (relative
// ones from dir), else the first match on the program's own PATH, trying PATHEXT's
// extensions. Batch files are refused: cmd.exe would parse their arguments.
func lookPath(name, dir string, env []string) (string, error) {
	get := func(k string) string {
		for i := len(env) - 1; i >= 0; i-- {
			if n, v, ok := strings.Cut(env[i], "="); ok && strings.EqualFold(n, k) {
				return v
			}
		}
		return ""
	}
	exts := strings.Split(strings.ToLower(get("PATHEXT")), ";")
	if len(exts) == 0 || exts[0] == "" {
		exts = []string{".com", ".exe", ".bat", ".cmd"}
	}
	try := func(p string) (string, bool) {
		cands := []string{}
		if ext := strings.ToLower(filepath.Ext(p)); ext != "" {
			cands = append(cands, p)
		}
		for _, e := range exts {
			if e != "" {
				cands = append(cands, p+e)
			}
		}
		for _, c := range cands {
			if isFile(c) {
				return c, true
			}
		}
		return "", false
	}
	var found string
	if strings.ContainsAny(name, `\/:`) {
		p := name
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		f, ok := try(p)
		if !ok {
			return "", fmt.Errorf("%s: not found", name)
		}
		found = f
	} else {
		for _, d := range filepath.SplitList(get("PATH")) {
			if d == "" {
				continue
			}
			if f, ok := try(filepath.Join(d, name)); ok {
				found = f
				break
			}
		}
		if found == "" {
			return "", fmt.Errorf("%s: not found on PATH", name)
		}
	}
	switch strings.ToLower(filepath.Ext(found)) {
	case ".bat", ".cmd":
		return "", fmt.Errorf("%s is a batch file, which a terminal tab does not run (cmd.exe would read its arguments); run the program it starts instead", found)
	}
	return found, nil
}

// envBlock is env as CreateProcess takes it: UTF-16 KEY=VALUE strings, each ended by a
// NUL, then one more NUL. SYSTEMROOT is kept, as Windows needs it.
func envBlock(env []string) ([]uint16, error) {
	hasRoot := false
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); strings.EqualFold(k, "SYSTEMROOT") {
			hasRoot = true
		}
	}
	if !hasRoot {
		if v := os.Getenv("SYSTEMROOT"); v != "" {
			env = append(env, "SYSTEMROOT="+v)
		}
	}
	var b []uint16
	for _, kv := range env {
		if strings.ContainsRune(kv, 0) {
			return nil, errors.New("an environment variable holds a NUL byte")
		}
		b = append(b, utf16.Encode([]rune(kv))...)
		b = append(b, 0)
	}
	if len(b) == 0 {
		b = append(b, 0)
	}
	return append(b, 0), nil
}

// Read is the output; once it ends (the pseudoconsole closed and its host went), the
// pipe is closed.
func (t *conTerm) Read(p []byte) (int, error) {
	n, err := t.out.Read(p)
	if err != nil {
		_ = t.out.Close()
	}
	return n, err
}

func (t *conTerm) Write(p []byte) (int, error) { return t.in.Write(p) }
func (t *conTerm) ClosesOnExit() bool          { return false }
func (t *conTerm) Name() string                { return t.api.name }

func (t *conTerm) Resize(cols, rows int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.hpc == 0 {
		return nil
	}
	if r, _, _ := syscall.SyscallN(t.api.resize, uintptr(t.hpc), coord(cols, rows)); uint32(r) != 0 {
		return fmt.Errorf("HRESULT 0x%08x", uint32(r))
	}
	return nil
}

func (t *conTerm) Wait() int {
	t.mu.Lock()
	h := t.process
	t.mu.Unlock()
	if h == 0 {
		return -1
	}
	code := -1
	if ev, err := windows.WaitForSingleObject(h, windows.INFINITE); err == nil && ev == windows.WAIT_OBJECT_0 {
		var c uint32
		if windows.GetExitCodeProcess(h, &c) == nil {
			code = int(c)
		}
	}
	t.mu.Lock()
	_ = windows.CloseHandle(t.process)
	t.process = 0
	t.mu.Unlock()
	return code
}

// Hangup closes the pseudoconsole: its programs get CTRL_CLOSE_EVENT, as when a console
// window is closed.
func (t *conTerm) Hangup() { go t.CloseTerminal() }

func (t *conTerm) Kill() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.process != 0 {
		_ = windows.TerminateProcess(t.process, 1)
	}
}

// CloseTerminal closes the pseudoconsole, which flushes what is left of the output and
// then ends it (Read sees the end and closes the output pipe), and the input pipe. The
// output must be being read meanwhile: a system pseudoconsole before Windows 11 24H2
// waits for that.
func (t *conTerm) CloseTerminal() {
	t.mu.Lock()
	h := t.hpc
	t.hpc = 0
	t.mu.Unlock()
	if h == 0 {
		return
	}
	_, _, _ = syscall.SyscallN(t.api.close, uintptr(h))
	_ = t.in.Close()
}
