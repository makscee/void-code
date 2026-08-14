param([string]$Installer = (Join-Path $PSScriptRoot '..\install.ps1'))

$ErrorActionPreference = 'Stop'
$source = Get-Content -Raw $Installer
$begin = '# BEGIN VC PATH HELPERS'
$end = '# END VC PATH HELPERS'
$beginIndex = $source.IndexOf($begin)
$endIndex = $source.IndexOf($end)
if ($beginIndex -lt 0 -or $endIndex -le $beginIndex) {
    throw 'installer PATH helper markers are missing or out of order'
}
$helperSource = $source.Substring($beginIndex, ($endIndex + $end.Length) - $beginIndex)
Invoke-Expression $helperSource

$script:Count = 0
function Assert-Equal {
    param([string]$Name, $Actual, $Expected)
    $script:Count++
    if ($Actual -ne $Expected) {
        throw "$Name failed: actual=[$Actual] expected=[$Expected]"
    }
}
function Assert-Contains {
    param([string]$Name, [string[]]$Actual, [string]$Expected)
    $script:Count++
    if (-not (($Actual -join "`n").Contains($Expected))) {
        throw "$Name failed: output did not contain [$Expected]"
    }
}

$target = 'C:\Users\Alice\.void-code\bin'
Assert-Equal 'null PATH' (Merge-VCPathEntry -PathValue $null -RequiredEntry $target) $target
Assert-Equal 'already present' (Merge-VCPathEntry -PathValue "C:\Windows;$target;C:\Tools" -RequiredEntry $target) "C:\Windows;$target;C:\Tools"
Assert-Equal 'case and trailing slash' (Merge-VCPathEntry -PathValue 'C:\Windows;c:\users\ALICE\.VOID-CODE\BIN\;C:\Tools' -RequiredEntry $target) "C:\Windows;$target;C:\Tools"
Assert-Equal 'forward slash equivalent' (Merge-VCPathEntry -PathValue 'C:/Users/Alice/.void-code/bin/' -RequiredEntry $target) $target
Assert-Equal 'duplicate variants' (Merge-VCPathEntry -PathValue "C:\One;$target;C:\Two;c:\users\alice\.void-code\bin\\;C:\Three" -RequiredEntry $target) "C:\One;$target;C:\Two;C:\Three"
$oldUserProfile = $env:USERPROFILE
$env:USERPROFILE = 'C:\Users\Alice'
Assert-Equal 'expanded environment entry' (Merge-VCPathEntry -PathValue '%USERPROFILE%\.void-code\bin\' -RequiredEntry $target) $target
$env:USERPROFILE = $oldUserProfile
Assert-Equal 'substring is not membership' (Merge-VCPathEntry -PathValue 'C:\Users\Alice\.void-code\bin-tools' -RequiredEntry $target) "C:\Users\Alice\.void-code\bin-tools;$target"
Assert-Equal 'preserve unrelated spelling and empties' (Merge-VCPathEntry -PathValue ' C:\One ;;C:\Two\' -RequiredEntry $target) " C:\One ;;C:\Two\;$target"
$longPath = ((1..300 | ForEach-Object { "C:\Existing\Entry$_" }) -join ';')
Assert-Equal 'long PATH is not truncated' (Merge-VCPathEntry -PathValue $longPath -RequiredEntry $target) "$longPath;$target"
Assert-Equal 'repair is idempotent' (Merge-VCPathEntry -PathValue "C:\One;$target;C:\Two" -RequiredEntry $target) "C:\One;$target;C:\Two"
Assert-Equal 'refresh Machine plus User' (Join-VCProcessPath -MachinePath 'C:\Windows;C:\Program Files\Tool' -UserPath "C:\UserTools;$target") "C:\Windows;C:\Program Files\Tool;C:\UserTools;$target"
Assert-Equal 'refresh null Machine' (Join-VCProcessPath -MachinePath $null -UserPath $target) $target
Assert-Equal 'refresh null User' (Join-VCProcessPath -MachinePath 'C:\Windows' -UserPath $null) 'C:\Windows'
Assert-Equal 'broadcast failure is nonfatal' (Send-VCEnvironmentChange -BroadcastAction { throw 'simulated broadcast failure' } -Quiet) $false
Assert-Equal 'broadcast success' (Send-VCEnvironmentChange -BroadcastAction { $true } -Quiet) $true

$guidance = @(Get-VCPathGuidance -VCResolvable $false -VSCodeStaleRisk $true)
Assert-Contains 'VS Code warning' $guidance 'Fully exit all VS Code windows and Code.exe processes, then reopen VS Code.'
Assert-Contains 'direct fallback' $guidance '& "$env:USERPROFILE\.void-code\bin\vc.exe" status'
$resolvedGuidance = @(Get-VCPathGuidance -VCResolvable $true -VSCodeStaleRisk $false)
Assert-Equal 'no fallback when resolved' (($resolvedGuidance -join "`n").Contains('.void-code\bin\vc.exe')) $false
$resolvedVSCodeGuidance = @(Get-VCPathGuidance -VCResolvable $true -VSCodeStaleRisk $true)
Assert-Contains 'VS Code warning even when current shell resolves' $resolvedVSCodeGuidance 'Fully exit all VS Code windows and Code.exe processes, then reopen VS Code.'
Assert-Equal 'no fallback solely for VS Code risk' (($resolvedVSCodeGuidance -join "`n").Contains('.void-code\bin\vc.exe')) $false

Write-Output "PASS: $script:Count installer PATH assertions"
