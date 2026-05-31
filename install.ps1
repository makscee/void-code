# vc Windows installer (PowerShell).
#
# Usage:
#   iex (irm https://auth.makscee.ru/vc/install.ps1)
#
# With access code (logs in automatically):
#   $env:VC_CODE='ABCD-EFGH'; iex (irm https://auth.makscee.ru/vc/install.ps1)
#
# Installs vc.exe — the void-code relay launcher for Windows.
# Also installs node (if absent) and @anthropic-ai/claude-code via npm.
#
# Env:
#   $env:VC_CODE              access code (optional — runs vc login after install).
#                             Wiped from env after use — never echoed or written to disk.
#   $env:VC_AUTH_HOST         default https://auth.makscee.ru — overrides fetch URL.
#                             Used by e2e harness to point at staging.
#   $env:VC_LANG              language for vc UI: en (default) or ru. If set,
#                             skips the interactive prompt and uses this value.
#   $env:VC_INSTALL_DRY_RUN = '1'  print URLs that would be fetched, then exit.

$ErrorActionPreference = 'Stop'

$authHost = if ($env:VC_AUTH_HOST) { $env:VC_AUTH_HOST } else { 'https://auth.makscee.ru' }

# Fetch version.json to get current version + canonical artifact path.
# If fetch fails, fall back to hardcoded path — never hardcode as primary.
$versionJsonUrl = "$authHost/vc/version.json"
$versionBanner = '==> void-code installer'
$vcArtifactPath = 'bin/vc-windows-amd64.exe'  # fallback
try {
    $versionJson = Invoke-RestMethod -Uri $versionJsonUrl -UseBasicParsing -ErrorAction Stop
    if ($versionJson.version) {
        $versionBanner = "==> void-code installer (v$($versionJson.version))"
    }
    # Read canonical windows/amd64 artifact path from version.json
    $fromJson = $null
    if ($versionJson.artifacts) {
        $fromJson = $versionJson.artifacts.'windows/amd64'
        if (-not $fromJson) { $fromJson = $versionJson.artifacts.'windows-amd64' }
    }
    if (-not $fromJson -and $versionJson.files) {
        $fromJson = $versionJson.files.'windows-amd64'
    }
    if ($fromJson) { $vcArtifactPath = $fromJson }
} catch {
    # version.json fetch failed — banner without version, artifact path from fallback
}
Write-Host $versionBanner

$vcUrl      = "$authHost/vc/$vcArtifactPath"
$relayCaUrl = "$authHost/vc/relay-ca.pem"

Write-Host "==> detecting platform: windows/amd64"

# Language select: use VC_LANG env if already set; prompt when interactive.
if (-not $env:VC_LANG) {
    if ([Environment]::UserInteractive -and -not [Console]::IsInputRedirected) {
        Write-Host ""
        Write-Host "==> Select language / Выберите язык:"
        Write-Host "    1) English"
        Write-Host "    2) Русский"
        $langChoice = Read-Host "Choice [1]"
        Write-Host ""
        if ($langChoice -eq '2') {
            $env:VC_LANG = 'ru'
        } else {
            $env:VC_LANG = 'en'
        }
    } else {
        $env:VC_LANG = 'en'
    }
}

if ($env:VC_INSTALL_DRY_RUN -eq '1') {
    Write-Output "GET $vcUrl"
    Write-Output "GET $relayCaUrl"
    Write-Output "WOULD: winget install OpenJS.NodeJS (if node absent)"
    Write-Output "WOULD: npm.cmd install -g @anthropic-ai/claude-code (if claude absent, default prefix)"
    if ($env:VC_CODE) {
        Write-Output "WOULD: vc login (VC_CODE set)"
    }
    Write-Output "VC_LANG=$($env:VC_LANG)"
    exit 0
}

$vcDir  = Join-Path $env:USERPROFILE '.void-code'
$binDir = Join-Path $vcDir 'bin'
New-Item -ItemType Directory -Force -Path $binDir | Out-Null

# 1. Download vc.exe
$tmp = [IO.Path]::GetTempFileName() + '.exe'
Write-Host "==> downloading vc binary from $authHost" -ForegroundColor Cyan
Invoke-WebRequest -Uri $vcUrl -OutFile $tmp -UseBasicParsing

# Verify download is non-empty (basic sanity check)
$tmpSize = (Get-Item $tmp).Length
if ($tmpSize -lt 1024) {
    Write-Host "vc: download looks too small ($tmpSize bytes) — aborting" -ForegroundColor Red
    Remove-Item -Force $tmp -ErrorAction SilentlyContinue
    exit 1
}

# Signature check: vc-windows-amd64.exe — unsigned preview.
# Minisign verification will be added in a future release.
Write-Host "==> verifying download (unsigned preview — minisign in future release)" -ForegroundColor Yellow

$target = Join-Path $binDir 'vc.exe'
$old    = Join-Path $binDir 'vc.old.exe'
if (Test-Path $old)    { Remove-Item -Force $old }
if (Test-Path $target) { Move-Item -Force $target $old }
Move-Item -Force $tmp $target
Write-Host "==> installing to $target" -ForegroundColor Green

# 2. Download relay CA (public cert)
$caPath = Join-Path $vcDir 'relay-ca.pem'
Write-Host "==> provisioning relay CA" -ForegroundColor Cyan
Invoke-WebRequest -Uri $relayCaUrl -OutFile $caPath -UseBasicParsing

