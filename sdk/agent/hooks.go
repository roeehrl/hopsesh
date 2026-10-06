package agent

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strconv"
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
	File           *HookFile
	Reason         string
	Evidence       string
	DisabledReason string // vendor setting prevents delivery; install/remove must preserve it
}

// HookFile edits only hooks owned by this integration in a shared vendor file.
// All functions are pure; the app owns bounded local reads and atomic writes.
type HookFile struct {
	Path   string
	Merge  func([]byte) ([]byte, error)
	Remove func([]byte) ([]byte, error)
	Has    func([]byte) bool
}

// HookVersionAtLeast accepts stable, three-component versions in the same major
// compatibility family. Unknown versions and prereleases fail closed.
func HookVersionAtLeast(version, minimum string) bool {
	parse := func(s string) ([3]int, bool) {
		var v [3]int
		parts := strings.Split(s, ".")
		if len(parts) != 3 {
			return v, false
		}
		for i, p := range parts {
			if p == "" || strings.Trim(p, "0123456789") != "" {
				return v, false
			}
			n, err := strconv.Atoi(p)
			if err != nil {
				return v, false
			}
			v[i] = n
		}
		return v, true
	}
	v, ok := parse(version)
	m, mok := parse(minimum)
	if !ok || !mok || v[0] != m[0] {
		return false
	}
	return v[1] > m[1] || v[1] == m[1] && v[2] >= m[2]
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
