# vc Windows installer (PowerShell).
#
# Usage:
#   iex (irm https://auth.makscee.ru/vc/install.ps1)
#
# After installation, authenticate interactively with: vc login
#
# Installs vc.exe — the void-code relay launcher for Windows.
# Bootstraps node + selected agent CLIs automatically on bare machines.
# Default: vc + node + Pi only. Optional env/params install Claude Code and/or Codex.
# node installed via winget (if present) or official Node LTS .msi fallback.
#
# Env:
#   $env:VC_AUTH_HOST         default https://auth.makscee.ru — overrides fetch URL.
#                             Used by e2e harness to point at staging.
#   $env:VC_LANG              language for vc UI: en (default) or ru. If set,
#                             skips the interactive prompt and uses this value.
#   $env:VC_INSTALL_DRY_RUN = '1'  print selected npm installs, then exit.
#   $env:VC_INSTALL_PI       default '1'; install @earendil-works/pi-coding-agent.
#   $env:VC_INSTALL_CLAUDE   default '0'; install @anthropic-ai/claude-code.
#   $env:VC_INSTALL_CODEX    default '0'; install @openai/codex.
#
# Local script params:
#   -WithPi -WithoutPi -WithClaude -WithCodex

param(
    [switch]$WithPi,
    [switch]$WithoutPi,
    [switch]$WithClaude,
    [switch]$WithCodex
)

$ErrorActionPreference = 'Stop'

