# Install the hopsesh Windows app silently, check what the installer set up, start the
# installed app, then uninstall it and check that everything is gone again (settings
# stay). Used by CI on a Windows runner.
#
#   ./scripts/windows-app-test.ps1 -Setup dist/windows/hopsesh-<version>-windows-amd64-setup.exe -Version <version>
param(
  [Parameter(Mandatory)][string]$Setup,
  [Parameter(Mandatory)][string]$Version
)
$ErrorActionPreference = 'Stop'
function Fail($msg) { Write-Error $msg; exit 1 }
function UserPath { [Environment]::GetEnvironmentVariable('Path', 'User') }

$dir = Join-Path $env:LOCALAPPDATA 'Programs\hopsesh'
$lnk = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\hopsesh.lnk'
$key = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\hopsesh'

Write-Host "Installing $Setup"
$p = Start-Process -FilePath (Resolve-Path $Setup) -ArgumentList '/S' -Wait -PassThru
if ($p.ExitCode -ne 0) { Fail "the installer exited with $($p.ExitCode)" }

foreach ($f in 'hopsesh-app.exe', 'hopsesh.exe', 'LICENSE', 'uninstall.exe') {
  if (-not (Test-Path (Join-Path $dir $f))) { Fail "$f is not in $dir" }
}
if (-not (Test-Path $lnk)) { Fail 'no Start menu entry' }
$entry = Get-ItemProperty $key
if ($entry.DisplayVersion -ne $Version) { Fail "uninstall entry says $($entry.DisplayVersion), want $Version" }
if (((UserPath) -split ';') -notcontains $dir) { Fail "the user PATH lacks $dir" }
$out = & (Join-Path $dir 'hopsesh.exe') version
if ("$out" -notmatch [regex]::Escape($Version)) { Fail "hopsesh version says: $out" }
Write-Host "Installed: $out"

# The installed app starts and stays up (WebView2 present, resources and manifest fine).
$app = Start-Process -FilePath (Join-Path $dir 'hopsesh-app.exe') -PassThru
Start-Sleep -Seconds 8
if ($app.HasExited) { Fail "the installed app exited with $($app.ExitCode)" }
Write-Host "The installed app is running (pid $($app.Id))"

# Installing again over it works with the app open (the installer closes it first).
$p = Start-Process -FilePath (Resolve-Path $Setup) -ArgumentList '/S' -Wait -PassThru
if ($p.ExitCode -ne 0) { Fail "reinstalling exited with $($p.ExitCode)" }
Start-Sleep -Seconds 2
Get-Process hopsesh-app -ErrorAction SilentlyContinue | Stop-Process -Force

# _?= runs the uninstaller in place, so -Wait waits for it.
$p = Start-Process -FilePath (Join-Path $dir 'uninstall.exe') -ArgumentList '/S', "_?=$dir" -Wait -PassThru
if ($p.ExitCode -ne 0) { Fail "the uninstaller exited with $($p.ExitCode)" }
Remove-Item (Join-Path $dir 'uninstall.exe') -ErrorAction SilentlyContinue
foreach ($f in 'hopsesh-app.exe', 'hopsesh.exe') {
  if (Test-Path (Join-Path $dir $f)) { Fail "$f is still there after uninstalling" }
}
if (Test-Path $lnk) { Fail 'the Start menu entry is still there' }
if (Test-Path $key) { Fail 'the uninstall entry is still there' }
if (((UserPath) -split ';') -contains $dir) { Fail "the user PATH still has $dir" }
Write-Host 'Install, start, reinstall and uninstall all check out.'
