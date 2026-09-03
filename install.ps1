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

# A temp path of our own, deleted on every exit path. [IO.Path]::GetTempFileName()
# creates the placeholder too, so the old `GetTempFileName() + '.exe'` left an
# empty stub behind in TEMP on every single run.
function New-VCTempPath {
    param([string]$Suffix)
    return (Join-Path ([IO.Path]::GetTempPath()) ("vc-install-" + [Guid]::NewGuid().ToString('N') + $Suffix))
}

# The HTTP status behind a failed Invoke-WebRequest, or 0 when the host never
# answered at all (broken stream, DNS, timeout — the absence IS the signal).
#
# The two PowerShells disagree about the exception and agree about the path to
# the number: Windows PowerShell 5.1 — the only PowerShell on a stock Windows 11,
# and therefore the one that runs this file for real — throws a WebException
# carrying an HttpWebResponse, while PowerShell 7 throws an HttpResponseException
# carrying an HttpResponseMessage. Both expose .Response.StatusCode, and on both
# it is a System.Net.HttpStatusCode enum, so a single [int] cast reads it on
# either. Nothing here may depend on the exception TYPE: no test can execute 5.1
# (there is no 5.1 on the CI runner), so a 7-shaped check would be unverified
# where it matters most. The InnerException walk is for the wrapped forms 5.1
# produces when the failure surfaces while writing -OutFile.
function Get-VCHttpStatus {
    param($ErrorRecord)
    $ex = $null
    try { $ex = $ErrorRecord.Exception } catch { return 0 }
    for ($depth = 0; $depth -lt 5 -and $null -ne $ex; $depth++) {
        $response = $null
        try { $response = $ex.Response } catch { $response = $null }
        if ($null -ne $response) {
            $code = $null
            try { $code = $response.StatusCode } catch { $code = $null }
            if ($null -ne $code) {
                try { return [int]$code } catch { }
            }
        }
        try { $ex = $ex.InnerException } catch { $ex = $null }
    }
    return 0
}

# Which answers are worth asking again, quoted rather than guessed. `man curl`,
# on what --retry covers: "Transient error means either: a timeout, an FTP 4xx
# response code or an HTTP 408, 429, 500, 502, 503 or 504 response code."
# install.sh widens even that to every error with --retry-all-errors when curl is
# new enough (install.sh:101-106), so the shell path shrugs off a 502 from a host
# mid-deploy. This one must too, and used to not: any answer at all ended the
# attempts, which made an ordinary deploy-time 502 fatal on Windows alone.
$VCTransientHttpStatuses = @(408, 429, 500, 502, 503, 504)

# ── every fetch gets a retry budget, not one shot ────────────────────────────
# vc.exe is ~8 MB and dies mid-stream on some routes: the header promises a
# length, a prefix arrives, the connection drops. That is the Windows shape of
# the curl: (92) a client reported on the shell path, and Invoke-WebRequest
# meets it as a terminating error over a half-written file — one shot means a
# dead installer. So the whole fetch is attempted again, and the destination is
# cleared first: a truncated leftover must never be mistaken for the download.
#
# A verdict is not a transient answer, and the line between them is drawn by the
# status code, not by "did the host reply at all". 403 and 404 are the host's
# answer and will be the same answer three times, so they end the attempts at
# once: $AUTH_HOST/vc/SHA256SUMS is a 404 on every host until the next stable
# release, and a retry budget spent on it is paid by every install in the world.
#
# $script:VCLastDownloadStatus carries that status out to callers that must word
# their message differently for "the host says there is no such thing" and "we
# could not reach the host" — the return value stays a plain success/failure.
function Invoke-VCDownload {
    param(
        [string]$Uri,
        [string]$OutFile,
        [int]$Attempts = 3
    )
    $script:VCLastDownloadStatus = 0
    for ($attempt = 1; $attempt -le $Attempts; $attempt++) {
        try {
            Remove-Item -Force $OutFile -ErrorAction SilentlyContinue
            Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing -ErrorAction Stop
            $script:VCLastDownloadStatus = 0
            return $true
        } catch {
            Remove-Item -Force $OutFile -ErrorAction SilentlyContinue
            $status = Get-VCHttpStatus $_
            $script:VCLastDownloadStatus = $status
            $worthAskingAgain = ($status -eq 0) -or ($VCTransientHttpStatuses -contains $status)
            if (-not $worthAskingAgain -or $attempt -ge $Attempts) { return $false }
            $why = if ($status -eq 0) { 'the transfer broke' } else { "HTTP $status" }
            Write-Host "vc: download attempt $attempt/$Attempts failed for $Uri ($why) — retrying" -ForegroundColor Yellow
            Start-Sleep -Seconds 1
        }
    }
    return $false
}

