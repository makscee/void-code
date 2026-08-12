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
$shaPattern = '^[a-f0-9]{64}$'
$isoPattern = '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$'

function Test-ExactKeys($Value, [string[]]$Keys) {
  if ($null -eq $Value) { return $false }
  $actual = @($Value.PSObject.Properties.Name | Sort-Object)
  $expected = @($Keys | Sort-Object)
  return (($actual -join "`n") -ceq ($expected -join "`n"))
}
function New-Document([string]$Result, [string]$Check, [string]$Code, $Candidate, $Processes, $Support) {
  [ordered]@{
    schema = 1; phase = ($Phase -creplace '([a-z])([A-Z])','$1_$2').ToLowerInvariant()
    occurredAt = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ss.fffZ')
    result = $Result; check = $Check; coarseCode = $Code
    candidate = $Candidate; processes = @($Processes); support = $Support
  }
}
function Write-Document($Document) {
  $json = $Document | ConvertTo-Json -Depth 8 -Compress
  if ($OutputFile) {
    if (Test-Path -LiteralPath $OutputFile) { throw 'OUTPUT_EXISTS' }
    [IO.File]::WriteAllText($OutputFile, $json + "`n", (New-Object Text.UTF8Encoding($false)))
  } else { [Console]::Out.WriteLine($json) }
  if ($Document.result -eq 'PASS') { exit 0 } else { exit 1 }
}
function Stop-Document([string]$Check, [string]$Code) { Write-Document (New-Document 'STOP' $Check $Code $null @() $null) }
function Read-JsonFile([string]$File) {
  if (-not $File -or -not [IO.File]::Exists($File)) { throw 'INVALID_INPUT' }
  return [IO.File]::ReadAllText($File) | ConvertFrom-Json
}
function Get-OwnedRows([int]$CandidateRoot) {
  $rows = @(Get-CimInstance Win32_Process | Select-Object Name,ProcessId,ParentProcessId)
  $roots = @($rows | Where-Object { [int]$_.ProcessId -eq $CandidateRoot })
  if ($roots.Count -ne 1 -or $CandidateRoot -le 0) { throw 'ROOT_NOT_FOUND' }
  if ($roots[0].Name -notin @('Void Code','Void Code.exe')) { throw 'OWNERSHIP_AMBIGUOUS' }
  $ids = New-Object 'System.Collections.Generic.HashSet[int]'
  if (-not $ids.Add($CandidateRoot)) { throw 'OWNERSHIP_AMBIGUOUS' }
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
  return @($owned | ForEach-Object { [ordered]@{ name=[IO.Path]::GetFileNameWithoutExtension([string]$_.Name); pid=[int]$_.ProcessId; parentPid=[int]$_.ParentProcessId } })
}

