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
function validManifest() {
  return {
    schema: 1, product: { name: 'Void Code', version: '0.1.0' },
    source: { commit: 'a'.repeat(40), branch: 'main', remote: 'origin/main', originUrl: 'https://github.com/makscee/void-code.git' },
    build: { timestamp }, installer: { basename: 'Void-Code-0.1.0-windows-x64.exe', size: 1, sha256: 'a'.repeat(64), arch: 'x64' },
    resources: { manifest: { basename: 'manifest.json', size: 1, sha256: 'b'.repeat(64) }, platform: 'win32-x64' },
    predecessor: { reference: 'v0.0.1', installerSha256: 'c'.repeat(64) }, signing: { status: 'unsigned' },
    operatorGate: { status: 'verified', evidence: 'pilot-2026-01-02', verifiedAt: timestamp },
  };
}

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

  it.runIf(pwshAvailable)('PowerShell rejects noncanonical timestamp lexemes in support, manifest, and prior evidence', () => {
    const dir = mkdtempSync(join(tmpdir(), 'vc-timestamps-'));
    const inputPath = join(dir, 'input.json');
    const support = { schema: 1, app: { name: 'Void Code', version: '0.1.0' }, system: { platform: 'windows', architecture: 'x64' }, generatedAt: timestamp, state: { workspace: 'ready', runtime: 'running', recoveryCode: 'NONE' } };
    for (const generatedAt of ['2026-01-02T03:04:05Z', '2026-01-02T03:04:05+02:00']) {
      writeFileSync(inputPath, JSON.stringify({ ...support, generatedAt }));
      const result = runPowerShell(['-Phase', 'SupportReport', '-SupportReport', quoted(inputPath)]);
      expect(result.status).toBe(1); expect(JSON.parse(result.stdout).coarseCode).toBe('SUPPORT_REPORT_INVALID');
    }
    writeFileSync(inputPath, JSON.stringify({ ...validManifest(), build: { timestamp: '2026-01-02T03:04:05Z' } }));
    let result = runPowerShell(['-Phase', 'Preflight', '-Manifest', quoted(inputPath)]);
    expect(result.status).toBe(1); expect(JSON.parse(result.stdout).coarseCode).toBe('MANIFEST_INVALID');
    const prior = { schema: 1, phase: 'during_launch', occurredAt: timestamp, result: 'PASS', check: 'PROCESS_OWNERSHIP', coarseCode: 'NONE', candidate: null, processes: [{ name: 'Void Code', pid: 20, parentPid: 1, creationDate: '2026-01-02T03:04:05+02:00' }], support: null };
    writeFileSync(inputPath, JSON.stringify(prior));
    result = runPowerShell(['-Phase', 'AfterQuit', '-PriorEvidence', quoted(inputPath), '-RootPid', '20']);
    expect(result.status).toBe(1); expect(JSON.parse(result.stdout).coarseCode).toBe('OWNERSHIP_AMBIGUOUS');
  });

  it.runIf(pwshAvailable)('PowerShell requires exact case-sensitive canonical support enums', () => {
    const dir = mkdtempSync(join(tmpdir(), 'vc-enums-')); const reportPath = join(dir, 'support.json');
    const report = { schema: 1, app: { name: 'Void Code', version: '0.1.0' }, system: { platform: 'windows', architecture: 'x64' }, generatedAt: timestamp, state: { workspace: 'ready', runtime: 'running', recoveryCode: 'NONE' } };
    for (const poisoned of [
      { ...report, system: { ...report.system, platform: 'WINDOWS' } },
      { ...report, system: { ...report.system, architecture: 'X64' } },
      { ...report, state: { ...report.state, workspace: 'READY' } },
      { ...report, state: { ...report.state, runtime: 'RUNNING' } },
      { ...report, state: { ...report.state, recoveryCode: 'none' } },
    ]) {
      writeFileSync(reportPath, JSON.stringify(poisoned));
      const result = runPowerShell(['-Phase', 'SupportReport', '-SupportReport', quoted(reportPath)]);
      expect(result.status).toBe(1); expect(JSON.parse(result.stdout).coarseCode).toBe('SUPPORT_REPORT_INVALID');
    }
  });

  it.runIf(pwshAvailable)('AfterChatClose requires DuringLaunch evidence and rejects a surviving chat identity', () => {
    const dir = mkdtempSync(join(tmpdir(), 'vc-chat-close-')); const priorPath = join(dir, 'prior.json');
    const processes = [
      { name: 'Void Code', pid: 20, parentPid: 1, creationDate: timestamp },
      { name: 'vc', pid: 30, parentPid: 20, creationDate: timestamp },
      { name: 'node', pid: 40, parentPid: 30, creationDate: timestamp },
    ];
    const prior = { schema: 1, phase: 'during_launch', occurredAt: timestamp, result: 'PASS', check: 'PROCESS_OWNERSHIP', coarseCode: 'NONE', candidate: null, processes, support: null };
    writeFileSync(priorPath, JSON.stringify(prior));
    const gone = "function Get-CimInstance { [pscustomobject]@{Name='Void Code.exe';ProcessId=20;ParentProcessId=1;CreationDate=[datetime]'2026-01-02T03:04:05.006Z'} }";
    let result = runPowerShell(['-Phase', 'AfterChatClose', '-PriorEvidence', quoted(priorPath), '-RootPid', '20'], gone);
    expect(result.status).toBe(0); expect(JSON.parse(result.stdout).check).toBe('PROCESS_EXIT');
    const remains = "function Get-CimInstance { @([pscustomobject]@{Name='Void Code.exe';ProcessId=20;ParentProcessId=1;CreationDate=[datetime]'2026-01-02T03:04:05.006Z'},[pscustomobject]@{Name='node.exe';ProcessId=40;ParentProcessId=30;CreationDate=[datetime]'2026-01-02T03:04:05.006Z'}) }";
    result = runPowerShell(['-Phase', 'AfterChatClose', '-PriorEvidence', quoted(priorPath), '-RootPid', '20'], remains);
    expect(result.status).toBe(1); expect(JSON.parse(result.stdout).coarseCode).toBe('OWNED_PROCESS_REMAINS');
    writeFileSync(priorPath, JSON.stringify({ ...prior, phase: 'after_chat_close' }));
    result = runPowerShell(['-Phase', 'AfterChatClose', '-PriorEvidence', quoted(priorPath), '-RootPid', '20'], gone);
    expect(result.status).toBe(1); expect(JSON.parse(result.stdout).coarseCode).toBe('OWNERSHIP_AMBIGUOUS');
  });

  it.runIf(pwshAvailable)('DuringLaunch scopes evidence to an explicit direct vc child and its descendants', () => {
    const rows = "function Get-CimInstance { @([pscustomobject]@{Name='Void Code.exe';ProcessId=20;ParentProcessId=1;CreationDate=[datetime]'2026-01-02T03:04:05.006Z'},[pscustomobject]@{Name='vc.exe';ProcessId=30;ParentProcessId=20;CreationDate=[datetime]'2026-01-02T03:04:05.006Z'},[pscustomobject]@{Name='node.exe';ProcessId=40;ParentProcessId=30;CreationDate=[datetime]'2026-01-02T03:04:05.006Z'},[pscustomobject]@{Name='node.exe';ProcessId=50;ParentProcessId=20;CreationDate=[datetime]'2026-01-02T03:04:05.006Z'}) }";
    let result = runPowerShell(['-Phase', 'DuringLaunch', '-RootPid', '20', '-ChatPid', '30'], rows);
    expect(result.status).toBe(0); expect(JSON.parse(result.stdout).processes.map((process: { pid: number }) => process.pid)).toEqual([20, 30, 40]);
    result = runPowerShell(['-Phase', 'DuringLaunch', '-RootPid', '20', '-ChatPid', '50'], rows);
    expect(result.status).toBe(1); expect(JSON.parse(result.stdout).coarseCode).toBe('OWNERSHIP_AMBIGUOUS');
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

  it.runIf(pwshAvailable)('PowerShell rejects uppercase mutable manifest references', () => {
    const dir = mkdtempSync(join(tmpdir(), 'vc-references-')); const manifestPath = join(dir, 'candidate.json');
    for (const manifest of [
      { ...validManifest(), predecessor: { ...validManifest().predecessor, reference: 'LATEST' } },
      { ...validManifest(), operatorGate: { ...validManifest().operatorGate, evidence: 'PENDING' } },
    ]) {
      writeFileSync(manifestPath, JSON.stringify(manifest));
      const result = runPowerShell(['-Phase', 'Preflight', '-Manifest', quoted(manifestPath)]);
      expect(result.status).toBe(1); expect(JSON.parse(result.stdout).coarseCode).toBe('MANIFEST_INVALID');
    }
  });

  it.runIf(pwshAvailable)('PowerShell rejects disconnected prior-process cycles', () => {
    const dir = mkdtempSync(join(tmpdir(), 'vc-cycle-')); const priorPath = join(dir, 'prior.json');
    const processes = [{ name: 'Void Code', pid: 20, parentPid: 1, creationDate: timestamp }, { name: 'node', pid: 30, parentPid: 40, creationDate: timestamp }, { name: 'node', pid: 40, parentPid: 30, creationDate: timestamp }];
    writeFileSync(priorPath, JSON.stringify({ schema: 1, phase: 'during_launch', occurredAt: timestamp, result: 'PASS', check: 'PROCESS_OWNERSHIP', coarseCode: 'NONE', candidate: null, processes, support: null }));
    const result = runPowerShell(['-Phase', 'AfterQuit', '-PriorEvidence', quoted(priorPath), '-RootPid', '20'], 'function Get-CimInstance { @() }');
    expect(result.status).toBe(1); expect(JSON.parse(result.stdout).coarseCode).toBe('OWNERSHIP_AMBIGUOUS'); expect(result.stderr).toBe('');
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
