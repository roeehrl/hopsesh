package installs

import (
	"bufio"
	"bytes"
	"strings"
)

// Where hopsesh can be, besides the PATH of the shell that runs the script and where the
// user's own shell finds it (the script's first argument).
var (
	posixPlaces = []string{
		`"$HOME/.hopsesh/bin/hopsesh"`, `"$HOME/.local/bin/hopsesh"`, `"$HOME/bin/hopsesh"`, `"$HOME/go/bin/hopsesh"`,
		`/opt/homebrew/bin/hopsesh`, `/usr/local/bin/hopsesh`, `/home/linuxbrew/.linuxbrew/bin/hopsesh`, `/usr/bin/hopsesh`,
		`/Applications/hopsesh.app/Contents/Resources/bin/hopsesh`, `"$HOME/Applications/hopsesh.app/Contents/Resources/bin/hopsesh"`,
	}
	windowsPlaces = []string{
		`(Join-Path $h '.hopsesh\bin\hopsesh.exe')`, `(Join-Path $env:LOCALAPPDATA 'Programs\hopsesh\hopsesh.exe')`,
		`(Join-Path $h 'scoop\apps\hopsesh\current\hopsesh.exe')`, `(Join-Path $h '.local\bin\hopsesh.exe')`,
	}
)

// Script is what lists hopsesh on a machine running goos: sh on macOS and Linux (pass where
// the user's shell finds hopsesh as its first argument, or ""), PowerShell on Windows. It
// prints tab-separated lines that Parse reads:
//
//	at <path> <program>       a path that runs <program> (links followed)
//	ver <program> <line>      what <program> version says
//	pkg <program> <manager>   a Linux package owns <program> (apt, dnf, apk)
//	appdir <program> <dir>    <program> is in the Windows app's folder <dir>
//	term <program>            the user's shell runs <program>
//	svc <program> <0|1>       the login service runs <program>; 1: it is there
//	open <bundle>             the app at <bundle> is running
func Script(goos string) string {
	if goos == "windows" {
		return windowsScript
	}
	return `real() { r="$(realpath "$1" 2>/dev/null || readlink -f "$1" 2>/dev/null)"; [ -n "$r" ] && printf '%s' "$r" || printf '%s' "$1"; }
seen=""
look() {
  p="$1"; [ -n "$p" ] && [ -f "$p" ] && [ -x "$p" ] || return 0
  r="$(real "$p")"
  printf 'at\t%s\t%s\n' "$p" "$r"
  case "$seen" in *"
$r
"*) return 0;; esac
  seen="$seen
$r
"
  printf 'ver\t%s\t%s\n' "$r" "$("$r" version 2>/dev/null | head -n 1)"
  case "$r" in /usr/local/*|"$HOME"/*) ;; /usr/*|/bin/*|/sbin/*)
    if command -v dpkg >/dev/null 2>&1 && dpkg -S "$r" >/dev/null 2>&1; then printf 'pkg\t%s\tapt\n' "$r"
    elif command -v rpm >/dev/null 2>&1 && rpm -qf "$r" >/dev/null 2>&1; then printf 'pkg\t%s\tdnf\n' "$r"
    elif command -v apk >/dev/null 2>&1 && apk info --who-owns "$r" >/dev/null 2>&1; then printf 'pkg\t%s\tapk\n' "$r"
    fi;;
  esac
}
for p in ` + strings.Join(posixPlaces, " ") + ` "$(command -v hopsesh 2>/dev/null)" "${1:-}"; do look "$p"; done
[ -n "${1:-}" ] && [ -x "$1" ] && printf 'term\t%s\n' "$(real "$1")"
s=""
if [ -f "$HOME/Library/LaunchAgents/` + serviceLabel + `.plist" ]; then
  f="$HOME/Library/LaunchAgents/` + serviceLabel + `.plist"
  s="$(plutil -extract ProgramArguments.0 raw "$f" 2>/dev/null || sed -n '/<array>/,/<\/array>/s/.*<string>\(.*\)<\/string>.*/\1/p' "$f" | head -n 1)"
elif [ -f "$HOME/.config/systemd/user/hopsesh.service" ]; then
  l="$(sed -n 's/^ExecStart=//p' "$HOME/.config/systemd/user/hopsesh.service" | head -n 1)"
  case "$l" in \"*) s="${l#\"}"; s="${s%%\"*}";; *) s="${l%% *}";; esac
fi
if [ -n "$s" ]; then
  if [ -x "$s" ]; then printf 'svc\t%s\t1\n' "$(real "$s")"; else printf 'svc\t%s\t0\n' "$s"; fi
fi
ps -axo command= 2>/dev/null | sed -n 's#^\(.*\.app\)/Contents/MacOS/hopsesh-app.*#open\t\1#p' | sort -u
exit 0
`
}

// serviceLabel is loginsvc.Label (not imported: loginsvc's code is per system).
const serviceLabel = "io.github.roeehrl.hopsesh.serve"