# Add ~/.void-code/bin to user PATH if not already there (idempotent)
$userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
if ($userPath -notlike "*$binDir*") {
    $newPath = if ($userPath) { "$userPath;$binDir" } else { $binDir }
    [Environment]::SetEnvironmentVariable('PATH', $newPath, 'User')
    Write-Host "==> added $binDir to user PATH (open a new terminal to pick up)" -ForegroundColor Green
}
if ($env:PATH -notlike "*$binDir*") { $env:PATH = "$env:PATH;$binDir" }

# 3. Install node if absent
Write-Host "==> checking node / claude dependencies"
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

# 4. Install @anthropic-ai/claude-code via npm.cmd if absent
# npm.cmd bypasses Windows execution-policy restrictions that block npm.ps1.
# Install to the default npm global prefix (AppData\Roaming\npm) — avoids
# cross-platform path differences between Unix (~/.void-code/bin/) and Windows
# (npm puts shims at <prefix>\ root, not <prefix>\bin\).
$claudePresent = $null -ne (Get-Command claude -ErrorAction SilentlyContinue)
if (-not $claudePresent) {
    # Prefer npm.cmd (avoids execution-policy block on npm.ps1).
    # Fall back to npm if npm.cmd is not found (e.g. non-standard installs).
    $npmCmd = $null
    $npmCmdExplicit = Join-Path $env:ProgramFiles 'nodejs\npm.cmd'
    if (Test-Path $npmCmdExplicit) {
        $npmCmd = $npmCmdExplicit
    } elseif ($null -ne (Get-Command npm.cmd -ErrorAction SilentlyContinue)) {
        $npmCmd = 'npm.cmd'
    } elseif ($null -ne (Get-Command npm -ErrorAction SilentlyContinue)) {
        $npmCmd = 'npm'
    }
    if ($npmCmd) {
        Write-Host "vc: installing @anthropic-ai/claude-code via $npmCmd…" -ForegroundColor Cyan
        try {
            & $npmCmd install -g @anthropic-ai/claude-code
            Write-Host "vc: @anthropic-ai/claude-code installed" -ForegroundColor Green
        } catch {
            Write-Host "vc: npm install failed: $_" -ForegroundColor Yellow
            Write-Host "vc: install manually: npm.cmd install -g @anthropic-ai/claude-code" -ForegroundColor Yellow
        }
    } else {
        Write-Host "vc: npm not found — install node first, then re-run." -ForegroundColor Yellow
    }
} else {
    Write-Host "vc: claude already installed" -ForegroundColor Green
}

# Persist VC_LANG to ~/.void-code/config so vc can read it on first run.
$configFile = Join-Path $vcDir 'config'
$langLine = "lang=$($env:VC_LANG)"
if (Test-Path $configFile) {
    $lines = Get-Content $configFile | Where-Object { $_ -notmatch '^lang=' }
    $lines += $langLine
    $lines | Set-Content $configFile
} else {
    $langLine | Set-Content $configFile
}

# 5. vc login — use VC_CODE if provided, then wipe it
if ($env:VC_CODE) {
    Write-Host "==> running first-time login..." -ForegroundColor Cyan
    $codeValue  = $env:VC_CODE
    $env:VC_CODE = $null
    try {
        & $target login --code $codeValue
    } catch {
        Write-Host "vc: login failed: $_ — re-run 'vc login --code <YOUR-CODE>' manually" -ForegroundColor Yellow
    }
    Remove-Variable -Name codeValue -ErrorAction SilentlyContinue
}

# Post-install UX
# Refresh PATH so we can resolve the binaries we just installed.
$env:PATH = [System.Environment]::GetEnvironmentVariable('PATH','Machine') + ';' + [System.Environment]::GetEnvironmentVariable('PATH','User')

$vcResolvable = $null -ne (Get-Command vc -ErrorAction SilentlyContinue)
$claudeInstalled = $null -ne (Get-Command claude -ErrorAction SilentlyContinue)

Write-Host ""
Write-Host "==============================================" -ForegroundColor Cyan
Write-Host "  vc installed successfully!" -ForegroundColor Cyan
Write-Host "==============================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "NEXT STEPS:" -ForegroundColor White

if (-not $claudeInstalled) {
    Write-Host ""
    Write-Host "  1. Open a NEW terminal (required — claude-code needs the updated PATH)" -ForegroundColor Yellow
    Write-Host "     Then run: npm.cmd install -g @anthropic-ai/claude-code" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "  2. Run: vc login --code <YOUR-CODE-FROM-OPERATOR>" -ForegroundColor White
    Write-Host ""
    Write-Host "  3. Run: vc" -ForegroundColor White
} else {
    Write-Host ""
    if ($vcResolvable) {
        Write-Host "  1. Run: vc login --code <YOUR-CODE-FROM-OPERATOR>" -ForegroundColor White
        Write-Host ""
        Write-Host "  2. Run: vc" -ForegroundColor White
    } else {
        Write-Host "  1. Open a NEW terminal (vc is installed — new terminal picks up the PATH)" -ForegroundColor Yellow
        Write-Host ""
        Write-Host "  2. Run: vc login --code <YOUR-CODE-FROM-OPERATOR>" -ForegroundColor White
        Write-Host ""
        Write-Host "  3. Run: vc" -ForegroundColor White
    }
}

Write-Host ""
Write-Host "  Stuck? Run: vc doctor" -ForegroundColor DarkGray
Write-Host ""
