# Make "studio" for the Windows screenshots: Ubuntu in WSL2 with an SSH server on port
# 2222 and demoseed's studio sessions for a Linux user alice, reachable from this machine
# as `ssh studio` (an entry in ~/.ssh/config, also written to -SshConfig for the demo
# home). Everything is made up; used by CI on a windows-2025 runner.
#
#   ./scripts/windows-demo-studio.ps1 -Key $env:RUNNER_TEMP\demo-ssh\id_ed25519 -SshConfig $env:RUNNER_TEMP\demo-ssh\config
param(
  [Parameter(Mandatory)][string]$Key,
  [Parameter(Mandatory)][string]$SshConfig
)
$ErrorActionPreference = 'Stop'
$distro = 'Ubuntu-24.04'
function Fail($msg) { Write-Error $msg; exit 1 }
# InWsl runs a bash script in the distro, from a file with Unix line endings.
function InWsl([string]$script, [string]$user = 'root') {
  $file = Join-Path $env:TEMP ("wsl-" + [guid]::NewGuid() + ".sh")
  [IO.File]::WriteAllText($file, $script.Replace("`r", ""))
  $path = (wsl -d $distro -- wslpath -a ($file -replace '\\', '/')).Trim()
  wsl -d $distro -u $user -- bash -e $path
  $code = $LASTEXITCODE
  Remove-Item $file
  if ($code -ne 0) { Fail "in WSL as ${user}: exit $code" }
}

Write-Host "Installing $distro in WSL2"
wsl --install -d $distro --no-launch --web-download
if ($LASTEXITCODE -ne 0) { Fail "wsl --install exited with $LASTEXITCODE" }
# Keep the distro running (sshd and the sleeping "live" agents) after each command.
Start-Process -WindowStyle Hidden -FilePath wsl.exe -ArgumentList '-d', $distro, '-u', 'root', '--', 'sleep', 'infinity'

# The laptop's key, readable only by this user (OpenSSH refuses keys others can read).
New-Item -ItemType Directory -Force -Path (Split-Path $Key) | Out-Null
if (Test-Path $Key) { Remove-Item $Key, "$Key.pub" -Force }
ssh-keygen -q -t ed25519 -N '' -C 'alice@laptop' -f $Key
icacls $Key /inheritance:r /grant:r "$($env:USERNAME):F" | Out-Null

# demoseed for Linux, and a stand-in claude that answers --version.
$seed = Join-Path (Split-Path $Key) 'demoseed'
$env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
go build -o $seed ./internal/devtools/demoseed
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
$seedWsl = (wsl -d $distro -- wslpath -a ($seed -replace '\\', '/')).Trim()
$pubWsl = (wsl -d $distro -- wslpath -a ("$Key.pub" -replace '\\', '/')).Trim()

InWsl @"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq openssh-server git >/dev/null
id alice >/dev/null 2>&1 || useradd -m -s /bin/bash alice
install -m 755 '$seedWsl' /usr/local/bin/demoseed
printf '#!/bin/sh\necho "2.1.284 (Claude Code)"\n' > /usr/local/bin/claude
chmod 755 /usr/local/bin/claude
install -d -o alice -m 755 /srv/git
install -d -o alice -m 700 /home/alice/.ssh
install -o alice -m 600 '$pubWsl' /home/alice/.ssh/authorized_keys
sed -i 's/^#\?Port .*/Port 2222/' /etc/ssh/sshd_config
ssh-keygen -A
mkdir -p /run/sshd
# Ubuntu 24.04 under systemd starts sshd from ssh.socket, which takes its port from
# sshd_config only after a reload; without systemd, start sshd itself.
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload
  systemctl restart ssh.socket ssh.service || true
fi
for i in 1 2 3 4 5; do ss -ltn | grep -q ':2222 ' && break; sleep 1; done
ss -ltn | grep -q ':2222 ' || /usr/sbin/sshd -p 2222
for i in 1 2 3 4 5; do ss -ltn | grep -q ':2222 ' && break; sleep 1; done
ss -ltn | grep ':2222 '
"@
InWsl 'export HOME=/home/alice; cd ~ && demoseed -role studio' 'alice'

# ~/.ssh/config: here (the profile ssh reads) and for the demo home.
$cfg = @"
Host studio
  HostName localhost
  Port 2222
  User alice
  IdentityFile $($Key -replace '\\', '/')
  IdentitiesOnly yes
"@
Set-Content -Path $SshConfig -Value $cfg -Encoding ascii
$mine = Join-Path $HOME '.ssh'
New-Item -ItemType Directory -Force -Path $mine | Out-Null
Add-Content -Path (Join-Path $mine 'config') -Value $cfg -Encoding ascii

# WSL forwards localhost:2222 to the guest; give it a moment.
for ($i = 0; $i -lt 20; $i++) {
  if ((Test-NetConnection -ComputerName localhost -Port 2222 -WarningAction SilentlyContinue).TcpTestSucceeded) { break }
  Start-Sleep -Seconds 1
}
$out = ssh -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL studio 'echo studio-ok; ls ~/.claude/projects | wc -l'
Write-Host $out
if ("$out" -notmatch 'studio-ok') { Fail 'ssh studio did not answer' }
Write-Host 'studio is up: WSL2 Ubuntu, sshd on 2222, alice with the demo sessions'