# ── what the SHA256SUMS list says about the bytes we just downloaded ─────────
# THREE outcomes, not two, and that is the entire reason this helper exists:
#
#   'ok'        — the list named this asset and the bytes match;
#   'mismatch'  — the list named this asset and the bytes do NOT match. The
#                 bytes are wrong whatever their source, and no caller may
#                 install them;
#   'unchecked' — there was nothing to check against: no list at that URL, or no
#                 entry for this asset in it. Says nothing at all about the
#                 bytes, so what it means is the caller's decision.
#
# install.sh keeps that distinction in an exit code for a stated reason: an exit
# code cannot be a "be lenient" argument, so leniency the primary path allows
# can never be handed to the mirror path, which must refuse. PowerShell has no
# exit code inside a script, so the same discipline is kept by the same means:
# this helper takes no switch that could soften it and returns a fact, never a
# verdict — the verdict is written by each caller, because "refusing to install"
# is true on a third-party mirror and false on the primary host. There is
# nothing lenient here to pass anywhere.
#
# It prints the facts (which list, which hashes) and stops there.
#
# 'unchecked' arrives THREE ways, and one sentence used to be printed for all of
# them — "that list is published from the next stable release onwards" — so two
# installs out of three were told something untrue. A 404 is the only arrival
# that sentence fits: the list is not on the host yet, for anyone. A list that
# answered 5xx to every attempt, or whose transfer broke, may well be published
# and complete; what happened is that this run did not get it, and after the
# release that sentence sends the user off to wait for something that already
# shipped while their install goes through unverified. A list that arrived and
# does not name this asset is neither of those: it is stale, or not the list
# these bytes belong to, and only saying so points at the thing to fix.
#
# The cause leaves the helper in $script:VCSumsUncheckedCause ('absent' |
# 'unreachable' | 'no-entry') rather than in the return value. Callers switch on
# the three-way status, and widening that into an object would put a shape change
# on every one of them — the mirror path included, whose whole point is that
# nothing lenient can be handed to it. $script:VCLastDownloadStatus already
# carries a "why" out alongside a plain success/failure the same way, and it
# alone cannot do this job: a torn transfer and a list with no entry for us both
# leave it 0.
function Get-VCSha256Status {
    param(
        [string]$FilePath,
        [string]$SumsUrl,
        [string]$AssetName
    )
    $script:VCSumsUncheckedCause = ''
    $sumsTmp = New-VCTempPath '.sha256sums'
    try {
        if (-not (Invoke-VCDownload -Uri $SumsUrl -OutFile $sumsTmp)) {
            # 404/410 is the host's own answer that the file is not there; every
            # other ending — 5xx to the last attempt, or nothing at all (status
            # 0, the transfer broke) — is this run failing to get a file that may
            # be sitting on the host perfectly well.
            if ($script:VCLastDownloadStatus -eq 404 -or $script:VCLastDownloadStatus -eq 410) {
                $script:VCSumsUncheckedCause = 'absent'
                Write-Host "vc: no such list at $SumsUrl" -ForegroundColor Yellow
            } else {
                $script:VCSumsUncheckedCause = 'unreachable'
                Write-Host "vc: could not fetch $SumsUrl" -ForegroundColor Yellow
            }
            return 'unchecked'
        }
        # The release runs `sha256sum vc-* version.json` inside dist/, so the
        # names in the list are bare basenames; binary mode writes them as
        # `*name`. Our asset can be on any line, so every line is read.
        $want = $null
        foreach ($line in @(Get-Content -LiteralPath $sumsTmp -ErrorAction SilentlyContinue)) {
            $fields = $line.Trim() -split '\s+', 2
            if ($fields.Count -lt 2) { continue }
            if ($fields[1].Trim().TrimStart('*') -eq $AssetName) {
                $want = $fields[0].Trim()
                break
            }
        }
        if (-not $want) {
            $script:VCSumsUncheckedCause = 'no-entry'
            Write-Host "vc: $SumsUrl lists no sha256 for $AssetName" -ForegroundColor Yellow
            return 'unchecked'
        }
        $got = (Get-FileHash -LiteralPath $FilePath -Algorithm SHA256).Hash
        if ($got.ToLowerInvariant() -ne $want.ToLowerInvariant()) {
            Write-Host "vc: sha256 mismatch for $AssetName" -ForegroundColor Red
            Write-Host "    expected $($want.ToLowerInvariant())" -ForegroundColor Red
            Write-Host "    got      $($got.ToLowerInvariant())" -ForegroundColor Red
            return 'mismatch'
        }
        Write-Host "==> sha256 verified against $SumsUrl" -ForegroundColor Green
        return 'ok'
    } finally {
        Remove-Item -Force $sumsTmp -ErrorAction SilentlyContinue
    }
}

