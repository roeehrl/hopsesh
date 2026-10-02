package repos

import "strings"

// PowerShellProbe returns a PowerShell script producing the same line format as the POSIX
// probe, for Windows machines.
func PowerShellProbe(dirs []string) string {
	var b strings.Builder
	b.WriteString("$dirs = @(")
	for i, d := range dirs {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("'" + strings.ReplaceAll(d, "'", "''") + "'")
	}
	b.WriteString(")\n")
	b.WriteString(`foreach ($d in $dirs) {
  "@@dir` + "`t" + `$d"
  if (-not (Test-Path -LiteralPath $d -PathType Container)) { "exists` + "`t" + `0"; continue }
  "exists` + "`t" + `1"
  $top = git -C $d rev-parse --show-toplevel 2>$null
  if ($LASTEXITCODE -ne 0) { "repo` + "`t" + `0"; continue }
  "repo` + "`t" + `1"; "top` + "`t" + `$top"
  "branch` + "`t" + `$(git -C $d branch --show-current 2>$null)"
  "head` + "`t" + `$(git -C $d rev-parse --short HEAD 2>$null)"
  $r = git -C $d remote 2>$null | Select-Object -First 1
  if ($r) { "remote` + "`t" + `$(git -C $d config --get remote.$r.url 2>$null)" }
  $up = git -C $d rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>$null
  if ($LASTEXITCODE -eq 0 -and $up) { "upstream` + "`t" + `$up"; "aheadbehind` + "`t" + `$(git -C $d rev-list --left-right --count '@{u}...HEAD' 2>$null)" }
  else { "unpushed` + "`t" + `$(git -C $d rev-list --count HEAD --not --remotes 2>$null)" }
  $dirty = @(git -C $d status --porcelain -- ':/' ':(top,exclude).claude/worktrees' 2>$null).Count
  "dirty` + "`t" + `$dirty"
  "gitdir` + "`t" + `$(git -C $d rev-parse --absolute-git-dir 2>$null)"
  $cd = git -C $d rev-parse --path-format=absolute --git-common-dir 2>$null
  "commondir` + "`t" + `$cd"
  "root` + "`t" + `$(git -C $d rev-list --max-parents=0 HEAD 2>$null | Select-Object -Last 1)"
  git -C $d worktree list --porcelain 2>$null | ForEach-Object { "wt` + "`t" + `$_" }
}
`)
	return b.String()
}
