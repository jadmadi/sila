# Sila (صِلَة) installer for Windows PowerShell.
#
# Default source: GitHub Releases for jadmadi/sila.
# Optional mirror: set SILA_URL_BASE to an R2-compatible base.
#
# Environment:
#   $env:SILA_RELEASE_REPO  GitHub repository (default: jadmadi/sila)
#   $env:SILA_VERSION       Release tag or "latest" (default: latest)
#   $env:SILA_URL_BASE      Optional download base
#   $env:SILA_INSTALL_DIR   Target directory (default: $HOME\.local\bin)
#   $env:SILA_NO_VERIFY     Set to 1 only for local debugging

$ErrorActionPreference = "Stop"

$repo = if ($env:SILA_RELEASE_REPO) { $env:SILA_RELEASE_REPO } else { "jadmadi/sila" }
$version = if ($env:SILA_VERSION) { $env:SILA_VERSION } else { "latest" }
$urlBase = $env:SILA_URL_BASE
$installDir = if ($env:SILA_INSTALL_DIR) { $env:SILA_INSTALL_DIR } else { Join-Path $HOME ".local\bin" }
$skipVerify = ($env:SILA_NO_VERIFY -eq "1" -or $env:SILA_NO_VERIFY -eq "true")

Write-Host "==> Installing Sila (صِلَة) - local-first continuity for AI coding agents" -ForegroundColor Cyan

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$asset = "sila-windows-$arch.exe"

if (-not $urlBase) {
    $urlBase = "https://github.com/$repo/releases/download"
    if ($version -eq "latest") {
        $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers @{ "User-Agent" = "sila-installer" }
        $version = $release.tag_name
    }
}
if ($version -notmatch '^v\d+\.\d+\.\d+') {
    throw "Resolved version '$version' is not a release tag."
}

$base = "$($urlBase.TrimEnd('/'))/$version"
$binUrl = "$base/$asset"
$checksumUrl = "$base/checksums.txt"

Write-Host "  Platform:    windows/$arch"
Write-Host "  Version:     $version"
Write-Host "  Install dir: $installDir"

$tempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("sila-install-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempDir | Out-Null

try {
    $tempBin = Join-Path $tempDir $asset
    Write-Host "==> Downloading $asset..." -ForegroundColor Cyan
    Invoke-WebRequest -Uri $binUrl -OutFile $tempBin -UseBasicParsing

    if (-not $skipVerify) {
        $checksumFile = Join-Path $tempDir "checksums.txt"
        try {
            Invoke-WebRequest -Uri $checksumUrl -OutFile $checksumFile -UseBasicParsing
        } catch {
            throw "Checksum manifest is missing at $checksumUrl. Refusing to install an unverified binary. Set SILA_NO_VERIFY=1 only for local debugging."
        }

        $expected = $null
        foreach ($line in Get-Content $checksumFile) {
            $parts = $line -split '\s+'
            if ($parts.Length -ge 2 -and ($parts[1] -eq $asset -or $parts[1] -eq "./$asset" -or $parts[1] -eq "dist/$asset")) {
                $expected = $parts[0].ToLower()
                break
            }
        }
        if (-not $expected) { throw "Checksum for $asset is missing." }

        $actual = (Get-FileHash -Path $tempBin -Algorithm SHA256).Hash.ToLower()
        if ($actual -ne $expected) { throw "Security error: checksum mismatch. Expected $expected, got $actual." }
        Write-Host "  SHA-256 verified: $actual" -ForegroundColor Green
    }

    if (-not (Test-Path $installDir)) { New-Item -ItemType Directory -Force -Path $installDir | Out-Null }
    $target = Join-Path $installDir "sila.exe"
    Copy-Item -Path $tempBin -Destination $target -Force

    Write-Host "`nInstalled $target" -ForegroundColor Green
    & $target --version 2>$null

    $userPath = [Environment]::GetEnvironmentVariable("PATH", [EnvironmentVariableTarget]::User)
    if (($userPath -split ';') -notcontains $installDir) {
        $newPath = if ($userPath) { "$userPath;$installDir" } else { $installDir }
        [Environment]::SetEnvironmentVariable("PATH", $newPath, [EnvironmentVariableTarget]::User)
        $env:PATH = "$env:PATH;$installDir"
        Write-Host "Added $installDir to your user PATH. Restart open terminals." -ForegroundColor Yellow
    }

    Write-Host "`nQuickstart:" -ForegroundColor Cyan
    Write-Host "  sila sources    # probe local AI tool stores"
    Write-Host "  sila demo       # try the sandbox without indexing your data"
    Write-Host "  sila sync       # ingest sessions read-only"
    Write-Host "  sila prime      # brief the next agent session"
    Write-Host "  sila mcp        # start the 76-tool MCP server"
} finally {
    if (Test-Path $tempDir) { Remove-Item -Recurse -Force -Path $tempDir | Out-Null }
}