var windowsScript = `$h = $env:USERPROFILE
$seen = @{}
function Look($p) {
  if (-not $p -or -not (Test-Path -LiteralPath $p -PathType Leaf)) { return }
  $r = (Get-Item -LiteralPath $p).FullName
  $t = (Get-Item -LiteralPath $p).Target
  if ($t) { $r = [string]($t | Select-Object -First 1) }
  "at` + "`t" + `$p` + "`t" + `$r"
  if ($seen.ContainsKey($r.ToLower())) { return }
  $seen[$r.ToLower()] = 1
  $v = (& $r version 2>$null | Select-Object -First 1)
  "ver` + "`t" + `$r` + "`t" + `$v"
  $d = Split-Path -Parent $r
  if (Test-Path -LiteralPath (Join-Path $d 'hopsesh-app.exe')) { "appdir` + "`t" + `$r` + "`t" + `$d" }
}
$cands = @(` + strings.Join(windowsPlaces, ", ") + `)
$cands += (Get-Command hopsesh -ErrorAction SilentlyContinue).Source
$wg = Join-Path $env:LOCALAPPDATA 'Microsoft\WinGet\Packages'
if (Test-Path $wg) { $cands += (Get-ChildItem $wg -Filter hopsesh.exe -Recurse -Depth 3 -ErrorAction SilentlyContinue | ForEach-Object { $_.FullName }) }
foreach ($p in $cands) { Look $p }
$c = (Get-Command hopsesh -ErrorAction SilentlyContinue).Source
if ($c) { "term` + "`t" + `$c" }
$q = schtasks /Query /TN 'hopsesh\serve' /V /FO LIST 2>$null | Select-String '^Task To Run:' | Select-Object -First 1
if ($q) {
  $v = ($q.Line -replace '^Task To Run:\s*', '').Trim()
  if ($v.StartsWith('"')) { $s = $v.Substring(1).Split('"')[0] } else { $s = $v.Split(' ')[0] }
  "svc` + "`t" + `$s` + "`t" + `$([int](Test-Path -LiteralPath $s))"
}
Get-Process hopsesh-app -ErrorAction SilentlyContinue | ForEach-Object { if ($_.Path) { "open` + "`t" + `$(Split-Path -Parent $_.Path)" } }
`

// Parse reads what Script printed on a machine running goos (arch and home as the
// machine reports them) into its list of installs, with the machine's own one chosen.
func Parse(out []byte, goos, arch, home string) List {
	l := List{OS: goos, Arch: arch, Home: strings.TrimRight(home, `/\`), Installs: []Install{}}
	type found struct {
		paths            []string
		version, manager string
		appDir           string
	}
	byProg := map[string]*found{}
	var order []string
	get := func(prog string) *found {
		key := prog
		if goos == "windows" {
			key = strings.ToLower(prog)
		}
		f, ok := byProg[key]
		if !ok {
			f = &found{}
			byProg[key] = f
			order = append(order, prog)
		}
		return f
	}
	var opens []string
	svcThere := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		f := strings.Split(strings.TrimRight(sc.Text(), "\r"), "\t")
		switch {
		case len(f) == 3 && f[0] == "at":
			p := get(f[2])
			if !contains(p.paths, f[1]) {
				p.paths = append(p.paths, f[1])
			}
		case len(f) == 3 && f[0] == "ver":
			get(f[1]).version = versionOf(f[2])
		case len(f) == 3 && f[0] == "pkg":
			get(f[1]).manager = f[2]
		case len(f) == 3 && f[0] == "appdir":
			get(f[1]).appDir = f[2]
		case len(f) == 2 && f[0] == "term":
			l.Terminal = f[1]
		case len(f) == 3 && f[0] == "svc":
			l.Service, svcThere = f[1], f[2] == "1"
		case len(f) == 2 && f[0] == "open":
			opens = append(opens, f[1])
		}
	}
	l.ServiceGone = l.Service != "" && !svcThere
	for _, prog := range order {
		f := get(prog)
		in := Install{Path: prog, Version: f.version}
		for _, p := range f.paths {
			if !same(p, prog) {
				in.Links = append(in.Links, p)
			}
		}
		in.Kind, in.Bundle, in.Manager = classify(prog, goos, l.Home, f.manager, f.appDir)
		if in.Kind == Package {
			in.UpdateWith = UpdateCommand(in.Manager, "", arch)
		}
		for _, o := range opens {
			if in.Bundle != "" && same(o, in.Bundle) {
				in.Open = true
			}
		}
		in.Terminal = in.Runs(l.Terminal)
		in.Service = in.Runs(l.Service)
		l.Installs = append(l.Installs, in)
	}
	pickMain(&l)
	return l
}

// versionOf takes the version from "hopsesh 0.6.4 (commit …)".
func versionOf(line string) string {
	f := strings.Fields(line)
	if len(f) >= 2 && f[0] == "hopsesh" {
		return f[1]
	}
	return ""
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