# Fetch version.json to get current version + canonical artifact path.
# If fetch fails, fall back to hardcoded path — never hardcode as primary.
$versionJsonUrl = "$authHost/vc/version.json"
$versionBanner = '==> void-code installer'
$vcArtifactPath = 'bin/vc-windows-amd64.exe'  # fallback
$versionTmp = New-VCTempPath '.version.json'
try {
    if (-not (Invoke-VCDownload -Uri $versionJsonUrl -OutFile $versionTmp)) {
        throw "could not fetch $versionJsonUrl"
    }
    $versionJson = (Get-Content -LiteralPath $versionTmp -Raw) | ConvertFrom-Json
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
} finally {
    Remove-Item -Force $versionTmp -ErrorAction SilentlyContinue
}
Write-Host $versionBanner

$vcUrl      = "$authHost/vc/$vcArtifactPath"
$relayCaUrl = "$authHost/vc/relay-ca.pem"
# The list lives beside version.json, on the host the bytes come from, and names
# the asset by its bare basename — release.yml publishes one file for both
# installers, so this is the same route install.sh takes.
$vcSumsUrl   = "$authHost/vc/SHA256SUMS"
$vcAssetName = ($vcArtifactPath -split '[\\/]')[-1]

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
# Pi is installed below VC's runtime. On Windows npm generates this .cmd package
# entrypoint; vc resolves and launches the same artifact rather than PATH `pi`.
$piRuntimeDir = Join-Path $vcDir 'runtime\pi'
$piEntry = Join-Path $piRuntimeDir 'node_modules\.bin\pi.cmd'
New-Item -ItemType Directory -Force -Path $binDir | Out-Null

# 1. Download vc.exe
$tmp = New-VCTempPath '.exe'
Write-Host "==> downloading vc binary from $authHost" -ForegroundColor Cyan
if (-not (Invoke-VCDownload -Uri $vcUrl -OutFile $tmp)) {
    Write-Host "vc: failed to download $vcUrl" -ForegroundColor Red
    Write-Host "    Check your connection and re-run; nothing was installed." -ForegroundColor Red
    Remove-Item -Force $tmp -ErrorAction SilentlyContinue
    exit 1
}

# Verify download is non-empty (basic sanity check)
$tmpSize = (Get-Item $tmp).Length
if ($tmpSize -lt 1024) {
    Write-Host "vc: download looks too small ($tmpSize bytes) — aborting" -ForegroundColor Red
    Remove-Item -Force $tmp -ErrorAction SilentlyContinue
    exit 1
}

