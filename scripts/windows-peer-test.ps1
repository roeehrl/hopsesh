# End-to-end test of hopsesh peers on Windows, over the machine's own OpenSSH server: this
# user plays both machines. "box" is reached over SSH and runs hopsesh peer --stdio from
# where install.ps1 puts it, with its own configuration; "here" uses a scratch one. A Claude
# Code session (with Japanese and Chinese in it) is listed on box, pushed to it, checked,
# undone on both sides, and refused once box stops receiving. Run by CI; needs admin.
param([string]$Bin = "$PWD\bin\hopsesh.exe")
$ErrorActionPreference = 'Continue' # native commands report through exit codes, checked below
$Bin = (Resolve-Path $Bin).Path
function Fail($msg) { Write-Host "FAIL: $msg"; exit 1 }

# OpenSSH server, key login for this (administrator) user.
if (-not (Get-Service sshd -ErrorAction SilentlyContinue)) {
  Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0 | Out-Null
}
# ssh commands run in Windows' own default shell (cmd.exe), whatever the image configures.
$shell = (Get-ItemProperty -Path 'HKLM:\SOFTWARE\OpenSSH' -Name DefaultShell -ErrorAction SilentlyContinue).DefaultShell
if ($shell) { Write-Host "OpenSSH DefaultShell was $shell; using cmd.exe"; Remove-ItemProperty -Path 'HKLM:\SOFTWARE\OpenSSH' -Name DefaultShell }
Start-Service sshd
$sshDir = Join-Path $HOME '.ssh'
New-Item -ItemType Directory -Force -Path $sshDir | Out-Null
$key = Join-Path $sshDir 'id_ed25519'
if (-not (Test-Path $key)) { ssh-keygen -q -t ed25519 -N '' -f $key | Out-Null }
$auth = 'C:\ProgramData\ssh\administrators_authorized_keys'
Get-Content "$key.pub" | Add-Content -Path $auth
icacls $auth /inheritance:r /grant 'Administrators:F' /grant 'SYSTEM:F' | Out-Null

# box: hopsesh where install.ps1 puts it, receiving, with Claude Code run there once.
$boxBin = Join-Path $env:LOCALAPPDATA 'Programs\hopsesh\hopsesh.exe'
New-Item -ItemType Directory -Force -Path (Split-Path $boxBin) | Out-Null
Copy-Item $Bin $boxBin -Force
& $boxBin receive on | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $HOME '.claude\projects') | Out-Null
$boxProj = Join-Path $HOME 'boxproj'
New-Item -ItemType Directory -Force -Path $boxProj | Out-Null

