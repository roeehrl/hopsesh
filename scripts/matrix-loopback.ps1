# The scenario matrix over real SSH on one Windows machine: "there" is this (administrator)
# account through Windows' own OpenSSH server, with hopsesh, the matrix helper and the
# stand-in agents on the system PATH; "here" is the same account with hopsesh's and the
# agents' folders kept apart by the matrix runner. Used by CI.
#
#   ./scripts/matrix-loopback.ps1 -Bin bin -Label windows→windows -Out out [-Strength 3] [-Shard 1/2] [-- hsmatrix flags]
#   ./scripts/matrix-loopback.ps1 -Bin bin -SetupOnly   (this machine as "there" for another)
param(
  [Parameter(Mandatory)][string]$Bin,
  [string]$Label,
  [string]$Out,
  [int]$Strength = 2,
  [string]$Shard = '1/1',
  [switch]$SetupOnly,
  [Parameter(ValueFromRemainingArguments)][string[]]$Rest
)
$ErrorActionPreference = 'Stop'
$Bin = (Resolve-Path $Bin).Path

# The programs, where every ssh session finds them.
$tools = 'C:\hsm-bin'
New-Item -ItemType Directory -Force -Path $tools | Out-Null
Copy-Item (Join-Path $Bin 'hopsesh.exe') $tools -Force
Copy-Item (Join-Path $Bin 'hsmatrix.exe') $tools -Force
foreach ($a in 'claude', 'codex') { Copy-Item (Join-Path $Bin 'fakeagent.exe') (Join-Path $tools "$a.exe") -Force }
$machine = [Environment]::GetEnvironmentVariable('Path', 'Machine')
if (($machine -split ';') -notcontains $tools) { [Environment]::SetEnvironmentVariable('Path', "$tools;$machine", 'Machine') }
$env:Path = "$tools;$env:Path"
# A setup-only GitHub Actions step cannot change the runner process's inherited
# PATH. Publish the same tools for later steps, including the local cloud rows
# driven from Git Bash; the machine PATH above serves incoming SSH sessions.
if ($env:GITHUB_PATH) { Add-Content -LiteralPath $env:GITHUB_PATH -Value $tools -Encoding utf8 }

# OpenSSH server, key login for this account, cmd.exe as the remote shell.
if (-not (Get-Service sshd -ErrorAction SilentlyContinue)) {
  Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0 | Out-Null
}
$shell = (Get-ItemProperty -Path 'HKLM:\SOFTWARE\OpenSSH' -Name DefaultShell -ErrorAction SilentlyContinue).DefaultShell
if ($shell) { Remove-ItemProperty -Path 'HKLM:\SOFTWARE\OpenSSH' -Name DefaultShell }
Restart-Service sshd -ErrorAction SilentlyContinue
Start-Service sshd
$sshDir = Join-Path $HOME '.ssh'
New-Item -ItemType Directory -Force -Path $sshDir | Out-Null
$key = Join-Path $sshDir 'id_ed25519'
if (-not (Test-Path $key)) { ssh-keygen -q -t ed25519 -N '' -f $key | Out-Null }
$auth = 'C:\ProgramData\ssh\administrators_authorized_keys'
Get-Content "$key.pub" | Add-Content -Path $auth
icacls $auth /inheritance:r /grant 'Administrators:F' /grant 'SYSTEM:F' | Out-Null
Add-Content -Path (Join-Path $sshDir 'config') -Value "Host hsm-box`n  HostName localhost`n  User $env:USERNAME" -Encoding ascii

if ($SetupOnly) { exit 0 }
New-Item -ItemType Directory -Force -Path $Out | Out-Null
& (Join-Path $Bin 'hsmatrix.exe') run -hopsesh (Join-Path $Bin 'hopsesh.exe') -there "$env:USERNAME@localhost" -alias hsm-box -label $Label -out $Out -t $Strength -shard $Shard @Rest
exit $LASTEXITCODE
