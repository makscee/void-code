# vc Windows installer (PowerShell).
#
# Usage:
#   iex (irm https://auth.makscee.ru/vc/install.ps1)
#
# With access code (logs in automatically):
#   $env:VC_CODE='ABCD-EFGH'; iex (irm https://auth.makscee.ru/vc/install.ps1)
#
# Installs vc.exe — the void-code relay launcher for Windows.
# Bootstraps node + @anthropic-ai/claude-code automatically on bare machines.
# node installed via winget (if present) or official Node LTS .msi fallback.
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

# Minimum node major version required by @anthropic-ai/claude-code (engines.node >=18.0.0)
$MinNodeMajor = 18

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
    # Node dry-run: show which install path would be taken
    $dryNodePresent = $null -ne (Get-Command node -ErrorAction SilentlyContinue)
    $dryNodeOk = $false
    if ($dryNodePresent) {
        $dryNodeVer = (node --version 2>$null) -replace '^v',''
        $dryNodeMajor = [int]($dryNodeVer -split '\.')[0]
        if ($dryNodeMajor -ge $MinNodeMajor) { $dryNodeOk = $true }
    }
    if (-not $dryNodeOk) {
        $hasWinget = $null -ne (Get-Command winget -ErrorAction SilentlyContinue)
        if ($hasWinget) {
            Write-Output "WOULD: winget install OpenJS.NodeJS.LTS --accept-source-agreements --accept-package-agreements --silent"
        } else {
            Write-Output "WOULD: download https://nodejs.org/dist/latest-v22.x/node-v22.14.0-x64.msi"
            Write-Output "WOULD: msiexec /i <node.msi> /qb"
        }
    }
    Write-Output "WOULD: npm.cmd install -g @anthropic-ai/claude-code (if claude absent, default prefix)"
    Write-Output "WOULD: add $env:APPDATA\npm to user PATH (npm global dir)"
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
Write-Host ""
Write-Host "  SmartScreen note: If Windows shows 'Windows protected your PC'," -ForegroundColor Yellow
Write-Host "  click 'More info' then 'Run anyway' to allow the unsigned preview binary." -ForegroundColor Yellow
Write-Host ""

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

# 3. Bootstrap node if absent or below minimum required version
Write-Host "==> bootstrapping node / claude dependencies"

# Check if node is present and meets the minimum version requirement
$hasWinget = $null -ne (Get-Command winget -ErrorAction SilentlyContinue)
$nodePresent = $null -ne (Get-Command node -ErrorAction SilentlyContinue)
$nodeOk = $false
if ($nodePresent) {
    $nodeVerStr = (node --version 2>$null) -replace '^v',''
    $nodeMajor = try { [int]($nodeVerStr -split '\.')[0] } catch { 0 }
    if ($nodeMajor -ge $MinNodeMajor) {
        $nodeOk = $true
        Write-Host "vc: node OK (v$nodeVerStr)" -ForegroundColor Green
    } else {
        Write-Host "vc: node v$nodeVerStr found but requires >=$MinNodeMajor — installing updated version" -ForegroundColor Yellow
    }
}