try {
  if ($Phase -eq 'Preflight') {
    $manifestValue = Read-JsonFile $Manifest
    if (-not (Test-ExactKeys $manifestValue @('schema','product','source','build','installer','resources','predecessor','signing','operatorGate')) -or
        -not (Test-ExactKeys $manifestValue.product @('name','version')) -or
        -not (Test-ExactKeys $manifestValue.source @('commit','branch','remote','originUrl')) -or
        -not (Test-ExactKeys $manifestValue.build @('timestamp')) -or
        -not (Test-ExactKeys $manifestValue.installer @('basename','size','sha256','arch')) -or
        -not (Test-ExactKeys $manifestValue.resources @('manifest','platform')) -or
        -not (Test-ExactKeys $manifestValue.resources.manifest @('basename','size','sha256')) -or
        -not (Test-ExactKeys $manifestValue.predecessor @('reference','installerSha256')) -or
        -not (Test-ExactKeys $manifestValue.signing @('status')) -or
        -not (Test-ExactKeys $manifestValue.operatorGate @('status','evidence','verifiedAt')) -or
        $manifestValue.schema -ne 1 -or $manifestValue.product.name -cne 'Void Code' -or
        $manifestValue.installer.arch -cne 'x64' -or $manifestValue.resources.platform -cne 'win32-x64' -or
        $manifestValue.resources.manifest.basename -cne 'manifest.json' -or $manifestValue.signing.status -cne 'unsigned' -or
        $manifestValue.installer.sha256 -cnotmatch $shaPattern -or $manifestValue.predecessor.installerSha256 -cnotmatch $shaPattern -or
        $manifestValue.predecessor.reference -match '^(latest|current|head|pending|unknown|tbd|none)$') { Stop-Document 'MANIFEST' 'MANIFEST_INVALID' }
    if ($manifestValue.operatorGate.status -cne 'verified') { Stop-Document 'MANIFEST' 'OPERATOR_GATE_BLOCKED' }
    if (-not $Installer -or -not [IO.File]::Exists($Installer)) { Stop-Document 'MANIFEST' 'ARTIFACT_MISMATCH' }
    $item = Get-Item -LiteralPath $Installer
    $actual = (Get-FileHash -LiteralPath $Installer -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($item.Name -cne $manifestValue.installer.basename -or $item.Length -ne $manifestValue.installer.size -or $actual -cne $manifestValue.installer.sha256) { Stop-Document 'MANIFEST' 'ARTIFACT_MISMATCH' }
    $signature = Get-AuthenticodeSignature -LiteralPath $Installer
    if ($signature.Status -ne [System.Management.Automation.SignatureStatus]::NotSigned) { Stop-Document 'MANIFEST' 'ARTIFACT_MISMATCH' }
    $motwPresent = Test-Path -LiteralPath $Installer -Stream Zone.Identifier -ErrorAction Stop
    $motw = 'absent'; if ($motwPresent) { $motw = 'present' }
    $candidate = [ordered]@{ installerBasename=$item.Name; expectedSha256=$manifestValue.installer.sha256; actualSha256=$actual; operatorGate='verified'; signature='not_signed'; motw=$motw }
    Write-Document (New-Document 'PASS' 'MANIFEST' 'NONE' $candidate @() $null)
  }
  if ($Phase -eq 'DuringLaunch') {
    $owned = Get-OwnedRows $RootPid
    Write-Document (New-Document 'PASS' 'PROCESS_OWNERSHIP' 'NONE' $null $owned $null)
  }
  if ($Phase -eq 'AfterChatClose') {
    $owned = Get-OwnedRows $RootPid
    Write-Document (New-Document 'PASS' 'PROCESS_OWNERSHIP' 'NONE' $null $owned $null)
  }
  if ($Phase -eq 'AfterQuit' -or $Phase -eq 'AfterUninstall') {
    $prior = Read-JsonFile $PriorEvidence
    if (-not (Test-ExactKeys $prior @('schema','phase','occurredAt','result','check','coarseCode','candidate','processes','support')) -or $prior.schema -ne 1 -or $prior.check -cne 'PROCESS_OWNERSHIP' -or $prior.result -cne 'PASS') { Stop-Document 'PROCESS_EXIT' 'OWNERSHIP_AMBIGUOUS' }
    foreach ($process in $prior.processes) {
      if (-not (Test-ExactKeys $process @('name','pid','parentPid')) -or $process.name -notin @('Void Code','vc','node','OpenConsole','conhost') -or [int]$process.pid -le 0) { Stop-Document 'PROCESS_EXIT' 'OWNERSHIP_AMBIGUOUS' }
    }
    $priorIds = @($prior.processes | ForEach-Object { [int]$_.pid })
    if ($priorIds.Count -lt 1 -or @($priorIds | Select-Object -Unique).Count -ne $priorIds.Count -or $priorIds -notcontains $RootPid) { Stop-Document 'PROCESS_EXIT' 'OWNERSHIP_AMBIGUOUS' }
    $rows = @(Get-CimInstance Win32_Process | Select-Object Name,ProcessId,ParentProcessId)
    $remaining = @($rows | Where-Object { $priorIds -contains [int]$_.ProcessId })
    if ($remaining.Count -gt 0) { Stop-Document 'PROCESS_EXIT' 'OWNED_PROCESS_REMAINS' }
    Write-Document (New-Document 'PASS' 'PROCESS_EXIT' 'NONE' $null @() $null)
  }
  if ($Phase -eq 'SupportReport') {
    $report = Read-JsonFile $SupportReport
    $valid = (Test-ExactKeys $report @('schema','app','system','generatedAt','state')) -and (Test-ExactKeys $report.app @('name','version')) -and
      (Test-ExactKeys $report.system @('platform','architecture')) -and (Test-ExactKeys $report.state @('workspace','runtime','recoveryCode')) -and
      $report.schema -eq 1 -and $report.app.name -ceq 'Void Code' -and $report.app.version -match '^\d+\.\d+\.\d+$' -and
      $report.system.platform -in @('windows','macos','linux','other') -and $report.system.architecture -in @('x64','arm64','other') -and
      $report.generatedAt -match $isoPattern -and $report.state.workspace -in @('none','ready','missing') -and
      $report.state.runtime -in @('not_started','running','ended','start_failed') -and
      $report.state.recoveryCode -in @('NONE','AUTH_PREFLIGHT_REQUIRED','SESSION_START_FAILED','RUNTIME_EXITED','WORKSPACE_MISSING','SESSION_MISSING')
    if (-not $valid) { Stop-Document 'SUPPORT_REPORT' 'SUPPORT_REPORT_INVALID' }
    $hash = (Get-FileHash -LiteralPath $SupportReport -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Document (New-Document 'PASS' 'SUPPORT_REPORT' 'NONE' $null @() ([ordered]@{sha256=$hash;valid=$true}))
  }
} catch {
  $check = 'MANIFEST'; $code = 'MANIFEST_INVALID'
  if ($Phase -match 'Launch|Chat|Quit|Uninstall') { $check = 'PROCESS_OWNERSHIP'; $code = 'OWNERSHIP_AMBIGUOUS' }
  if ($Phase -match 'Quit|Uninstall') { $check = 'PROCESS_EXIT' }
  if ($Phase -eq 'SupportReport') { $check = 'SUPPORT_REPORT'; $code = 'SUPPORT_REPORT_INVALID' }
  Stop-Document $check $code
}
