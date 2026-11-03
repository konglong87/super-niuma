$ErrorActionPreference = "Stop"

# Windows PowerShell does not turn a native program's nonzero exit into an exception.
function Invoke-CheckedCommand {
    param([string]$Command, [string[]]$Arguments)
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Command failed with exit code $LASTEXITCODE"
    }
}

$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$desktop = Join-Path $root "desktop-v2"
$frontendDist = Join-Path $desktop "frontend\dist"
$desktopBinary = Join-Path $desktop "niuma.exe"
$dist = Join-Path $root "dist"
$installer = Join-Path $desktop "build\bin\niuma-amd64-installer.exe"

foreach ($command in @("npm", "go", "wails", "makensis")) {
    Get-Command $command -ErrorAction Stop | Out-Null
}
if (-not (Test-Path (Join-Path $desktop "build\windows\installer\project.nsi"))) {
    throw "Missing customized NSIS template; the default template does not bundle niuma.exe"
}

$savedUIVersion = $env:VITE_DESKTOP_UI_VERSION
$savedGOOS = $env:GOOS
$savedGOARCH = $env:GOARCH
$savedCGOEnabled = $env:CGO_ENABLED
Push-Location $root
try {
    $env:VITE_DESKTOP_UI_VERSION = "2"
    Push-Location (Join-Path $root "web")
    try {
        Invoke-CheckedCommand "npm" @("run", "build", "--", "--mode", "desktop-v2")
    }
    finally {
        Pop-Location
    }

    if (Test-Path $frontendDist) {
        Remove-Item $frontendDist -Recurse -Force
    }
    New-Item $frontendDist -ItemType Directory -Force | Out-Null
    Copy-Item (Join-Path $root "web\dist\*") $frontendDist -Recurse -Force

    # Stage outside build/bin: Wails -clean removes that directory before NSIS runs.
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    # go-sqlite3 otherwise compiles a stub that fails only when SQLite is opened.
    $env:CGO_ENABLED = "1"
    Invoke-CheckedCommand "go" @("build", "-trimpath", "-ldflags", "-s -w", "-o", $desktopBinary, "./cmd/niuma")
    if (-not (Test-Path $desktopBinary -PathType Leaf) -or (Get-Item $desktopBinary).Length -eq 0) {
        throw "The niuma.exe sidecar was not generated"
    }

    Push-Location $desktop
    try {
        Invoke-CheckedCommand "wails" @("build", "-platform", "windows/amd64", "-nsis", "-clean", "-o", "niuma-desktop.exe")
    }
    finally {
        Pop-Location
    }

    if (-not (Test-Path $installer -PathType Leaf) -or (Get-Item $installer).Length -eq 0) {
        throw "Wails NSIS installer was not generated: $installer"
    }
    New-Item $dist -ItemType Directory -Force | Out-Null
    Copy-Item $installer (Join-Path $dist "niuma-setup.exe") -Force
    Write-Host "Created $dist\niuma-setup.exe"
}
finally {
    $env:VITE_DESKTOP_UI_VERSION = $savedUIVersion
    $env:GOOS = $savedGOOS
    $env:GOARCH = $savedGOARCH
    $env:CGO_ENABLED = $savedCGOEnabled
    Pop-Location
}
