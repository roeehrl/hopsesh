# The app's terminal window on Windows, end to end: a test build of the app (-tags e2e)
# with the bundled ConPTY beside it opens a terminal tab running termprobe and shows it in
# the terminal window (WebView2), whose test page reaches hopsesh only through the tab's
# stream; the real xterm.js emulator answers what reaches it. Once the program ends the app
# writes the tab's backend, exit code and output, and quits. Used by CI on a Windows
# runner.
#
#   ./scripts/windows-terminal-check.ps1 -App $env:RUNNER_TEMP/e2e/hopsesh-app.exe
param([Parameter(Mandatory)][string]$App)
$ErrorActionPreference = 'Stop'
$dir = Split-Path -Parent (Resolve-Path $App)
go build -o (Join-Path $dir 'termprobe.exe') ./internal/devtools/termprobe
if ($LASTEXITCODE -ne 0) { exit 1 }
go run ./internal/devtools/conptyfetch -arch amd64 -out (Join-Path $dir 'conpty')
if ($LASTEXITCODE -ne 0) { exit 1 }
foreach ($placement in @('separate', 'bottom', 'right')) {
$env:HOPSESH_E2E_TERMINAL_PLACEMENT = $placement
$env:HOPSESH_E2E_CDP_PORT = '9334'
$work = Join-Path $env:RUNNER_TEMP ('hsterm-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path "$work\config", "$work\state" | Out-Null
$out = Join-Path $work 'terminal.txt'
Remove-Item $out -ErrorAction SilentlyContinue
$env:HOPSESH_CONFIG_DIR = "$work\config"
$env:HOPSESH_STATE_DIR = "$work\state"
$env:HOPSESH_E2E_TERMINAL = ConvertTo-Json -Compress @((Join-Path $dir 'termprobe.exe'))
$env:HOPSESH_E2E_TERMINAL_OUT = $out
$stdout = Join-Path $work 'app.stdout.log'
$stderr = Join-Path $work 'app.stderr.log'
$p = Start-Process -FilePath (Resolve-Path $App) -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
$diagnostics = Join-Path $work 'webview.log'
$diagnosticErrors = Join-Path $work 'webview.stderr.log'
$watch = Start-Process -FilePath node -ArgumentList @('scripts/terminal-native-diagnostics.mjs', $env:HOPSESH_E2E_CDP_PORT) -PassThru -RedirectStandardOutput $diagnostics -RedirectStandardError $diagnosticErrors
for ($i = 0; $i -lt 120; $i++) {
  if ((Test-Path $out) -and (Get-Item $out).Length -gt 0) { break }
  if ($p.HasExited) { break }
  Start-Sleep -Seconds 1
}
$p.Refresh()
$ended = $p.HasExited
$code = if ($ended) { $p.ExitCode } else { 'still running at timeout' }
if (-not $ended) { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue }
Stop-Process -Id $watch.Id -Force -ErrorAction SilentlyContinue
foreach ($log in @($stdout, $stderr, $diagnostics, $diagnosticErrors)) {
  if ((Test-Path $log) -and (Get-Item $log).Length -gt 0) {
    Write-Host "App log: $log"
    Get-Content -Tail 80 $log | Write-Host
  }
}
if (-not ((Test-Path $out) -and (Get-Item $out).Length -gt 0)) { Write-Error "the app's terminal check wrote nothing (process: $code; logs: $work)"; exit 1 }
$text = Get-Content -Raw $out
Write-Host $text
if ($text -notmatch '(?m)^backend=conpty \(bundled\) code=0') { Write-Error 'the tab did not run on the bundled ConPTY, or its program ended badly'; exit 1 }
if ($text -notmatch [regex]::Escape('da1="\x1b[?')) { Write-Error "the tab's program got no answer to its query"; exit 1 }
Write-Host 'A tab ran on the bundled ConPTY in the real terminal window and got an answer through its stream.'

}
Remove-Item Env:HOPSESH_E2E_TERMINAL_PLACEMENT -ErrorAction SilentlyContinue
Remove-Item Env:HOPSESH_E2E_CDP_PORT -ErrorAction SilentlyContinue