if (-not $nodeOk) {
    if ($hasWinget) {
        Write-Host "vc: installing Node.js LTS via winget…" -ForegroundColor Cyan
        try {
            winget install --id OpenJS.NodeJS.LTS --accept-source-agreements --accept-package-agreements --silent
            # Refresh PATH from Machine + User registries so node is visible in this session
            $machinePath = [Environment]::GetEnvironmentVariable('PATH', 'Machine')
            $userPathNow = [Environment]::GetEnvironmentVariable('PATH', 'User')
            $env:PATH = "$machinePath;$userPathNow"
            Write-Host "vc: node installed via winget" -ForegroundColor Green
        } catch {
            Write-Host "vc: winget install failed: $_" -ForegroundColor Red
            Write-Host "vc: install Node.js manually from https://nodejs.org and re-run this script." -ForegroundColor Red
            exit 1
        }
    } else {
        # winget not present (pre-2021 Windows 10) — download official Node LTS .msi
        $nodeMsiUrl = 'https://nodejs.org/dist/latest-v22.x/node-v22.14.0-x64.msi'
        $nodeMsiTmp = Join-Path $env:TEMP 'node-installer.msi'
        Write-Host "vc: winget not found — downloading Node.js LTS installer from nodejs.org…" -ForegroundColor Cyan
        try {
            Invoke-WebRequest -Uri $nodeMsiUrl -OutFile $nodeMsiTmp -UseBasicParsing
            $msiSize = (Get-Item $nodeMsiTmp).Length
            if ($msiSize -lt 1024) {
                Write-Host "vc: Node.js installer download looks too small ($msiSize bytes) — aborting" -ForegroundColor Red
                Remove-Item -Force $nodeMsiTmp -ErrorAction SilentlyContinue
                exit 1
            }
            Write-Host "vc: running Node.js installer (UAC prompt will appear)…" -ForegroundColor Cyan
            $msiResult = Start-Process msiexec -ArgumentList "/i `"$nodeMsiTmp`" /qb" -Wait -PassThru
            Remove-Item -Force $nodeMsiTmp -ErrorAction SilentlyContinue
            if ($msiResult.ExitCode -ne 0) {
                Write-Host "vc: Node.js installer failed (exit code $($msiResult.ExitCode)) or was cancelled." -ForegroundColor Red
                Write-Host "vc: Install Node.js manually from https://nodejs.org and re-run." -ForegroundColor Red
                exit 1
            }
            # Refresh PATH so node is visible in this session
            $machinePath = [Environment]::GetEnvironmentVariable('PATH', 'Machine')
            $userPathNow = [Environment]::GetEnvironmentVariable('PATH', 'User')
            $env:PATH = "$machinePath;$userPathNow"
            Write-Host "vc: node installed via .msi" -ForegroundColor Green
        } catch {
            Write-Host "vc: failed to download or run Node.js installer: $_" -ForegroundColor Red
            Write-Host "vc: Install Node.js manually from https://nodejs.org and re-run." -ForegroundColor Red
            Remove-Item -Force $nodeMsiTmp -ErrorAction SilentlyContinue
            exit 1
        }
    }

    # Re-verify after install
    $nodeCmd = Get-Command node -ErrorAction SilentlyContinue
    if ($null -eq $nodeCmd) {
        Write-Host "vc: node still not found after install. Open a new terminal and re-run." -ForegroundColor Red
        exit 1
    }
    $nodeVerStr = (node --version 2>$null) -replace '^v',''
    $nodeMajor = try { [int]($nodeVerStr -split '\.')[0] } catch { 0 }
    if ($nodeMajor -lt $MinNodeMajor) {
        Write-Host "vc: installed node v$nodeVerStr is still below minimum v$MinNodeMajor. Visit https://nodejs.org." -ForegroundColor Red
        exit 1
    }
    Write-Host "vc: node v$nodeVerStr ready" -ForegroundColor Green
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

# Add npm global dir (AppData\Roaming\npm) to user PATH so 'claude' shim is reachable
# in new terminals. This is the default npm global prefix on Windows.
$npmGlobalDir = Join-Path $env:APPDATA 'npm'
$userPathAfter = [Environment]::GetEnvironmentVariable('PATH', 'User')
if ($userPathAfter -notlike "*$npmGlobalDir*") {
    $newUserPath = if ($userPathAfter) { "$userPathAfter;$npmGlobalDir" } else { $npmGlobalDir }
    [Environment]::SetEnvironmentVariable('PATH', $newUserPath, 'User')
    Write-Host "==> added $npmGlobalDir to user PATH (claude shim location)" -ForegroundColor Green
}
if ($env:PATH -notlike "*$npmGlobalDir*") { $env:PATH = "$env:PATH;$npmGlobalDir" }

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
