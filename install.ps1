<#
.SYNOPSIS
  roach-code installer for Windows.

.DESCRIPTION
  Downloads the prebuilt binary for your CPU architecture from GitHub Releases,
  verifies its SHA-256 against the release's SHA256SUMS, installs it to a user
  directory, and adds that directory to your user PATH. No Go required.

  Install (PowerShell):
    irm https://raw.githubusercontent.com/tmdgusya/roach-code/main/install.ps1 | iex

  Environment overrides:
    ROACH_REPO         GitHub "owner/repo"  (default: tmdgusya/roach-code)
    ROACH_VERSION      tag to install       (default: latest release)
    ROACH_INSTALL_DIR  install directory    (default: %LOCALAPPDATA%\roach-code\bin)
#>
$ErrorActionPreference = 'Stop'
# Windows PowerShell 5.1 defaults to TLS 1.0/1.1, which GitHub rejects.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = if ($env:ROACH_REPO) { $env:ROACH_REPO } else { 'tmdgusya/roach-code' }
$InstallDir = if ($env:ROACH_INSTALL_DIR) { $env:ROACH_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'roach-code\bin' }

function Fail($msg) { Write-Error "install: $msg"; exit 1 }

# --- detect arch ------------------------------------------------------------
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  default { Fail "unsupported arch: $($env:PROCESSOR_ARCHITECTURE)" }
}

# --- resolve version --------------------------------------------------------
$version = $env:ROACH_VERSION
if (-not $version) {
  try {
    $rel = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing -Headers @{ 'User-Agent' = 'roach-code-install' }
    $version = $rel.tag_name
  } catch { Fail "could not resolve latest release for $Repo (set ROACH_VERSION). $_" }
}
if (-not $version) { Fail "no release version resolved" }

$asset = "roach-code-windows-$arch.zip"
$base  = "https://github.com/$Repo/releases/download/$version"
Write-Host "install: roach-code $version (windows/$arch)"

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("roach-code-" + [Guid]::NewGuid().ToString('N').Substring(0,8))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  $zip = Join-Path $tmp $asset
  Invoke-WebRequest "$base/$asset" -OutFile $zip -UseBasicParsing -Headers @{ 'User-Agent' = 'roach-code-install' }

  # --- verify checksum (only if SHA256SUMS is published) --------------------
  # Download to a file (not .Content): with -UseBasicParsing the body comes back
  # as a byte[] for octet-stream assets like SHA256SUMS, which breaks string ops.
  try {
    $sumsFile = Join-Path $tmp 'SHA256SUMS'
    Invoke-WebRequest "$base/SHA256SUMS" -OutFile $sumsFile -UseBasicParsing -Headers @{ 'User-Agent' = 'roach-code-install' }
    $sums = Get-Content $sumsFile -Raw
    $want = ($sums -split "`n" | Where-Object { $_ -match [regex]::Escape($asset) + '\s*$' } |
             Select-Object -First 1) -replace '\s.*$', ''
    if ($want) {
      $got = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLower()
      if ($got -ne $want.ToLower()) { Fail "checksum mismatch for $asset (expected $want, got $got)" }
      Write-Host "install: checksum ok"
    } else {
      Write-Host "install: checksum skipped (asset not listed in SHA256SUMS)"
    }
  } catch { Write-Host "install: checksum skipped (SHA256SUMS unavailable)" }

  # --- extract & install ----------------------------------------------------
  Expand-Archive -Path $zip -DestinationPath $tmp -Force
  $exe = Get-ChildItem -Path $tmp -Recurse -Filter 'roach-code.exe' | Select-Object -First 1
  if (-not $exe) { Fail "roach-code.exe not found in archive" }

  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
  $dest = Join-Path $InstallDir 'roach-code.exe'
  Copy-Item $exe.FullName $dest -Force

  # short alias: a `roach.cmd` shim that delegates to roach-code.exe beside it,
  # so `roach update` only ever has to replace the one real binary.
  $shim = Join-Path $InstallDir 'roach.cmd'
  Set-Content -Path $shim -Encoding ascii -Value @(
    '@echo off',
    '"%~dp0roach-code.exe" %*'
  )
  & $shim version | Out-Null
  Write-Host "install: roach-code -> $dest  (short alias: roach)"
} finally {
  Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

# --- ensure on user PATH ----------------------------------------------------
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $InstallDir) {
  $newPath = if ($userPath) { "$userPath;$InstallDir" } else { $InstallDir }
  [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
  $env:Path = "$env:Path;$InstallDir"
  Write-Host "install: added $InstallDir to your user PATH (restart terminals to pick it up)"
}

& $dest version