# The primary host's own list, and the asymmetry with a third-party mirror is
# deliberate. $AUTH_HOST is where the bytes came from anyway, so its list proves
# nothing about the host — it catches a torn transfer, which is the failure this
# installer keeps meeting. What it must NOT do is refuse when the list is not
# there: the route starts existing with the next stable release, so strictness
# here would break every Windows install between that release and this change.
# Say it out loud and carry on. A mismatch is a different animal: those bytes
# are wrong, and nothing gets replaced with them.
$vcSumsStatus = Get-VCSha256Status -FilePath $tmp -SumsUrl $vcSumsUrl -AssetName $vcAssetName
if ($vcSumsStatus -eq 'mismatch') {
    Write-Host "    Refusing to install. Nothing was replaced." -ForegroundColor Red
    Remove-Item -Force $tmp -ErrorAction SilentlyContinue
    exit 1
}
if ($vcSumsStatus -ne 'ok') {
    Write-Host "vc: this download is NOT VERIFIED — there was nothing to check it against" -ForegroundColor Yellow
    Write-Host "    at $vcSumsUrl" -ForegroundColor Yellow
    # One line per cause, and the transitional one stays word for word: it is the
    # only one of the three that is true today, and the repair must not be made
    # by deleting the explanation that is correct. An unset cause says nothing
    # extra rather than guessing — silence beats a confident wrong reason.
    switch ($script:VCSumsUncheckedCause) {
        'absent'      { Write-Host "    (that list is published from the next stable release onwards)." -ForegroundColor Yellow }
        'unreachable' { Write-Host "    That list could not be fetched: the host was asked and never delivered it." -ForegroundColor Yellow }
        'no-entry'    { Write-Host "    That list has no line for $vcAssetName — it is stale, or not the list these bytes belong to." -ForegroundColor Yellow }
    }
    Write-Host "    Continuing with the install." -ForegroundColor Yellow
}

# Authenticity is a separate question from integrity, and saying so is the point:
# the line above used to read "verifying download (unsigned preview — minisign in
# future release)" while nothing but the file size had been looked at, and the
# word minisign let every reader — us included — conclude integrity was covered.
Write-Host "==> the binary is unsigned (minisign signatures come in a future release)" -ForegroundColor Yellow
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

# Add ~/.void-code/bin to user PATH if not already there (idempotent)
$userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
if ($userPath -notlike "*$binDir*") {
    $newPath = if ($userPath) { "$userPath;$binDir" } else { $binDir }
    [Environment]::SetEnvironmentVariable('PATH', $newPath, 'User')
    Write-Host "==> added $binDir to user PATH (open a new terminal to pick up)" -ForegroundColor Green
}
if ($env:PATH -notlike "*$binDir*") { $env:PATH = "$env:PATH;$binDir" }

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
    if ($Binary -eq 'pi') { return (Test-Path -LiteralPath $piEntry -PathType Leaf) }
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
    if ($Binary -eq 'pi') {
        New-Item -ItemType Directory -Force -Path $piRuntimeDir | Out-Null
        & $NpmCommand --prefix $piRuntimeDir install --no-save @NpmInstallRetryArgs $Package
        if ($LASTEXITCODE -eq 0 -and (Test-AgentHealthy -Binary 'pi')) {
            Write-Host "vc: $Package installed in VC managed runtime" -ForegroundColor Green
            return $true
        }
        Write-Host "vc: managed Pi runtime install failed" -ForegroundColor Yellow
        return $false
    }
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
$userPathAfter = [Environment]::GetEnvironmentVariable('PATH', 'User')
if ($userPathAfter -notlike "*$npmGlobalDir*") {
    $newUserPath = if ($userPathAfter) { "$userPathAfter;$npmGlobalDir" } else { $npmGlobalDir }
    [Environment]::SetEnvironmentVariable('PATH', $newUserPath, 'User')
    Write-Host "==> added npm global binary directory to user PATH (open a new terminal to pick up)" -ForegroundColor Green
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

# Post-install UX
# Refresh PATH so we can resolve the binaries we just installed.
$machinePathFinal = [System.Environment]::GetEnvironmentVariable('PATH','Machine')
$userPathFinal = [System.Environment]::GetEnvironmentVariable('PATH','User')
$env:PATH = "$machinePathFinal;$userPathFinal"

$vcResolvable = $null -ne (Get-Command vc -ErrorAction SilentlyContinue)
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
if (-not $vcResolvable) {
    Write-Host ""
    Write-Host "  $step. Open a NEW terminal (vc and npm PATH updates are picked up there)" -ForegroundColor Yellow
    $step++
}
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
