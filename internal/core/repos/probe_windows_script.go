package repos

import (
	"strconv"
	"strings"
	"time"
)

// PowerShellProbe returns a PowerShell script producing the same line format as the POSIX
// probe, for Windows machines.
//
// git runs through hp-git, which gives each git call limit (RemoteProbeTimeout) to answer:
// one still running then is killed, and the folder prints "timeout" instead of its partial
// output (each folder's lines are collected and written only once it is done).
// hp-git reads git's output in the console's encoding, as PowerShell does for a native
// command, so paths come out as they did before. Its arguments are quoted where they start
// with a dash: PowerShell would take a bare "--" for itself.
func PowerShellProbe(dirs, excl []string, limit time.Duration) string {
	var b strings.Builder
	b.WriteString("$dirs = @(")
	for i, d := range dirs {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("'" + strings.ReplaceAll(d, "'", "''") + "'")
	}
	b.WriteString(")\n")
	b.WriteString(`$hpLimit = @LIMIT@
$hpGit = Get-Command git -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty Source
if (-not $hpGit) { $hpGit = 'git' }
function hp-quote([string]$s) {
  if ($s -ne '' -and $s -notmatch '[\s"]') { return $s }
  '"' + (($s -replace '(\\*)"', '$1$1\"') -replace '(\\+)$', '$1$1') + '"'
}
function hp-git {
  $ms = $hpLimit * 1000
  $si = New-Object System.Diagnostics.ProcessStartInfo
  $si.FileName = $hpGit
  $si.Arguments = (@('-C', $d) + $args | ForEach-Object { hp-quote ([string]$_) }) -join ' '
  $si.UseShellExecute = $false
  $si.CreateNoWindow = $true
  $si.RedirectStandardInput = $true
  $si.RedirectStandardOutput = $true
  $si.RedirectStandardError = $true
  $si.StandardOutputEncoding = [Console]::OutputEncoding
  $p = [Diagnostics.Process]::Start($si)
  $p.StandardInput.Close()
  $o = $p.StandardOutput.ReadToEndAsync()
  $null = $p.StandardError.ReadToEndAsync()
  if (-not $p.WaitForExit($ms)) { try { $p.Kill() } catch {}; throw 'hopsesh-timeout' }
  if (-not $o.Wait($ms)) { throw 'hopsesh-timeout' }
  $global:LASTEXITCODE = $p.ExitCode
  $t = $o.Result.TrimEnd([char[]]"` + "`r`n" + `")
  if ($t -ne '') { $t -split "` + "`r?`n" + `" }
}
foreach ($d in $dirs) {
  "@@dir` + "`t" + `$d"
  try {
    $lines = & {
      if (-not (Test-Path -LiteralPath $d -PathType Container)) { "exists` + "`t" + `0"; return }
      "exists` + "`t" + `1"
      $top = hp-git rev-parse '--show-toplevel'
      if ($LASTEXITCODE -ne 0) { "repo` + "`t" + `0"; return }
      "repo` + "`t" + `1"; "top` + "`t" + `$top"
      "branch` + "`t" + `$(hp-git branch '--show-current')"
      "head` + "`t" + `$(hp-git rev-parse '--short' HEAD)"
      $r = hp-git remote | Select-Object -First 1
      if ($r) { "remote` + "`t" + `$(hp-git config '--get' "remote.$r.url")" }
      $up = hp-git rev-parse '--abbrev-ref' '--symbolic-full-name' '@{u}'
      if ($LASTEXITCODE -eq 0 -and $up) { "upstream` + "`t" + `$up"; "aheadbehind` + "`t" + `$(hp-git rev-list '--left-right' '--count' '@{u}...HEAD')" }
      else { "unpushed` + "`t" + `$(hp-git rev-list '--count' HEAD '--not' '--remotes')" }
      $dirty = @(hp-git status '--porcelain' '--' ':/'@EXCLUDES@).Count
      "dirty` + "`t" + `$dirty"
      "gitdir` + "`t" + `$(hp-git rev-parse '--absolute-git-dir')"
      $cd = hp-git rev-parse '--path-format=absolute' '--git-common-dir'
      "commondir` + "`t" + `$cd"
      "root` + "`t" + `$(hp-git rev-list '--max-parents=0' HEAD | Select-Object -Last 1)"
      hp-git worktree list '--porcelain' | ForEach-Object { "wt` + "`t" + `$_" }
    }
    $lines
  } catch {
    if ("$_" -eq 'hopsesh-timeout') { "timeout` + "`t" + `$hpLimit" }
    else { "error` + "`t" + `$(("$_" -split "` + "`r?`n" + `")[0])" }
  }
}
`)
	var ex strings.Builder
	for _, p := range excludes(excl) {
		ex.WriteString(" '" + strings.ReplaceAll(p, "'", "''") + "'")
	}
	s := strings.Replace(b.String(), "@EXCLUDES@", ex.String(), 1)
	return strings.Replace(s, "@LIMIT@", strconv.Itoa(seconds(limit)), 1)
}
