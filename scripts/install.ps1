# Install the hopsesh command-line tool on Windows.
#
#   irm https://raw.githubusercontent.com/roeehrl/hopsesh/main/scripts/install.ps1 | iex
#
# Environment: HOPSESH_VERSION (default: latest), HOPSESH_INSTALL_DIR
# (default: %LOCALAPPDATA%\Programs\hopsesh). The archive is checked against the
# release's checksums.txt, and checksums.txt against the release signing key below
# (PowerShell 7 or later; Windows PowerShell 5.1 checks the checksum only).
$ErrorActionPreference = 'Stop'
$Repo = 'roeehrl/hopsesh'
# Public half of the key that signs checksums.txt (docs/RELEASING.md), base64 DER.
$ReleasePubKey = 'MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEv/ATZNvxb7prYETkiikx+XrVirJJrDHoF6azgJ5jvnMcjc6nMWhY25liChLfhNbvP0GTOftoE6OfFuH3claq/g=='

function Fail($msg) { Write-Error "hopsesh install: $msg"; exit 1 }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) { 'AMD64' { 'amd64' } 'ARM64' { 'arm64' } default { Fail "unsupported architecture $($env:PROCESSOR_ARCHITECTURE)" } }
$tag = $env:HOPSESH_VERSION
if (-not $tag) {
  $rel = Invoke-RestMethod -UseBasicParsing "https://api.github.com/repos/$Repo/releases/latest"
  $tag = $rel.tag_name
}
if (-not $tag.StartsWith('v')) { $tag = "v$tag" }
$version = $tag.Substring(1)
$archive = "hopsesh_${version}_windows_${arch}.zip"
$base = "https://github.com/$Repo/releases/download/$tag"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("hopsesh-" + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "Downloading hopsesh $version for windows/$arch..."
  Invoke-WebRequest -UseBasicParsing "$base/$archive" -OutFile "$tmp\$archive"
  Invoke-WebRequest -UseBasicParsing "$base/checksums.txt" -OutFile "$tmp\checksums.txt"

  if ($ReleasePubKey) {
    if ($PSVersionTable.PSVersion.Major -ge 7) {
      Invoke-WebRequest -UseBasicParsing "$base/checksums.txt.sig" -OutFile "$tmp\checksums.txt.sig"
      $ecdsa = [Security.Cryptography.ECDsa]::Create()
      [void]$ecdsa.ImportSubjectPublicKeyInfo([Convert]::FromBase64String($ReleasePubKey), [ref]0)
      $ok = $ecdsa.VerifyData([IO.File]::ReadAllBytes("$tmp\checksums.txt"), [IO.File]::ReadAllBytes("$tmp\checksums.txt.sig"),
        [Security.Cryptography.HashAlgorithmName]::SHA256, [Security.Cryptography.DSASignatureFormat]::Rfc3279DerSequence)
      if (-not $ok) { Fail 'checksums.txt signature is NOT valid; not installing' }
    } else {
      Write-Warning 'Windows PowerShell 5.1 cannot check the release signature; checking the checksum only (use PowerShell 7 for both)'
    }
  } else {
    Write-Warning 'this installer has no release key yet; checking the checksum only'
  }

  $want = (Get-Content "$tmp\checksums.txt" | Where-Object { ($_ -split '\s+')[1] -eq $archive } | ForEach-Object { ($_ -split '\s+')[0] }) | Select-Object -First 1
  if (-not $want) { Fail "checksums.txt has no entry for $archive" }
  $got = (Get-FileHash -Algorithm SHA256 "$tmp\$archive").Hash.ToLower()
  if ($got -ne $want.ToLower()) { Fail "checksum mismatch for $archive; not installing" }

  Expand-Archive -Path "$tmp\$archive" -DestinationPath "$tmp\x" -Force
  $dir = if ($env:HOPSESH_INSTALL_DIR) { $env:HOPSESH_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\hopsesh' }
  New-Item -ItemType Directory -Force -Path $dir | Out-Null
  $dest = Join-Path $dir 'hopsesh.exe'
  if (Test-Path $dest) { Move-Item -Force $dest "$dest.old" }
  Copy-Item "$tmp\x\hopsesh.exe" $dest
  Write-Host "Installed $(& $dest version) at $dest"

  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if (-not ($userPath -split ';' | Where-Object { $_ -eq $dir })) {
    [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ";$dir"), 'User')
    Write-Host "Added $dir to your user PATH (open a new terminal to use it)."
  }
  Write-Host 'Start with: hopsesh   (or hopsesh --help)'
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
