# The scenario matrix between Windows and Linux on one Windows machine, both ways: Ubuntu
# in WSL2 is the Linux machine (user hsremote, sshd on its own address), this Windows
# account the Windows one (its OpenSSH server, see matrix-loopback.ps1). Windows → Linux
# runs here; Linux → Windows runs inside the guest. Used by CI on windows-2025.
#
#   ./scripts/matrix-wsl.ps1 -Bin bin -LinuxBin bin-linux -Out out
param(
  [Parameter(Mandatory)][string]$Bin,
  [Parameter(Mandatory)][string]$LinuxBin,
  [Parameter(Mandatory)][string]$Out
)
$ErrorActionPreference = 'Stop'
$distro = 'Ubuntu-24.04'
function Fail($msg) { Write-Error $msg; exit 1 }
function WslPath([string]$p) { (wsl -d $distro -- wslpath -a ($p -replace '\\', '/')).Trim() }
# InWsl runs a bash script in the distro, from a file with Unix line endings.
function InWsl([string]$script, [string]$user = 'root') {
  $file = Join-Path $env:TEMP ("wsl-" + [guid]::NewGuid() + ".sh")
  [IO.File]::WriteAllText($file, $script.Replace("`r", ""))
  wsl -d $distro -u $user -- bash -e (WslPath $file)
  $code = $LASTEXITCODE
  Remove-Item $file
  return $code
}

# This machine as "there" for Linux → Windows.
& (Join-Path $PSScriptRoot 'matrix-loopback.ps1') -Bin $Bin -SetupOnly
if ($LASTEXITCODE -ne 0) { Fail 'Windows setup failed' }
# The guest reaches this machine's sshd through the WSL network: allow it in.
New-NetFirewallRule -DisplayName 'hsmatrix-wsl-ssh' -Direction Inbound -InterfaceAlias 'vEthernet (WSL*)' -Action Allow -Protocol TCP -LocalPort 22 | Out-Null

Write-Host "Installing $distro in WSL2"
wsl --install -d $distro --no-launch --web-download
if ($LASTEXITCODE -ne 0) { Fail "wsl --install exited with $LASTEXITCODE" }
Start-Process -WindowStyle Hidden -FilePath wsl.exe -ArgumentList '-d', $distro, '-u', 'root', '--', 'sleep', 'infinity'

$LinuxBin = (Resolve-Path $LinuxBin).Path
$winPub = Get-Content (Join-Path $HOME '.ssh\id_ed25519.pub')
$code = InWsl @"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq openssh-server git >/dev/null
id hsremote >/dev/null 2>&1 || useradd -m -s /bin/bash hsremote
install -m 755 '$(WslPath "$LinuxBin\hopsesh")' /usr/local/bin/hopsesh
install -m 755 '$(WslPath "$LinuxBin\hsmatrix")' /usr/local/bin/hsmatrix
install -m 755 '$(WslPath "$LinuxBin\fakeagent")' /usr/local/bin/claude
install -m 755 '$(WslPath "$LinuxBin\fakeagent")' /usr/local/bin/codex
install -d -o hsremote -m 700 /home/hsremote/.ssh
echo '$winPub' > /home/hsremote/.ssh/authorized_keys
chown hsremote /home/hsremote/.ssh/authorized_keys; chmod 600 /home/hsremote/.ssh/authorized_keys
su hsremote -c "ssh-keygen -q -t ed25519 -N '' -f /home/hsremote/.ssh/id_ed25519"
ssh-keygen -A
mkdir -p /run/sshd
if [ -d /run/systemd/system ]; then systemctl restart ssh.socket ssh.service || true; fi
for i in 1 2 3 4 5; do ss -ltn | grep -q ':22 ' && break; sleep 1; done
ss -ltn | grep -q ':22 ' || /usr/sbin/sshd
"@
if ($code -ne 0) { Fail "WSL setup exited with $code" }

$ip = ((wsl -d $distro -- hostname -I) -split '\s+' | Where-Object { $_ -match '^\d+\.\d+\.\d+\.\d+$' } | Select-Object -First 1)
$gw = (wsl -d $distro -- sh -c "ip route show default | awk '{print `$3}'").Trim()
Write-Host "Linux guest $ip, Windows host as seen from it $gw"
Add-Content -Path (Join-Path $HOME '.ssh\config') -Value "Host hsm-wsl`n  HostName $ip`n  User hsremote" -Encoding ascii
# The guest user's key may log in here (this account is an administrator).
$guestPub = (wsl -d $distro -u hsremote -- cat /home/hsremote/.ssh/id_ed25519.pub).Trim()
Add-Content -Path 'C:\ProgramData\ssh\administrators_authorized_keys' -Value $guestPub

New-Item -ItemType Directory -Force -Path $Out | Out-Null
$failed = 0
Write-Host '== Windows → Linux'
& (Join-Path (Resolve-Path $Bin).Path 'hsmatrix.exe') run -hopsesh (Join-Path (Resolve-Path $Bin).Path 'hopsesh.exe') -there "hsremote@$ip" -alias hsm-wsl -label 'windows→linux' -out (Join-Path $Out 'w2l')
if ($LASTEXITCODE -ne 0) { $failed++ }

Write-Host '== Linux → Windows'
$l2w = Join-Path $Out 'l2w'
New-Item -ItemType Directory -Force -Path $l2w | Out-Null
$code = InWsl @"
printf 'Host hsm-win\n  HostName $gw\n  User $env:USERNAME\n' >> ~/.ssh/config
cd ~
code=0
hsmatrix run -hopsesh /usr/local/bin/hopsesh -there '$env:USERNAME@$gw' -alias hsm-win -label 'linux→windows' -out /tmp/l2w || code=`$?
cp -r /tmp/l2w/. '$(WslPath $l2w)/' 2>/dev/null || true
exit `$code
"@ 'hsremote'
if ($code -ne 0) { $failed++ }
$summary = Join-Path $l2w 'summary.md'
if ((Test-Path $summary) -and $env:GITHUB_STEP_SUMMARY) { Get-Content $summary | Add-Content $env:GITHUB_STEP_SUMMARY }
exit $failed
