# vc Windows installer (PowerShell).
#
# Usage:
#   $env:VC_CODE='ABCD-EFGH'; iex (irm https://auth.makscee.ru/vc/install.ps1)
#
# Installs vc.exe — the void-code relay launcher for Windows.
# Also installs node (if absent) and @anthropic-ai/claude-code via npm.
#
# Env:
#   $env:VC_CODE              access code (required for vc login). Wiped after use.
#   $env:VC_AUTH_HOST         default https://auth.makscee.ru — overrides fetch URL.
#                             Used by e2e harness to point at staging.
#   $env:VC_INSTALL_DRY_RUN = '1'  print URLs that would be fetched, then exit.

$ErrorActionPreference = 'Stop'

$authHost = if ($env:VC_AUTH_HOST) { $env:VC_AUTH_HOST } else { 'https://auth.makscee.ru' }

$vcUrl     = "$authHost/vc/bin/vc-windows-amd64.exe"
$relayCaUrl = "$authHost/vc/relay-ca.pem"

if ($env:VC_INSTALL_DRY_RUN -eq '1') {
    Write-Output "GET $vcUrl"
    Write-Output "GET $relayCaUrl"
    Write-Output "WOULD: winget install OpenJS.NodeJS (if node absent)"
    Write-Output "WOULD: npm install -g @anthropic-ai/claude-code (if claude absent)"
    if ($env:VC_CODE) {
        Write-Output "WOULD: vc login (VC_CODE set)"
    }
    exit 0
}

$vcDir  = Join-Path $env:USERPROFILE '.void-code'
$binDir = Join-Path $vcDir 'bin'
New-Item -ItemType Directory -Force -Path $binDir | Out-Null

# 1. Download vc.exe
$tmp = [IO.Path]::GetTempFileName() + '.exe'
Write-Host "vc: downloading vc.exe from $authHost" -ForegroundColor Cyan
Invoke-WebRequest -Uri $vcUrl -OutFile $tmp -UseBasicParsing

# Signature check: vc-windows-amd64.exe — unsigned preview.
# Minisign verification will be added in a future release.
Write-Host "vc: skipping signature check (preview — unsigned build)" -ForegroundColor Yellow

$target = Join-Path $binDir 'vc.exe'
$old    = Join-Path $binDir 'vc.old.exe'
if (Test-Path $old)    { Remove-Item -Force $old }
if (Test-Path $target) { Move-Item -Force $target $old }
Move-Item -Force $tmp $target
Write-Host "vc: installed to $target" -ForegroundColor Green

# 2. Download relay CA (public cert)
$caPath = Join-Path $vcDir 'relay-ca.pem'
Write-Host "vc: downloading relay CA…" -ForegroundColor Cyan
Invoke-WebRequest -Uri $relayCaUrl -OutFile $caPath -UseBasicParsing
Write-Host "vc: relay CA saved to $caPath" -ForegroundColor Green

# Add ~/.void-code/bin to user PATH if not already there (idempotent)
$userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
if ($userPath -notlike "*$binDir*") {
    $newPath = if ($userPath) { "$userPath;$binDir" } else { $binDir }
    [Environment]::SetEnvironmentVariable('PATH', $newPath, 'User')
    Write-Host "vc: added $binDir to user PATH (restart shell to pick up)" -ForegroundColor Green
}
if ($env:PATH -notlike "*$binDir*") { $env:PATH = "$env:PATH;$binDir" }

# 3. Install node if absent
$nodePresent = $null -ne (Get-Command node -ErrorAction SilentlyContinue)
if (-not $nodePresent) {
    Write-Host "vc: node not found — installing via winget…" -ForegroundColor Cyan
    try {
        winget install --id OpenJS.NodeJS --accept-source-agreements --accept-package-agreements --silent
        $machinePath = [Environment]::GetEnvironmentVariable('PATH', 'Machine')
        $userPathNow = [Environment]::GetEnvironmentVariable('PATH', 'User')
        $env:PATH = "$machinePath;$userPathNow"
        Write-Host "vc: node installed" -ForegroundColor Green
    } catch {
        Write-Host "vc: winget install failed: $_" -ForegroundColor Yellow
        Write-Host "vc: install node manually from https://nodejs.org and re-run this script." -ForegroundColor Yellow
    }
} else {
    $nodeVer = (node --version 2>$null) -replace '^v',''
    Write-Host "vc: node already installed (v$nodeVer)" -ForegroundColor Green
}

# 4. Install @anthropic-ai/claude-code via npm if absent
$claudePresent = $null -ne (Get-Command claude -ErrorAction SilentlyContinue)
$claudeBinPath = Join-Path $env:USERPROFILE '.void-code\bin\claude'
if (-not $claudePresent -and -not (Test-Path $claudeBinPath)) {
    $npmPresent = $null -ne (Get-Command npm -ErrorAction SilentlyContinue)
    if ($npmPresent) {
        Write-Host "vc: installing @anthropic-ai/claude-code via npm…" -ForegroundColor Cyan
        $vcPrefix = Join-Path $env:USERPROFILE '.void-code'
        New-Item -ItemType Directory -Force -Path $vcPrefix | Out-Null
        try {
            $env:npm_config_prefix = $vcPrefix
            npm install -g @anthropic-ai/claude-code
            Write-Host "vc: @anthropic-ai/claude-code installed to $vcPrefix\bin\claude" -ForegroundColor Green
        } catch {
            Write-Host "vc: npm install failed: $_" -ForegroundColor Yellow
            Write-Host "vc: install manually: npm install -g @anthropic-ai/claude-code" -ForegroundColor Yellow
        }
    } else {
        Write-Host "vc: npm not found — install node first, then re-run." -ForegroundColor Yellow
    }
} else {
    Write-Host "vc: claude already installed" -ForegroundColor Green
}

# 5. vc login — use VC_CODE if provided, then wipe it
if ($env:VC_CODE) {
    $codeValue  = $env:VC_CODE
    $env:VC_CODE = $null
    Write-Host "vc: logging in with access code…" -ForegroundColor Cyan
    try {
        & $target login --code $codeValue
    } catch {
        Write-Host "vc: login failed: $_ — re-run 'vc login' manually" -ForegroundColor Yellow
    }
    Remove-Variable -Name codeValue -ErrorAction SilentlyContinue
}

# Post-install UX
$vcResolvable = $null -ne (Get-Command vc -ErrorAction SilentlyContinue)
if ($vcResolvable) {
    Write-Host ""
    Write-Host "✓ ready · run: vc" -ForegroundColor Cyan
} else {
    Write-Host ""
    Write-Host "vc installed. If 'vc' is not found, open a new PowerShell and run: vc" -ForegroundColor Yellow
}
