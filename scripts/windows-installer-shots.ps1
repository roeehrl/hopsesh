# Screenshots of the hopsesh installer's pages, run interactively: each page is captured,
# then Enter takes the default button (Next, I Agree, Install, Finish) to the next one.
# For checking how the installer looks; CI uploads them as windows-installer-shots.
#
#   ./scripts/windows-installer-shots.ps1 -Setup dist/windows/hopsesh-<ver>-windows-amd64-setup.exe -Out shots
param(
  [Parameter(Mandatory)][string]$Setup,
  [Parameter(Mandatory)][string]$Out
)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing, System.Windows.Forms
Add-Type @"
using System;
using System.Runtime.InteropServices;
public static class Win {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool SetProcessDPIAware();
}
"@
[Win]::SetProcessDPIAware() | Out-Null
New-Item -ItemType Directory -Force -Path $Out | Out-Null

function Shot([IntPtr]$h, [string]$name) {
  $r = New-Object Win+RECT
  [Win]::GetWindowRect($h, [ref]$r) | Out-Null
  $w = $r.Right - $r.Left; $hgt = $r.Bottom - $r.Top
  if ($w -le 0 -or $hgt -le 0) { return }
  $bmp = New-Object System.Drawing.Bitmap $w, $hgt
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($r.Left, $r.Top, 0, 0, $bmp.Size)
  $bmp.Save((Join-Path $Out "$name.png"), [System.Drawing.Imaging.ImageFormat]::Png)
  $g.Dispose(); $bmp.Dispose()
  Write-Host "captured $name ($w x $hgt)"
}

$p = Start-Process -FilePath (Resolve-Path $Setup) -PassThru
for ($i = 1; $i -le 10; $i++) {
  for ($t = 0; $t -lt 40 -and $p.MainWindowHandle -eq 0 -and -not $p.HasExited; $t++) { Start-Sleep -Milliseconds 250; $p.Refresh() }
  if ($p.HasExited) { break }
  $h = $p.MainWindowHandle
  [Win]::SetForegroundWindow($h) | Out-Null
  Start-Sleep -Milliseconds 1200 # let the page paint (and the progress page move)
  Shot $h ("page-{0:d2}" -f $i)
  [System.Windows.Forms.SendKeys]::SendWait('{ENTER}')
  Start-Sleep -Milliseconds 1500
  $p.Refresh()
}
if (-not $p.HasExited) { Stop-Process -Id $p.Id -Force }
# Whatever the wizard left running or installed goes away again.
Get-Process hopsesh-app -ErrorAction SilentlyContinue | Stop-Process -Force
$un = Join-Path $env:LOCALAPPDATA 'Programs\hopsesh\uninstall.exe'
if (Test-Path $un) { Start-Process -FilePath $un -ArgumentList '/S', "_?=$(Split-Path $un)" -Wait }
Write-Host "screenshots in $Out"
