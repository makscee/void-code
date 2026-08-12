import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { descendantSnapshot, serializeEvidence, sha256Text, supportReportValid } from '../scripts/windows-pilot-rehearsal-lib.mjs';

const scriptUrl = new URL('../scripts/windows-pilot-rehearsal.ps1', import.meta.url);
const scriptPath = decodeURIComponent(scriptUrl.pathname);
const script = readFileSync(scriptUrl, 'utf8');
const timestamp = '2026-01-02T03:04:05.006Z';
const pwshAvailable = spawnSync('pwsh', ['--version']).status === 0;
function runPowerShell(args: string[], prelude = '') {
  return spawnSync('pwsh', ['-NoLogo', '-NoProfile', '-NonInteractive', '-Command', `${prelude}\n& '${scriptPath.replaceAll("'", "''")}' ${args.join(' ')}`], { encoding: 'utf8' });
}
function quoted(value: string) { return `'${value.replaceAll("'", "''")}'`; }

describe('value-free Windows pilot rehearsal contract', () => {
  it('serializes only exact, semantically related evidence', () => {
    const hash = 'a'.repeat(64);
    const value = { schema: 1, phase: 'preflight', occurredAt: timestamp, result: 'PASS', check: 'MANIFEST', coarseCode: 'NONE', candidate: { installerBasename: 'Void-Code-0.1.0-windows-x64.exe', expectedSha256: hash, actualSha256: hash, operatorGateDeclaredStatus: 'verified', signature: 'not_signed', motw: 'present' }, processes: [], support: null };
    expect(serializeEvidence(value)).toBe(`${JSON.stringify(value)}\n`);
    expect(() => serializeEvidence({ ...value, path: 'poison' })).toThrow('EVIDENCE_INVALID');
    expect(() => serializeEvidence({ ...value, result: 'STOP' })).toThrow('EVIDENCE_INVALID');
    expect(() => serializeEvidence({ ...value, phase: 'after_quit' })).toThrow('EVIDENCE_INVALID');
    expect(() => serializeEvidence({ ...value, candidate: { ...value.candidate, certificate: 'poison' } })).toThrow('EVIDENCE_INVALID');
  });

  it('validates exact support schema and real canonical timestamps', () => {
    const report = { schema: 1, app: { name: 'Void Code', version: '0.1.0' }, system: { platform: 'windows', architecture: 'x64' }, generatedAt: timestamp, state: { workspace: 'ready', runtime: 'running', recoveryCode: 'NONE' } };
    expect(supportReportValid(report)).toBe(true);
    expect(supportReportValid({ ...report, generatedAt: '2026-99-99T99:99:99.999Z' })).toBe(false);
    expect(supportReportValid({ ...report, token: 'poison' })).toBe(false);
    expect(supportReportValid({ ...report, state: { ...report.state, path: 'poison' } })).toBe(false);
    expect(sha256Text(JSON.stringify(report))).toMatch(/^[a-f0-9]{64}$/);
  });

  it('builds a stable identity root-descendant closure and rejects ambiguity', () => {
    const rows = [{ name: 'unrelated', pid: 1, parentPid: 0, creationDate: timestamp }, { name: 'node.exe', pid: 30, parentPid: 20, creationDate: timestamp }, { name: 'Void Code.exe', pid: 20, parentPid: 1, creationDate: timestamp }, { name: 'conhost.exe', pid: 40, parentPid: 30, creationDate: timestamp }];
    expect(descendantSnapshot(rows, 20)).toEqual([{ name: 'Void Code', pid: 20, parentPid: 1, creationDate: timestamp }, { name: 'node', pid: 30, parentPid: 20, creationDate: timestamp }, { name: 'conhost', pid: 40, parentPid: 30, creationDate: timestamp }]);
    expect(() => descendantSnapshot([...rows, { name: 'mystery.exe', pid: 50, parentPid: 20, creationDate: timestamp }], 20)).toThrow('OWNERSHIP_AMBIGUOUS');
    expect(() => descendantSnapshot([...rows, { name: 'node.exe', pid: 20, parentPid: 20, creationDate: timestamp }], 20)).toThrow('ROOT_NOT_FOUND');
  });

  it.runIf(pwshAvailable)('PowerShell rejects impossible support timestamps and emits coarse collision failures', () => {
    const dir = mkdtempSync(join(tmpdir(), 'vc-pwsh-'));
    const reportPath = join(dir, 'support.json'); const outputPath = join(dir, 'exists.json');
    writeFileSync(reportPath, JSON.stringify({ schema: 1, app: { name: 'Void Code', version: '0.1.0' }, system: { platform: 'windows', architecture: 'x64' }, generatedAt: '2026-99-99T99:99:99.999Z', state: { workspace: 'ready', runtime: 'running', recoveryCode: 'NONE' } }));
    let result = runPowerShell(['-Phase', 'SupportReport', '-SupportReport', quoted(reportPath)]);
    expect(result.status).toBe(1); expect(JSON.parse(result.stdout).coarseCode).toBe('SUPPORT_REPORT_INVALID'); expect(result.stderr).toBe('');
    writeFileSync(outputPath, 'private path poison');
    result = runPowerShell(['-Phase', 'SupportReport', '-SupportReport', quoted(reportPath), '-OutputFile', quoted(outputPath)]);
    const evidence = JSON.parse(result.stdout);
    expect(result.status).toBe(1); expect(evidence.coarseCode).toBe('OUTPUT_UNAVAILABLE'); expect(result.stderr).toBe('');
    expect(`${result.stdout}${result.stderr}`).not.toContain(dir);
  });

  it.runIf(pwshAvailable)('PowerShell enforces canonical manifest fields before platform probes', () => {
    const dir = mkdtempSync(join(tmpdir(), 'vc-manifest-')); const manifestPath = join(dir, 'candidate.json');
    const manifest = { schema: 1, product: { name: 'Void Code', version: 'EVIL' }, source: { commit: 'x', branch: 'main', remote: 'origin/main', originUrl: 'https://github.com/makscee/void-code.git' }, build: { timestamp: '2026-99-99T99:99:99.999Z' }, installer: { basename: 'bad.exe', size: 0, sha256: 'a'.repeat(64), arch: 'x64' }, resources: { manifest: { basename: 'manifest.json', size: 0, sha256: 'x' }, platform: 'win32-arm64' }, predecessor: { reference: 'latest', installerSha256: 'a'.repeat(64) }, signing: { status: 'unsigned' }, operatorGate: { status: 'verified', evidence: 'pending', verifiedAt: null } };
    writeFileSync(manifestPath, JSON.stringify(manifest));
    const result = runPowerShell(['-Phase', 'Preflight', '-Manifest', quoted(manifestPath)]);
    expect(result.status).toBe(1); expect(JSON.parse(result.stdout).coarseCode).toBe('MANIFEST_INVALID'); expect(result.stderr).toBe('');
  });

  it.runIf(pwshAvailable)('PowerShell validates prior evidence and ignores reused PIDs with a new creation identity', () => {
    const dir = mkdtempSync(join(tmpdir(), 'vc-process-')); const priorPath = join(dir, 'prior.json');
    const prior = { schema: 1, phase: 'during_launch', occurredAt: timestamp, result: 'PASS', check: 'PROCESS_OWNERSHIP', coarseCode: 'NONE', candidate: null, processes: [{ name: 'Void Code', pid: 20, parentPid: 1, creationDate: timestamp }], support: null };
    writeFileSync(priorPath, JSON.stringify(prior));
    const prelude = "function Get-CimInstance { [pscustomobject]@{Name='unrelated.exe';ProcessId=20;ParentProcessId=1;CreationDate=[datetime]'2026-01-02T04:04:05.006Z'} }";
    let result = runPowerShell(['-Phase', 'AfterQuit', '-PriorEvidence', quoted(priorPath), '-RootPid', '20'], prelude);
    expect(result.status).toBe(0); expect(JSON.parse(result.stdout).result).toBe('PASS');
    writeFileSync(priorPath, JSON.stringify({ ...prior, phase: 'preflight' }));
    result = runPowerShell(['-Phase', 'AfterQuit', '-PriorEvidence', quoted(priorPath), '-RootPid', '20'], prelude);
    expect(result.status).toBe(1); expect(JSON.parse(result.stdout).coarseCode).toBe('OWNERSHIP_AMBIGUOUS');
  });

  it('source guards preserve the bounded collection and action scope', () => {
    for (const forbidden of ['Start-Transcript', 'Get-Process', 'Win32_Environment', 'Get-ChildItem', 'Stop-Process', 'taskkill', 'Start-Process', 'Invoke-Expression', 'Invoke-WebRequest', 'Remove-Item', 'Set-AuthenticodeSignature']) expect(script).not.toContain(forbidden);
    for (const forbiddenField of ['commandLine=', 'environment=', 'username=', 'homePath=', 'rawError=', 'workspace=', 'sessionId=', 'chat=']) expect(script).not.toContain(forbiddenField);
    expect(script).toContain('Select-Object Name,ProcessId,ParentProcessId,CreationDate');
    expect(script).toContain('Get-AuthenticodeSignature -LiteralPath $Installer');
    expect(script).toContain('-Stream Zone.Identifier');
  });
});