# here: its own configuration, Claude Code folder and one session.
$work = Join-Path ([IO.Path]::GetTempPath()) ("hopsesh-" + [Guid]::NewGuid())
$proj = Join-Path $work 'proj'
New-Item -ItemType Directory -Force -Path $proj | Out-Null
$env:HOPSESH_CONFIG_DIR = Join-Path $work 'config'
$env:HOPSESH_STATE_DIR = Join-Path $work 'state'
$env:CLAUDE_CONFIG_DIR = Join-Path $work 'claude'
$env:HOPSESH_MACHINE = 'here'
function Slug($p) { ($p.ToCharArray() | ForEach-Object { if ($_ -match '[A-Za-z0-9]') { $_ } else { '-' } }) -join '' }
$id = '3a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d'
$projJson = $proj.Replace('\', '\\')
$sessionDir = Join-Path $env:CLAUDE_CONFIG_DIR ("projects\" + (Slug $proj))
New-Item -ItemType Directory -Force -Path $sessionDir | Out-Null
# Each line in parentheses: in PowerShell a comma binds tighter than +.
$lines = @(
  ('{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"' + $id + '","cwd":"' + $projJson + '","version":"2.1.284","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"バグを直して-' + $projJson + '\\main.go 修复错误"}}'),
  ('{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"' + $id + '","cwd":"' + $projJson + '","timestamp":"2026-10-01T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"直しました。 已修复。"}]}}'),
  ('{"type":"custom-title","customTitle":"テスト Windows 测试","sessionId":"' + $id + '"}')
)
[IO.File]::WriteAllText((Join-Path $sessionDir "$id.jsonl"), ($lines -join "`n") + "`n", (New-Object Text.UTF8Encoding $false))
foreach ($l in $lines) { try { $null = $l | ConvertFrom-Json } catch { Fail "the test session has a line that is not JSON: $l" } }
if ($lines.Count -ne 3) { Fail "the test session has $($lines.Count) lines, not 3" }

& $Bin hosts add box "$env:USERNAME@127.0.0.1" | Out-Null
if ($LASTEXITCODE -ne 0) { Fail 'hosts add' }
& $Bin trust box --yes | Out-Null
if ($LASTEXITCODE -ne 0) { Fail 'trust' }
$ls = & $Bin ls --host box --no-local --json | Out-String
if ($LASTEXITCODE -ne 0) { Write-Host $ls; Fail 'ls of box over SSH' }

$out = & $Bin push $id box --to $boxProj --yes --json 2>&1 | Out-String
if ($LASTEXITCODE -ne 0) {
  Write-Host $out
  Write-Host '--- agents here:'; & $Bin agents
  Write-Host '--- sessions here:'; & $Bin ls --host local --json | Out-String | Write-Host
  Get-ChildItem -Recurse $env:CLAUDE_CONFIG_DIR | Select-Object -ExpandProperty FullName | Write-Host
  $bytes = [IO.File]::ReadAllBytes((Join-Path $sessionDir "$id.jsonl"))
  Write-Host "--- the session file: $($bytes.Length) bytes, starting" (($bytes[0..23] | ForEach-Object { $_.ToString('x2') }) -join ' ')
  Fail 'push'
}
$transfer = $out | ConvertFrom-Json
$journal = $transfer.result.journal
$receivedId = $transfer.plan.placement.key.session
if (-not $receivedId -or $receivedId -eq $id) { Fail 'unverified account transfer did not create a fresh session' }
if ($transfer.plan.kind -ne 'continue') { Fail 'unverified account transfer did not use portable history' }
$got = Join-Path $HOME (".claude\projects\" + (Slug $boxProj) + "\$receivedId.jsonl")
if (-not (Test-Path $got)) { Write-Host $out; Fail "nothing installed at $got" }
$text = [IO.File]::ReadAllText($got, [Text.Encoding]::UTF8)
foreach ($want in @('バグを直して', '修复错误', '直しました。 已修复。', $boxProj.Replace('\', '\\'))) {
  if (-not $text.Contains($want)) { Write-Host $text; Fail "the session on box lacks: $want" }
}
if ($text.Contains($projJson)) { Write-Host $text; Fail 'the old path is still in the session on box' }
$here = [IO.File]::ReadAllText((Join-Path $sessionDir "$id.jsonl"), [Text.Encoding]::UTF8)
if ($here.Contains([string][char]0x21AA)) { Fail 'the copy here was relabelled: titles must stay as they were' }

& $Bin undo $journal --yes | Out-Null
if (Test-Path $got) { Fail 'undo left the copy on box' }
$here = [IO.File]::ReadAllText((Join-Path $sessionDir "$id.jsonl"), [Text.Encoding]::UTF8)
if ($here.Contains([string][char]0x21AA)) { Fail 'the copy here carries a title label after undo' }

# box's own settings: without this machine's scratch configuration.
$saved = @{}
foreach ($v in 'HOPSESH_CONFIG_DIR', 'HOPSESH_STATE_DIR', 'CLAUDE_CONFIG_DIR', 'HOPSESH_MACHINE') { $saved[$v] = [Environment]::GetEnvironmentVariable($v); [Environment]::SetEnvironmentVariable($v, $null) }
& $boxBin receive off | Out-Null
foreach ($v in $saved.Keys) { [Environment]::SetEnvironmentVariable($v, $saved[$v]) }
$out = & $Bin push $id box --to $boxProj --yes --json 2>&1 | Out-String
if ($LASTEXITCODE -eq 0) { Fail 'push to a machine that does not receive' }
if (-not $out.Contains('does not receive sessions')) { Write-Host $out; Fail 'the refusal does not say why' }
Write-Host 'windows peer test passed'
exit 0 # the refused push above left a failing exit code
