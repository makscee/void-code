import { execFileSync } from 'node:child_process';

const SIGNING_CAPABILITY_ERROR = 'Windows signing capability unavailable';

const signatureScript = String.raw`
$ErrorActionPreference = 'Stop'
$expected = $env:VC_DESKTOP_EXPECTED_PUBLISHER
$paths = @($env:VC_DESKTOP_SIGNATURE_INSTALLER, $env:VC_DESKTOP_SIGNATURE_APP)
if ([string]::IsNullOrWhiteSpace($expected) -or $expected.Trim() -cne $expected) { throw 'unavailable' }
foreach ($file in $paths) {
  if ([string]::IsNullOrWhiteSpace($file) -or -not (Test-Path -LiteralPath $file -PathType Leaf)) { throw 'unavailable' }
  $signature = Get-AuthenticodeSignature -LiteralPath $file
  if ($signature.Status -ne 'Valid' -or $null -eq $signature.SignerCertificate) { throw 'unavailable' }
  $simpleName = $signature.SignerCertificate.GetNameInfo([System.Security.Cryptography.X509Certificates.X509NameType]::SimpleName, $false)
  if (-not [string]::Equals($simpleName, $expected, [System.StringComparison]::Ordinal)) { throw 'unavailable' }
}
Write-Output 'ok'
`;

export function verifyWindowsSignatures({ installer, appExecutable, expectedPublisher, powershell, execute = execFileSync }) {
  if (typeof expectedPublisher !== 'string' || !expectedPublisher || expectedPublisher.trim() !== expectedPublisher) throw new Error(SIGNING_CAPABILITY_ERROR);
  try {
    const output = execute(powershell, ['-NoProfile', '-NonInteractive', '-Command', signatureScript], {
      encoding: 'utf8',
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'ignore'],
      env: {
        ...process.env,
        VC_DESKTOP_EXPECTED_PUBLISHER: expectedPublisher,
        VC_DESKTOP_SIGNATURE_INSTALLER: installer,
        VC_DESKTOP_SIGNATURE_APP: appExecutable,
      },
    });
    if (output.trim() !== 'ok') throw new Error(SIGNING_CAPABILITY_ERROR);
  } catch {
    throw new Error(SIGNING_CAPABILITY_ERROR);
  }
}