# BEGIN VC PATH HELPERS
function ConvertTo-VCNormalizedPathEntry {
    param([string]$Entry)

    if ($null -eq $Entry) { return '' }
    $trimmed = $Entry.Trim()
    $unquoted = $trimmed.Trim('"')
    $expanded = [Environment]::ExpandEnvironmentVariables($unquoted)
    $normalized = $expanded.Replace('/', '\')
    while ($normalized.Length -gt 3 -and ($normalized.EndsWith('\') -or $normalized.EndsWith('/'))) {
        $normalized = $normalized.Substring(0, $normalized.Length - 1)
    }
    return $normalized
}

function Test-VCPathEntryEqual {
    param(
        [string]$Left,
        [string]$Right
    )

    $leftNormalized = ConvertTo-VCNormalizedPathEntry $Left
    $rightNormalized = ConvertTo-VCNormalizedPathEntry $Right
    return [string]::Equals($leftNormalized, $rightNormalized, [StringComparison]::OrdinalIgnoreCase)
}

function Merge-VCPathEntry {
    param(
        [AllowNull()][string]$PathValue,
        [string]$RequiredEntry
    )

    if ([string]::IsNullOrEmpty($PathValue)) { return $RequiredEntry }

    $result = New-Object System.Collections.Generic.List[string]
    $found = $false
    foreach ($entry in [regex]::Split($PathValue, ';')) {
        if (Test-VCPathEntryEqual -Left $entry -Right $RequiredEntry) {
            if (-not $found) {
                [void]$result.Add($RequiredEntry)
                $found = $true
            }
        } else {
            [void]$result.Add($entry)
        }
    }
    if (-not $found) { [void]$result.Add($RequiredEntry) }
    return ($result -join ';')
}

function Set-VCUserPathEntry {
    param([string]$RequiredEntry)

    $current = [Environment]::GetEnvironmentVariable('PATH', 'User')
    $updated = Merge-VCPathEntry -PathValue $current -RequiredEntry $RequiredEntry
    if (-not [string]::Equals($current, $updated, [StringComparison]::Ordinal)) {
        # SetEnvironmentVariable writes the complete User PATH and, unlike setx,
        # does not apply setx.exe's historical expansion/truncation behavior.
        [Environment]::SetEnvironmentVariable('PATH', $updated, 'User')
        return $true
    }
    return $false
}

function Join-VCProcessPath {
    param(
        [AllowNull()][string]$MachinePath,
        [AllowNull()][string]$UserPath
    )

    $parts = New-Object System.Collections.Generic.List[string]
    if (-not [string]::IsNullOrEmpty($MachinePath)) { [void]$parts.Add($MachinePath) }
    if (-not [string]::IsNullOrEmpty($UserPath)) { [void]$parts.Add($UserPath) }
    return ($parts -join ';')
}

function Refresh-VCProcessPath {
    $machinePath = [Environment]::GetEnvironmentVariable('PATH', 'Machine')
    $userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
    $env:PATH = Join-VCProcessPath -MachinePath $machinePath -UserPath $userPath
}

function Send-VCEnvironmentChange {
    param(
        [scriptblock]$BroadcastAction = $null,
        [switch]$Quiet
    )

    try {
        if ($null -ne $BroadcastAction) {
            [void](& $BroadcastAction)
        } else {
            if ($null -eq ('VC.NativeMethods' -as [type])) {
                Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace VC {
    public static class NativeMethods {
        [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        public static extern IntPtr SendMessageTimeout(
            IntPtr hWnd, uint Msg, IntPtr wParam, string lParam,
            uint fuFlags, uint uTimeout, out IntPtr lpdwResult);
    }
}
'@
            }
            $result = [IntPtr]::Zero
            $HWND_BROADCAST = [IntPtr]0xffff
            $WM_SETTINGCHANGE = 0x001A
            $SMTO_ABORTIFHUNG = 0x0002
            $sendResult = [VC.NativeMethods]::SendMessageTimeout(
                $HWND_BROADCAST, $WM_SETTINGCHANGE, [IntPtr]::Zero,
                'Environment', $SMTO_ABORTIFHUNG, 2000, [ref]$result)
            if ($sendResult -eq [IntPtr]::Zero) {
                $errorCode = [Runtime.InteropServices.Marshal]::GetLastWin32Error()
                throw "WM_SETTINGCHANGE broadcast failed (Win32 error $errorCode)"
            }
        }
        return $true
    } catch {
        if (-not $Quiet) {
            Write-Host "vc: PATH was saved, but Windows environment notification failed: $_" -ForegroundColor Yellow
        }
        return $false
    }
}

function Test-VCVSCodeStaleRisk {
    try {
        if (Get-Process -Name 'Code','Code - Insiders','Code-Insiders' -ErrorAction SilentlyContinue) { return $true }

        $currentProcessId = $PID
        for ($depth = 0; $depth -lt 16 -and $currentProcessId; $depth++) {
            $process = Get-CimInstance Win32_Process -Filter "ProcessId=$currentProcessId" -ErrorAction Stop
            if ($null -eq $process) { break }
            if ($process.Name -ieq 'Code.exe' -or $process.Name -ieq 'Code - Insiders.exe') { return $true }
            $currentProcessId = $process.ParentProcessId
        }
    } catch {
        # Process ancestry is advisory only; PATH repair must still succeed.
    }
    return $false
}

function Get-VCPathGuidance {
    param(
        [bool]$VCResolvable,
        [bool]$VSCodeStaleRisk
    )

    $messages = New-Object System.Collections.Generic.List[string]
    if ($VSCodeStaleRisk) {
        [void]$messages.Add('VS Code may still have its old inherited PATH.')
        [void]$messages.Add('Fully exit all VS Code windows and Code.exe processes, then reopen VS Code.')
        [void]$messages.Add('The installer will not close VS Code automatically.')
    }
    if (-not $VCResolvable) {
        [void]$messages.Add('Bare vc is not available in this shell.')
        [void]$messages.Add('Direct fallback: & "$env:USERPROFILE\.void-code\bin\vc.exe" status')
    }
    return $messages.ToArray()
}
# END VC PATH HELPERS

function Get-VCInstallFlag {
    param(
        [string]$Name,
        [string]$DefaultValue
    )
    $value = [Environment]::GetEnvironmentVariable($Name)
    if ([string]::IsNullOrEmpty($value)) { return $DefaultValue }
    return $value
}

$InstallPi = (Get-VCInstallFlag 'VC_INSTALL_PI' '1') -eq '1'
$InstallClaude = (Get-VCInstallFlag 'VC_INSTALL_CLAUDE' '0') -eq '1'
$InstallCodex = (Get-VCInstallFlag 'VC_INSTALL_CODEX' '0') -eq '1'
if ($WithPi) { $InstallPi = $true }
if ($WithoutPi) { $InstallPi = $false }
if ($WithClaude) { $InstallClaude = $true }
if ($WithCodex) { $InstallCodex = $true }

$NpmInstallRetryArgs = @('--maxsockets=1', '--fetch-retries=5', '--fetch-retry-mintimeout=20000', '--fetch-retry-maxtimeout=120000', '--fetch-timeout=300000')

function Format-NpmInstallGlobal {
    param([string]$Package)
    return "npm.cmd install -g $($NpmInstallRetryArgs -join ' ') $Package"
}

if ($env:VC_INSTALL_DRY_RUN -eq '1') {
    if ($InstallPi) { Write-Output "WOULD: $(Format-NpmInstallGlobal '@earendil-works/pi-coding-agent')" }
    if ($InstallClaude) { Write-Output "WOULD: $(Format-NpmInstallGlobal '@anthropic-ai/claude-code')" }
    if ($InstallCodex) {
        Write-Output "WOULD: $(Format-NpmInstallGlobal '@openai/codex')"
        Write-Output 'WOULD: consider Codex healthy only if codex --version contains codex-cli; repair missing native optional package if needed'
    }
    Write-Output 'NEXT: vc login'
    exit 0
}

# Minimum node major version required by selected Node-based agent CLIs.
$MinNodeMajor = 22

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

# Verify the canonical installed binary directly without reading or changing auth.
try {
    $directVersion = & $target --version 2>&1
    if ($LASTEXITCODE -ne 0) { throw "exit code $LASTEXITCODE" }
    Write-Host "==> verified installed binary: $directVersion" -ForegroundColor Green
} catch {
    $verificationError = $_
    Remove-Item -Force $target -ErrorAction SilentlyContinue
    if (Test-Path $old) {
        Move-Item -Force $old $target
        throw "vc: installed binary failed direct verification; restored the previous vc.exe: $verificationError"
    }
    throw "vc: installed binary failed direct verification at ${target}: $verificationError"
}

# 2. Download relay CA (public cert). This is useful for relay-backed agents,
# but it must never prevent vc.exe itself from installing. Windows PowerShell's
# Invoke-WebRequest can occasionally wait indefinitely during this small fetch,
# so bound it and degrade with actionable guidance.
$caPath = Join-Path $vcDir 'relay-ca.pem'
Write-Host "==> provisioning relay CA" -ForegroundColor Cyan
$caReady = $false
try {
    Invoke-WebRequest -Uri $relayCaUrl -OutFile $caPath -UseBasicParsing -TimeoutSec 20 -ErrorAction Stop
    $caReady = (Test-Path $caPath) -and ((Get-Item $caPath).Length -gt 0)
    if (-not $caReady) { throw 'downloaded relay CA is empty' }
} catch {
    Remove-Item -Force $caPath -ErrorAction SilentlyContinue
    Write-Host "vc: relay CA download failed or timed out: $_" -ForegroundColor Yellow
    Write-Host "    vc is installed; retry later by re-running this installer." -ForegroundColor Yellow
}

# 2b. Trust the relay CA in the OS store so Schannel/.NET consumers (PowerShell
# Invoke-WebRequest, etc.) can validate the relay's HTTPS proxy cert. vc injects
# NODE_EXTRA_CA_CERTS so *Node* (claude) already trusts it; this covers the rest.
# NOTE (VCD-81): this is necessary but NOT sufficient for Windows system32 curl.exe
# — Schannel fail-closes on a revocation check of the relay leaf cert
# (CRYPT_E_NO_REVOCATION_CHECK) that a private CA can't satisfy, and curl has no
# proxy-revocation override. Full curl fix is relay-side (leaf cert CRL/OCSP) or the
# plaintext relay (VC_RELAY_HOST=http://relay.makscee.ru:8448). A trust prompt appears
# once (CurrentUser\Root). Non-fatal + idempotent (same-thumbprint re-import is a no-op).
# Importing a private CA into Trusted Root can display a GUI confirmation and
# indefinitely block terminal-only installs. vc passes this CA directly to its
# managed Node agents, so OS-store trust is optional. Operators that explicitly
# need Schannel/.NET trust can opt in; ordinary onboarding remains unattended.
if ($env:VC_TRUST_RELAY_CA -eq '1') {
    try {
        if (-not $caReady) { throw 'relay CA is unavailable' }
        Import-Certificate -FilePath $caPath -CertStoreLocation Cert:\CurrentUser\Root -ErrorAction Stop | Out-Null
        Write-Host "==> trusted relay CA in CurrentUser\Root store" -ForegroundColor Green
    } catch {
        Write-Host "vc: could not trust relay CA: $_" -ForegroundColor Yellow
    }
} elseif ($caReady) {
    Write-Host "==> relay CA saved (OS trust skipped; set VC_TRUST_RELAY_CA=1 to opt in)" -ForegroundColor Green
}

# Keep exactly one normalized canonical vc bin entry in User PATH. Comparisons
# are exact per PATH element and case-insensitive; unrelated entries are untouched.
$vcPathChanged = Set-VCUserPathEntry -RequiredEntry $binDir
if ($vcPathChanged) {
    Write-Host "==> repaired $binDir in user PATH" -ForegroundColor Green
}
Refresh-VCProcessPath
[void](Send-VCEnvironmentChange)

# 3. Bootstrap node if absent or below minimum required version when an agent is selected.
$AnyAgentSelected = $InstallPi -or $InstallClaude -or $InstallCodex
if ($AnyAgentSelected) {
Write-Host "==> bootstrapping node / selected agents"

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
        # winget not present (pre-2021 Windows 10) — download official Node LTS .msi.
        # Resolve the CURRENT v22 patch: the filename in latest-v22.x/ changes every
        # release, so a hardcoded patch (e.g. v22.14.0) 404s once Node moves on.
        # Fall back to a known-good only if the index fetch itself fails.
        $nodeBase = 'https://nodejs.org/dist/latest-v22.x'
        $nodeMsiFile = 'node-v22.22.3-x64.msi'
        try {
            $shaTxt = (Invoke-WebRequest -Uri "$nodeBase/SHASUMS256.txt" -UseBasicParsing).Content
            $m = [regex]::Match($shaTxt, 'node-v[0-9][0-9.]*-x64\.msi')
            if ($m.Success) { $nodeMsiFile = $m.Value }
        } catch { }
        $nodeMsiUrl = "$nodeBase/$nodeMsiFile"
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
} else {
    Write-Host "==> no agent CLIs selected; skipping node bootstrap"
}

# 4. Install selected agent CLIs via npm.cmd if absent.
# npm.cmd bypasses Windows execution-policy restrictions that block npm.ps1.
# Install to the default npm global prefix (AppData\Roaming\npm) so agent shims
# are available from new terminals after PATH refresh.
function Resolve-NpmCommand {
    $npmCmdExplicit = Join-Path $env:ProgramFiles 'nodejs\npm.cmd'
    if (Test-Path $npmCmdExplicit) { return $npmCmdExplicit }
    if ($null -ne (Get-Command npm.cmd -ErrorAction SilentlyContinue)) { return 'npm.cmd' }
    if ($null -ne (Get-Command npm -ErrorAction SilentlyContinue)) { return 'npm' }
    return $null
}

function Get-NpmPackageInstallName {
    param([string]$Package)
    $name = $Package
    $aliasIndex = $name.IndexOf('@npm:')
    if ($aliasIndex -gt 0) { $name = $name.Substring(0, $aliasIndex) }
    if ($name.StartsWith('@')) {
        $parts = $name.Split('@')
        if ($parts.Length -ge 3) { return "@$($parts[1])" }
        return $name
    }
    return ($name -split '@')[0]
}

function Clear-NpmPartialGlobalInstall {
    param(
        [string]$NpmCommand,
        [string]$Package
    )
    $name = Get-NpmPackageInstallName $Package
    if (-not $name) { return }

    try {
        $rootOutput = & $NpmCommand root -g 2>$null
        if ($LASTEXITCODE -eq 0) {
            $root = (($rootOutput | Select-Object -First 1) -as [string]).Trim()
            if ($root) {
                $pkgPath = if ($name.StartsWith('@') -and $name.Contains('/')) {
                    $parts = $name -split '/', 2
                    Join-Path (Join-Path $root $parts[0]) $parts[1]
                } else {
                    Join-Path $root $name
                }
                Remove-Item -Recurse -Force $pkgPath -ErrorAction SilentlyContinue
            }
        }
    } catch { }
}

function Invoke-NpmInstallGlobal {
    param(
        [string]$NpmCommand,
        [string]$Package
    )

    $maxAttempts = 5
    for ($attempt = 1; $attempt -le $maxAttempts; $attempt++) {
        $npmArgs = @('install', '-g') + $NpmInstallRetryArgs + @($Package)
        & $NpmCommand @npmArgs
        $exitCode = $LASTEXITCODE
        if ($exitCode -eq 0) { return $true }

        Clear-NpmPartialGlobalInstall -NpmCommand $NpmCommand -Package $Package
        if ($attempt -ge $maxAttempts) { return $false }
        $nextAttempt = $attempt + 1
        $delay = switch ($attempt) { 1 { 5 } 2 { 10 } default { 20 } }
        Write-Host "vc: npm install failed (exit code $exitCode); retrying attempt $nextAttempt/$maxAttempts in ${delay}s..." -ForegroundColor Yellow
        Start-Sleep -Seconds $delay
    }
    return $false
}

function Get-CommandSourceOrNull {
    param([string]$Name)
    $cmd = Get-Command $Name -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($cmd) { return $cmd.Source }
    return $null
}

function Get-CodexCommandPath {
    foreach ($name in @('codex.cmd', 'codex.exe', 'codex')) {
        $source = Get-CommandSourceOrNull $name
        if ($source) { return $source }
    }

    if ($env:APPDATA) {
        foreach ($file in @('codex.cmd', 'codex.exe', 'codex')) {
            $candidate = Join-Path (Join-Path $env:APPDATA 'npm') $file
            if (Test-Path $candidate) { return $candidate }
        }
    }

    return $null
}

function Invoke-CodexVersionOutput {
    $codexCmd = Get-CodexCommandPath
    if (-not $codexCmd) { return $null }

    try {
        $output = & $codexCmd --version 2>&1
        $exitCode = $LASTEXITCODE
        return [pscustomobject]@{
            Output = ($output | Out-String)
            ExitCode = $exitCode
        }
    } catch {
        return [pscustomobject]@{
            Output = ($_ | Out-String)
            ExitCode = 1
        }
    }
}

function Test-CodexHealthy {
    $result = Invoke-CodexVersionOutput
    if (-not $result) { return $false }
    return (($result.ExitCode -eq 0) -and ($result.Output -match 'codex-cli'))
}

function Test-CodexNativeOptionalMissing {
    $result = Invoke-CodexVersionOutput
    if (-not $result) { return $false }
    return ($result.Output -match '(?i)Missing optional dependency')
}

function Get-CodexNativePlatform {
    if ($env:PROCESSOR_ARCHITECTURE -match 'ARM64') { return 'win32-arm64' }
    return 'win32-x64'
}

function Get-CodexInstalledVersion {
    param([string]$NpmCommand)

    if (-not $NpmCommand) { return $null }

    $rootOutput = & $NpmCommand root -g 2>$null
    if ($LASTEXITCODE -eq 0) {
        $root = (($rootOutput | Select-Object -First 1) -as [string]).Trim()
        if ($root) {
            $packageJson = Join-Path (Join-Path $root '@openai') 'codex\package.json'
            if (Test-Path $packageJson) {
                try {
                    $pkg = Get-Content -Raw $packageJson | ConvertFrom-Json
                    if ($pkg.version) { return [string]$pkg.version }
                } catch { }
            }
        }
    }

    $listOutput = & $NpmCommand list -g '@openai/codex' --depth=0 2>$null
    $listText = ($listOutput | Out-String)
    $match = [regex]::Match($listText, '@openai/codex@([^\s]+)')
    if ($match.Success) { return $match.Groups[1].Value }

    return $null
}

function Repair-CodexNativeOptional {
    param([string]$NpmCommand)

    if (-not (Test-CodexNativeOptionalMissing)) { return $false }
    if (-not $NpmCommand) {
        Write-Host "vc: codex native repair needs npm, but npm is not available." -ForegroundColor Yellow
        return $false
    }

    $platform = Get-CodexNativePlatform
    $version = Get-CodexInstalledVersion -NpmCommand $NpmCommand
    if (-not $version) {
        Write-Host "vc: codex native repair could not determine installed @openai/codex version." -ForegroundColor Yellow
        return $false
    }

    $nativePackage = "@openai/codex-$platform@npm:@openai/codex@$version-$platform"
    Write-Host "vc: codex wrapper found but native optional package is missing; repairing $nativePackage..." -ForegroundColor Cyan
    if (Invoke-NpmInstallGlobal -NpmCommand $NpmCommand -Package $nativePackage) {
        if (Test-CodexHealthy) {
            Write-Host "vc: codex native package repaired." -ForegroundColor Green
            return $true
        }
        Write-Host "vc: codex native repair ran, but codex --version still did not report codex-cli." -ForegroundColor Yellow
        return $false
    }

    Write-Host "vc: codex native package repair failed." -ForegroundColor Yellow
    return $false
}

function Test-AgentHealthy {
    param([string]$Binary)
    if ($Binary -eq 'codex') { return (Test-CodexHealthy) }
    return ($null -ne (Get-Command $Binary -ErrorAction SilentlyContinue))
}

function Install-NpmAgent {
    param(
        [string]$Binary,
        [string]$Package,
        [string]$Label,
        [bool]$Selected,
        [string]$NpmCommand
    )

    if (-not $Selected) { return $true }
    if (Test-AgentHealthy -Binary $Binary) {
        Write-Host "vc: $Binary already installed" -ForegroundColor Green
        return $true
    }

    if ($Binary -eq 'codex' -and (Get-CodexCommandPath)) {
        if (Repair-CodexNativeOptional -NpmCommand $NpmCommand) { return $true }
        Write-Host "vc: codex found, but codex --version did not report codex-cli." -ForegroundColor Yellow
    }

    if (-not $NpmCommand) {
        Write-Host "vc: npm not found — install Node.js first, then run: $(Format-NpmInstallGlobal $Package)" -ForegroundColor Yellow
        return $false
    }

    Write-Host "vc: installing $Package ($Label) via $NpmCommand…" -ForegroundColor Cyan
    if (Invoke-NpmInstallGlobal -NpmCommand $NpmCommand -Package $Package) {
        if ($Binary -eq 'codex') {
            if ((Test-CodexHealthy) -or (Repair-CodexNativeOptional -NpmCommand $NpmCommand)) {
                Write-Host "vc: $Package installed" -ForegroundColor Green
                return $true
            }
            Write-Host "vc: $Package installed, but codex --version did not report codex-cli." -ForegroundColor Yellow
            Write-Host "vc: install manually: $(Format-NpmInstallGlobal $Package)" -ForegroundColor Yellow
            return $false
        }

        Write-Host "vc: $Package installed" -ForegroundColor Green
        return $true
    }

    Write-Host "vc: npm install failed for ${Package}" -ForegroundColor Yellow
    Write-Host "vc: install manually: $(Format-NpmInstallGlobal $Package)" -ForegroundColor Yellow
    return $false
}

$npmCmd = if ($AnyAgentSelected) { Resolve-NpmCommand } else { $null }
$piAgentOk = Install-NpmAgent -Binary 'pi' -Package '@earendil-works/pi-coding-agent' -Label 'Pi' -Selected $InstallPi -NpmCommand $npmCmd
$claudeAgentOk = Install-NpmAgent -Binary 'claude' -Package '@anthropic-ai/claude-code' -Label 'Claude Code' -Selected $InstallClaude -NpmCommand $npmCmd
$codexAgentOk = Install-NpmAgent -Binary 'codex' -Package '@openai/codex' -Label 'OpenAI Codex' -Selected $InstallCodex -NpmCommand $npmCmd

# Add npm global dir (AppData\Roaming\npm) to user PATH so npm-installed agent shims are reachable
# in new terminals. This is the default npm global prefix on Windows.
$npmGlobalDir = Join-Path $env:APPDATA 'npm'
$npmPathChanged = Set-VCUserPathEntry -RequiredEntry $npmGlobalDir
if ($npmPathChanged) {
    Write-Host "==> added npm global binary directory to user PATH" -ForegroundColor Green
    [void](Send-VCEnvironmentChange)
}
Refresh-VCProcessPath

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

# Post-install UX. Refresh this process from the complete Machine + User
# registry PATH values and verify bare-command resolution reaches this install.
Refresh-VCProcessPath

$vcCommand = Get-Command vc -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
$vcResolvable = ($null -ne $vcCommand) -and (Test-VCPathEntryEqual -Left $vcCommand.Source -Right $target)
if ($vcResolvable) {
    Write-Host "==> verified current-shell command: vc -> $($vcCommand.Source)" -ForegroundColor Green
}
$vsCodeStaleRisk = Test-VCVSCodeStaleRisk
$piInstalled = Test-AgentHealthy -Binary 'pi'
$claudeInstalled = Test-AgentHealthy -Binary 'claude'
$codexInstalled = Test-AgentHealthy -Binary 'codex'

Write-Host ""
Write-Host "==============================================" -ForegroundColor Cyan
Write-Host "  vc installed successfully!" -ForegroundColor Cyan
Write-Host "==============================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "NEXT STEPS:" -ForegroundColor White

$step = 1
foreach ($pathMessage in @(Get-VCPathGuidance -VCResolvable $vcResolvable -VSCodeStaleRisk $vsCodeStaleRisk)) {
    Write-Host "  $pathMessage" -ForegroundColor Yellow
}
if (-not $vcResolvable -or $vsCodeStaleRisk) { Write-Host "" }
if ($InstallPi -and -not $piInstalled) {
    Write-Host ""
    Write-Host "  $step. Install Pi: $(Format-NpmInstallGlobal '@earendil-works/pi-coding-agent')" -ForegroundColor Yellow
    $step++
}
if ($InstallClaude -and -not $claudeInstalled) {
    Write-Host ""
    Write-Host "  $step. Install Claude Code: $(Format-NpmInstallGlobal '@anthropic-ai/claude-code')" -ForegroundColor Yellow
    $step++
}
if ($InstallCodex -and -not $codexInstalled) {
    Write-Host ""
    Write-Host "  $step. Install OpenAI Codex: $(Format-NpmInstallGlobal '@openai/codex')" -ForegroundColor Yellow
    $step++
}
Write-Host ""
Write-Host "  $step. Log in interactively: vc login" -ForegroundColor White
$step++
Write-Host ""
Write-Host "  $step. Run: vc" -ForegroundColor White

Write-Host ""
Write-Host "  Stuck? Run: vc doctor" -ForegroundColor DarkGray
Write-Host ""
