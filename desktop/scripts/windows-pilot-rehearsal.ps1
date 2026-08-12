#requires -Version 5.1
[CmdletBinding()]
param(
  [Parameter(Mandatory=$true)][ValidateSet('Preflight','DuringLaunch','AfterChatClose','AfterQuit','AfterUninstall','SupportReport')][string]$Phase,
  [string]$Manifest,
  [string]$Installer,
  [int]$RootPid,
  [string]$PriorEvidence,
  [string]$SupportReport,
  [string]$OutputFile
)

$ErrorActionPreference = 'Stop'
$allowedNames = @('Void Code','Void Code.exe','vc','vc.exe','node','node.exe','OpenConsole','OpenConsole.exe','conhost','conhost.exe')
$normalizedNames = @('Void Code','vc','node','OpenConsole','conhost')
$shaPattern = '^[a-f0-9]{64}$'
$commitPattern = '^[a-f0-9]{40}$'
$versionPattern = '^\d+\.\d+\.\d+$'
$referencePattern = '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'
$mutableReferencePattern = '^(latest|current|head|pending|unknown|tbd|none)$'
$isoPattern = '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$'
$phaseName = ($Phase -creplace '([a-z])([A-Z])','$1_$2').ToLowerInvariant()

function Test-ExactKeys($Value, [string[]]$Keys) {
  if ($null -eq $Value -or $Value -is [Array] -or $Value -isnot [PSObject]) { return $false }
  $actual = @($Value.PSObject.Properties.Name | Sort-Object)
  $expected = @($Keys | Sort-Object)
  return (($actual -join "`n") -ceq ($expected -join "`n"))
}
function Test-Integer($Value) {
  return $Value -is [byte] -or $Value -is [int16] -or $Value -is [int32] -or $Value -is [int64] -or $Value -is [uint16] -or $Value -is [uint32]
}
function Test-CanonicalTimestamp($Value) {
  if ($Value -is [DateTime]) { return $Value.Kind -ne [DateTimeKind]::Unspecified }
  if ($Value -isnot [string] -or $Value -cnotmatch $isoPattern) { return $false }
  $parsed = [DateTime]::MinValue
  if (-not [DateTime]::TryParseExact($Value, 'yyyy-MM-ddTHH:mm:ss.fffZ', [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::AssumeUniversal -bor [Globalization.DateTimeStyles]::AdjustToUniversal, [ref]$parsed)) { return $false }
  return $parsed.ToString('yyyy-MM-ddTHH:mm:ss.fffZ', [Globalization.CultureInfo]::InvariantCulture) -ceq $Value
}
function Format-CanonicalTimestamp($Value) {
  if ($Value -is [DateTime]) { return $Value.ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ss.fffZ', [Globalization.CultureInfo]::InvariantCulture) }
  return [string]$Value
}
function Convert-CreationDate($Value) {
  if ($Value -is [DateTime]) { return $Value.ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ss.fffZ', [Globalization.CultureInfo]::InvariantCulture) }
  $parsed = [Management.ManagementDateTimeConverter]::ToDateTime([string]$Value)
  return $parsed.ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ss.fffZ', [Globalization.CultureInfo]::InvariantCulture)
}
function New-Document([string]$Result, [string]$Check, [string]$Code, $Candidate, $Processes, $Support) {
  [ordered]@{
    schema = 1; phase = $phaseName; occurredAt = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ss.fffZ')
    result = $Result; check = $Check; coarseCode = $Code
    candidate = $Candidate; processes = @($Processes); support = $Support
  }
}
function Write-Fallback([string]$Check, [string]$Code) {
  $fallback = New-Document 'STOP' $Check $Code $null @() $null
  try { [Console]::Out.WriteLine(($fallback | ConvertTo-Json -Depth 4 -Compress)) } catch { }
  exit 1
}
function Write-Document($Document) {
  $json = $Document | ConvertTo-Json -Depth 8 -Compress
  if ($OutputFile) {
    try {
      if ([IO.File]::Exists($OutputFile) -or [IO.Directory]::Exists($OutputFile)) { Write-Fallback $Document.check 'OUTPUT_UNAVAILABLE' }
      $stream = New-Object IO.FileStream($OutputFile, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
      try {
        $bytes = (New-Object Text.UTF8Encoding($false)).GetBytes($json + "`n")
        $stream.Write($bytes, 0, $bytes.Length)
      } finally { $stream.Dispose() }
    } catch { Write-Fallback $Document.check 'OUTPUT_UNAVAILABLE' }
  } else {
    try { [Console]::Out.WriteLine($json) } catch { exit 1 }
  }
  if ($Document.result -eq 'PASS') { exit 0 } else { exit 1 }
}
function Stop-Document([string]$Check, [string]$Code) { Write-Document (New-Document 'STOP' $Check $Code $null @() $null) }
function Read-JsonFile([string]$File) {
  if (-not $File -or -not [IO.File]::Exists($File)) { throw 'INVALID_INPUT' }
  return [IO.File]::ReadAllText($File) | ConvertFrom-Json
}
function Test-Reference($Value) { return $Value -is [string] -and $Value -cmatch $referencePattern -and $Value -cnotmatch $mutableReferencePattern }
function Test-Manifest($Value) {
  if (-not (Test-ExactKeys $Value @('schema','product','source','build','installer','resources','predecessor','signing','operatorGate')) -or
      -not (Test-ExactKeys $Value.product @('name','version')) -or
      -not (Test-ExactKeys $Value.source @('commit','branch','remote','originUrl')) -or
      -not (Test-ExactKeys $Value.build @('timestamp')) -or
      -not (Test-ExactKeys $Value.installer @('basename','size','sha256','arch')) -or
      -not (Test-ExactKeys $Value.resources @('manifest','platform')) -or
      -not (Test-ExactKeys $Value.resources.manifest @('basename','size','sha256')) -or
      -not (Test-ExactKeys $Value.predecessor @('reference','installerSha256')) -or
      -not (Test-ExactKeys $Value.signing @('status')) -or
      -not (Test-ExactKeys $Value.operatorGate @('status','evidence','verifiedAt'))) { return $false }
  if (-not (Test-Integer $Value.schema) -or $Value.schema -ne 1 -or $Value.product.name -cne 'Void Code' -or $Value.product.version -isnot [string] -or $Value.product.version -cnotmatch $versionPattern) { return $false }
  if ($Value.source.commit -isnot [string] -or $Value.source.commit -cnotmatch $commitPattern -or $Value.source.branch -cne 'main' -or $Value.source.remote -cne 'origin/main' -or $Value.source.originUrl -cnotin @('https://github.com/makscee/void-code.git','git@github.com:makscee/void-code.git','ssh://git@github.com/makscee/void-code.git')) { return $false }
  if (-not (Test-CanonicalTimestamp $Value.build.timestamp) -or $Value.installer.arch -cnotin @('x64','arm64')) { return $false }
  $expectedBasename = 'Void-Code-{0}-windows-{1}.exe' -f $Value.product.version,$Value.installer.arch
  if ($Value.installer.basename -cne $expectedBasename -or -not (Test-Integer $Value.installer.size) -or [int64]$Value.installer.size -lt 1 -or $Value.installer.sha256 -isnot [string] -or $Value.installer.sha256 -cnotmatch $shaPattern) { return $false }
  if ($Value.resources.manifest.basename -cne 'manifest.json' -or -not (Test-Integer $Value.resources.manifest.size) -or [int64]$Value.resources.manifest.size -lt 1 -or $Value.resources.manifest.sha256 -isnot [string] -or $Value.resources.manifest.sha256 -cnotmatch $shaPattern -or $Value.resources.platform -cne ('win32-' + $Value.installer.arch)) { return $false }
  if (-not (Test-Reference $Value.predecessor.reference) -or $Value.predecessor.installerSha256 -isnot [string] -or $Value.predecessor.installerSha256 -cnotmatch $shaPattern -or $Value.signing.status -cne 'unsigned' -or -not (Test-Reference $Value.operatorGate.evidence)) { return $false }
  if ($Value.operatorGate.status -ceq 'verified') { return Test-CanonicalTimestamp $Value.operatorGate.verifiedAt }
  return $Value.operatorGate.status -ceq 'blocked' -and $null -eq $Value.operatorGate.verifiedAt
}
function Get-OwnedRows([int]$CandidateRoot) {
  $rows = @(Get-CimInstance Win32_Process | Select-Object Name,ProcessId,ParentProcessId,CreationDate)
  if (@($rows | Group-Object ProcessId | Where-Object Count -ne 1).Count -gt 0) { throw 'OWNERSHIP_AMBIGUOUS' }
  $roots = @($rows | Where-Object { [int]$_.ProcessId -eq $CandidateRoot })
  if ($roots.Count -ne 1 -or $CandidateRoot -le 0) { throw 'ROOT_NOT_FOUND' }
  if ($roots[0].Name -notin @('Void Code','Void Code.exe')) { throw 'OWNERSHIP_AMBIGUOUS' }
  $ids = New-Object 'System.Collections.Generic.HashSet[int]'
  [void]$ids.Add($CandidateRoot)
  do {
    $added = $false
    foreach ($row in $rows) {
      $pidValue = [int]$row.ProcessId; $parent = [int]$row.ParentProcessId
      if ($ids.Contains($parent) -and -not $ids.Contains($pidValue)) {
        if ($pidValue -eq $parent -or -not $ids.Add($pidValue)) { throw 'OWNERSHIP_AMBIGUOUS' }
        $added = $true
      }
    }
  } while ($added)
  $owned = @($rows | Where-Object { $ids.Contains([int]$_.ProcessId) } | Sort-Object {[int]$_.ProcessId})
  foreach ($row in $owned) { if ($row.Name -notin $allowedNames) { throw 'OWNERSHIP_AMBIGUOUS' } }
  return @($owned | ForEach-Object { [ordered]@{ name=[IO.Path]::GetFileNameWithoutExtension([string]$_.Name); pid=[int]$_.ProcessId; parentPid=[int]$_.ParentProcessId; creationDate=(Convert-CreationDate $_.CreationDate) } })
}
function Test-PriorEvidence($Value) {
  if (-not (Test-ExactKeys $Value @('schema','phase','occurredAt','result','check','coarseCode','candidate','processes','support')) -or -not (Test-Integer $Value.schema) -or $Value.schema -ne 1 -or
      $Value.phase -cnotin @('during_launch','after_chat_close') -or -not (Test-CanonicalTimestamp $Value.occurredAt) -or $Value.result -cne 'PASS' -or $Value.check -cne 'PROCESS_OWNERSHIP' -or $Value.coarseCode -cne 'NONE' -or
      $null -ne $Value.candidate -or $null -ne $Value.support -or @($Value.processes).Count -lt 1) { return $false }
  $ids = @()
  foreach ($process in @($Value.processes)) {
    if (-not (Test-ExactKeys $process @('name','pid','parentPid','creationDate')) -or $process.name -cnotin $normalizedNames -or -not (Test-Integer $process.pid) -or -not (Test-Integer $process.parentPid) -or [int64]$process.pid -le 0 -or [int64]$process.parentPid -lt 0 -or -not (Test-CanonicalTimestamp $process.creationDate)) { return $false }
    $ids += [int]$process.pid
  }
  if (@($ids | Select-Object -Unique).Count -ne $ids.Count -or $ids -notcontains $RootPid) { return $false }
  $root = @(@($Value.processes) | Where-Object { [int]$_.pid -eq $RootPid })
  if ($root.Count -ne 1 -or $root[0].name -cne 'Void Code') { return $false }
  foreach ($process in @($Value.processes)) {
    if ([int]$process.pid -ne $RootPid -and $ids -notcontains [int]$process.parentPid) { return $false }
  }
  return $true
}

try {
  if ($Phase -eq 'Preflight') {
    $manifestValue = Read-JsonFile $Manifest
    if (-not (Test-Manifest $manifestValue)) { Stop-Document 'MANIFEST' 'MANIFEST_INVALID' }
    if ($manifestValue.operatorGate.status -cne 'verified') { Stop-Document 'MANIFEST' 'OPERATOR_GATE_BLOCKED' }
    if (-not $Installer -or -not [IO.File]::Exists($Installer)) { Stop-Document 'MANIFEST' 'ARTIFACT_MISMATCH' }
    $item = Get-Item -LiteralPath $Installer
    $actual = (Get-FileHash -LiteralPath $Installer -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($item.Name -cne $manifestValue.installer.basename -or $item.Length -ne $manifestValue.installer.size -or $actual -cne $manifestValue.installer.sha256) { Stop-Document 'MANIFEST' 'ARTIFACT_MISMATCH' }
    $signature = Get-AuthenticodeSignature -LiteralPath $Installer
    if ($signature.Status -ne [System.Management.Automation.SignatureStatus]::NotSigned) { Stop-Document 'MANIFEST' 'ARTIFACT_MISMATCH' }
    $motwPresent = Test-Path -LiteralPath $Installer -Stream Zone.Identifier -ErrorAction Stop
    $motw = 'absent'; if ($motwPresent) { $motw = 'present' }
    $candidate = [ordered]@{ installerBasename=$item.Name; expectedSha256=$manifestValue.installer.sha256; actualSha256=$actual; operatorGateDeclaredStatus=$manifestValue.operatorGate.status; signature='not_signed'; motw=$motw }
    Write-Document (New-Document 'PASS' 'MANIFEST' 'NONE' $candidate @() $null)
  }
  if ($Phase -eq 'DuringLaunch' -or $Phase -eq 'AfterChatClose') {
    $owned = Get-OwnedRows $RootPid
    Write-Document (New-Document 'PASS' 'PROCESS_OWNERSHIP' 'NONE' $null $owned $null)
  }
  if ($Phase -eq 'AfterQuit' -or $Phase -eq 'AfterUninstall') {
    $prior = Read-JsonFile $PriorEvidence
    if (-not (Test-PriorEvidence $prior)) { Stop-Document 'PROCESS_EXIT' 'OWNERSHIP_AMBIGUOUS' }
    $rows = @(Get-CimInstance Win32_Process | Select-Object Name,ProcessId,ParentProcessId,CreationDate)
    $remaining = @()
    foreach ($process in @($prior.processes)) {
      $current = @($rows | Where-Object { [int]$_.ProcessId -eq [int]$process.pid })
      if ($current.Count -gt 1) { Stop-Document 'PROCESS_EXIT' 'OWNERSHIP_AMBIGUOUS' }
      if ($current.Count -eq 1 -and (Convert-CreationDate $current[0].CreationDate) -ceq (Format-CanonicalTimestamp $process.creationDate)) { $remaining += $current[0] }
    }
    if ($remaining.Count -gt 0) { Stop-Document 'PROCESS_EXIT' 'OWNED_PROCESS_REMAINS' }
    Write-Document (New-Document 'PASS' 'PROCESS_EXIT' 'NONE' $null @() $null)
  }
  if ($Phase -eq 'SupportReport') {
    $report = Read-JsonFile $SupportReport
    $valid = (Test-ExactKeys $report @('schema','app','system','generatedAt','state')) -and (Test-ExactKeys $report.app @('name','version')) -and
      (Test-ExactKeys $report.system @('platform','architecture')) -and (Test-ExactKeys $report.state @('workspace','runtime','recoveryCode')) -and
      (Test-Integer $report.schema) -and $report.schema -eq 1 -and $report.app.name -ceq 'Void Code' -and $report.app.version -is [string] -and $report.app.version -cmatch $versionPattern -and
      $report.system.platform -cin @('windows','macos','linux','other') -and $report.system.architecture -cin @('x64','arm64','other') -and
      (Test-CanonicalTimestamp $report.generatedAt) -and $report.state.workspace -cin @('none','ready','missing') -and
      $report.state.runtime -cin @('not_started','running','ended','start_failed') -and
      $report.state.recoveryCode -cin @('NONE','AUTH_PREFLIGHT_REQUIRED','SESSION_START_FAILED','RUNTIME_EXITED','WORKSPACE_MISSING','SESSION_MISSING')
    if (-not $valid) { Stop-Document 'SUPPORT_REPORT' 'SUPPORT_REPORT_INVALID' }
    $hash = (Get-FileHash -LiteralPath $SupportReport -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Document (New-Document 'PASS' 'SUPPORT_REPORT' 'NONE' $null @() ([ordered]@{sha256=$hash;valid=$true}))
  }
} catch {
  $check = 'MANIFEST'; if ($Phase -match 'Launch|Chat') { $check = 'PROCESS_OWNERSHIP' }; if ($Phase -match 'Quit|Uninstall') { $check = 'PROCESS_EXIT' }; if ($Phase -eq 'SupportReport') { $check = 'SUPPORT_REPORT' }
  Write-Fallback $check 'INTERNAL_FAILURE'
}
