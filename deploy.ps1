# deploy.ps1 — build roach-code and push the fresh binary to EVERY copy that PATH
# resolves, then prove the one `roach-code` will actually launch IS the new build.
#
# Why this exists: a stale shadow at C:\Users\<you>\.bun\bin\roach-code.exe sits
# ahead of C:\Users\<you>\bin on PATH, so building into the repo's bin\ alone left
# the old binary running and "nothing changed." This deploys to all of them and
# hash-verifies the resolved one, so a build can never silently fail to take.
#
# Running session? Windows locks a live .exe against OVERWRITE but allows RENAME,
# so we hot-swap: park the locked binary aside, drop the fresh build into its
# place. The open session keeps its in-memory image; the next launch is the new
# build. (Still must restart the session to SEE the change.)
#
# Usage:  powershell -ExecutionPolicy Bypass -File .\deploy.ps1
$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $MyInvocation.MyCommand.Definition
Push-Location $repo
try {
    $version = (git describe --tags --always).Trim()
    $out = Join-Path $repo 'bin\roach-code.exe'
    Write-Host "build  $version -> bin\roach-code.exe"
    $env:CGO_ENABLED = '0'
    go build -ldflags "-s -w -X main.version=$version" -o $out ./cmd/roach-code
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    $srcHash = (Get-FileHash $out -Algorithm SHA256).Hash

    # Every target: whatever PATH resolves today + the canonical install dirs (so a
    # fresh machine still gets it), minus the build output itself.
    $targets = @()
    Get-Command roach-code -All -ErrorAction SilentlyContinue | ForEach-Object { $targets += $_.Source }
    $targets += "$env:USERPROFILE\.bun\bin\roach-code.exe"
    $targets += "$env:USERPROFILE\bin\roach-code.exe"
    $targets = $targets | Where-Object { $_ -and ($_ -ne $out) } | Select-Object -Unique

    $failed = @()
    foreach ($t in $targets) {
        $dir = Split-Path -Parent $t
        if (-not (Test-Path $dir)) { continue }
        try {
            Copy-Item -Force $out $t -ErrorAction Stop
            Write-Host "deploy -> $t"
        } catch {
            # Locked by a running session: rename the live exe aside, then drop the
            # fresh build into the original path.
            try {
                $bak = "$t.old-$((Get-Date).Ticks)"
                Rename-Item -Path $t -NewName (Split-Path -Leaf $bak) -ErrorAction Stop
                Copy-Item -Force $out $t -ErrorAction Stop
                Write-Host "deploy -> $t  (hot-swapped; live session parked at *.old-*)"
            } catch {
                $failed += $t
                Write-Warning "could not update $t : $($_.Exception.Message)"
            }
        }
    }

    # Best-effort sweep of parked binaries from prior hot-swaps (skips any still locked).
    foreach ($t in $targets) {
        Get-ChildItem -Path (Split-Path -Parent $t) -Filter 'roach-code.exe.old-*' -ErrorAction SilentlyContinue |
            ForEach-Object { Remove-Item -Force $_.FullName -ErrorAction SilentlyContinue }
    }

    if ($failed.Count -gt 0) {
        throw "deploy incomplete; could not update: $($failed -join ', ')"
    }

    # The real gate: does the binary PATH will launch match the fresh build?
    $resolved = (Get-Command roach-code -ErrorAction SilentlyContinue).Source
    if (-not $resolved) { throw "roach-code is not on PATH after deploy" }
    $resHash = (Get-FileHash $resolved -Algorithm SHA256).Hash
    if ($resHash -ne $srcHash) {
        throw "MISMATCH: 'roach-code' on PATH resolves to $resolved which is NOT the $version build"
    }
    Write-Host ""
    Write-Host "OK  'roach-code' -> $resolved  ==  fresh $version build" -ForegroundColor Green
    Write-Host "    restart any open session (exit / Ctrl-D, then 'roach-code chat') to see it."
}
finally { Pop-Location }
