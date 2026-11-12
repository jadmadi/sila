# Sila (صِلَة) - Windows PowerShell Installer
#
# Usage:
#   irm https://sila.jadmadi.net/install.ps1 | iex
#
# Environment variables:
#   $env:SILA_VERSION       Version to install (default: latest)
#   $env:SILA_INSTALL_DIR   Target directory (default: $HOME\.local\bin)
#   $env:SILA_URL_BASE      Base distribution URL (default: https://sila.jadmadi.net)
#   $env:SILA_NO_VERIFY     Set to 1 or true to skip SHA-256 verification

$ErrorActionPreference = "Stop"

Write-Host "==> Installing Sila (صِلَة) — Autonomous AI Agent Continuity & Governance Substrate" -ForegroundColor Cyan

# 1. Architecture Detection
$arch = "amd64"
if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") {
    $arch = "arm64"
} elseif ($env:PROCESSOR_ARCHITECTURE -ne "AMD64") {
    Write-Warning "Detected architecture '$($env:PROCESSOR_ARCHITECTURE)', defaulting to amd64."
}

$os = "windows"
$assetName = "sila-${os}-${arch}.exe"
$urlBase = if ($env:SILA_URL_BASE) { $env:SILA_URL_BASE.TrimEnd('/') } else { "https://sila.jadmadi.net/sila" }
if (-not $urlBase.EndsWith("/sila")) {
    $urlBase = "$urlBase/sila"
}
$version = if ($env:SILA_VERSION) { $env:SILA_VERSION } else { "latest" }

# 2. Determine Installation Directory
$installDir = if ($env:SILA_INSTALL_DIR) {
    $env:SILA_INSTALL_DIR
} else {
    Join-Path $HOME ".local\bin"
}

Write-Host "  • Platform:     $os/$arch" -ForegroundColor DarkGray
Write-Host "  • Version:      $version" -ForegroundColor DarkGray
Write-Host "  • Install dir:  $installDir" -ForegroundColor DarkGray

# 3. Create Temporary Directory
$tempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("sila-install-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempDir | Out-Null

try {
    # 4. Download Binary
    Write-Host "==> Downloading $assetName..." -ForegroundColor Cyan
    $tempBinPath = Join-Path $tempDir $assetName
    $primaryUrl = "$urlBase/$version/$assetName"

    try {
        Invoke-WebRequest -Uri $primaryUrl -OutFile $tempBinPath -UseBasicParsing
    } catch {
        Write-Error "Failed to download $assetName from $primaryUrl: $_"
        exit 1
    }

    # 5. Verify Cryptographic Integrity (SHA-256)
    $skipVerify = ($env:SILA_NO_VERIFY -eq "1" -or $env:SILA_NO_VERIFY -eq "true")
    if (-not $skipVerify) {
        $checksumUrl = "$urlBase/$version/checksums.txt"
        $checksumFile = Join-Path $tempDir "checksums.txt"

        $hasChecksums = $false
        try {
            Invoke-WebRequest -Uri $checksumUrl -OutFile $checksumFile -UseBasicParsing
            $hasChecksums = $true
        } catch {
            # checksum file download failed, skip verification
        }
        }

        if ($hasChecksums) {
            $lines = Get-Content $checksumFile
            $expectedHash = ""
            foreach ($line in $lines) {
                $parts = -split $line
                if ($parts.Length -ge 2 -and ($parts[1] -eq $assetName -or $parts[1] -eq "./$assetName" -or $parts[1] -eq "dist/$assetName")) {
                    $expectedHash = $parts[0].ToLower()
                    break
                }
            }

            if ($expectedHash) {
                $actualHash = (Get-FileHash -Path $tempBinPath -Algorithm SHA256).Hash.ToLower()
                if ($actualHash -ne $expectedHash) {
                    Write-Error "Security Error: SHA-256 checksum mismatch! Expected: $expectedHash, Actual: $actualHash"
                    exit 1
                }
                Write-Host "  ✔ Cryptographic integrity verified: $actualHash" -ForegroundColor Green
            }
        }
    }

    # 6. Install Binary
    if (-not (Test-Path $installDir)) {
        New-Item -ItemType Directory -Force -Path $installDir | Out-Null
    }

    $destPath = Join-Path $installDir "sila.exe"
    Copy-Item -Path $tempBinPath -Destination $destPath -Force

    # 7. Test Execution
    $versionOutput = & $destPath version 2>$null
    if (-not $versionOutput) {
        $versionOutput = & $destPath --version 2>$null
    }

    Write-Host "`n✔ Successfully installed Sila!" -ForegroundColor Green
    Write-Host "  • Binary:       $destPath" -ForegroundColor DarkGray
    Write-Host "  • Version Info: $versionOutput`n" -ForegroundColor DarkGray

    # 8. Ensure PATH Visibility
    $userPath = [Environment]::GetEnvironmentVariable("PATH", [EnvironmentVariableTarget]::User)
    $pathParts = $userPath -split ';'
    if ($pathParts -notcontains $installDir) {
        Write-Host "==> Adding $installDir to User PATH..." -ForegroundColor Yellow
        $newUserPath = if ($userPath) { "$userPath;$installDir" } else { $installDir }
        [Environment]::SetEnvironmentVariable("PATH", $newUserPath, [EnvironmentVariableTarget]::User)
        $env:PATH = "$env:PATH;$installDir"
        Write-Host "  ✔ Added to User PATH. Restart open terminals for it to take full effect.`n" -ForegroundColor Green
    }

    # 9. Quickstart Banner
    Write-Host "Quickstart Commands:" -ForegroundColor Cyan
    Write-Host "  sila prime        Prime agent context with project facts, rules, and goals"
    Write-Host "  sila clean        Universal disk hygiene across Go, Rust, pnpm, npm, uv, bun, zig"
    Write-Host "  sila gate         Execute pre-commit quality discipline checks"
    Write-Host "  sila mcp          Run 58-tool Model Context Protocol server"
    Write-Host "  sila doctor       Verify installation, database integrity, and data sovereignty`n"
    Write-Host "Documentation & Source: https://github.com/jadmadi/sila" -ForegroundColor DarkGray

} finally {
    if (Test-Path $tempDir) {
        Remove-Item -Recurse -Force -Path $tempDir | Out-Null
    }
}
