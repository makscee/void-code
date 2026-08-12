import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { descendantSnapshot, serializeEvidence, sha256Text, supportReportValid } from '../scripts/windows-pilot-rehearsal-lib.mjs';

const script = readFileSync(new URL('../scripts/windows-pilot-rehearsal.ps1', import.meta.url), 'utf8');
const timestamp = '2026-01-02T03:04:05.006Z';

describe('value-free Windows pilot rehearsal contract', () => {
  it('serializes only the exact schema and candidate classification', () => {
    const hash = 'a'.repeat(64);
    const value = { schema: 1, phase: 'preflight', occurredAt: timestamp, result: 'PASS', check: 'MANIFEST', coarseCode: 'NONE', candidate: { installerBasename: 'Void-Code-0.1.0-windows-x64.exe', expectedSha256: hash, actualSha256: hash, operatorGate: 'verified', signature: 'not_signed', motw: 'present' }, processes: [], support: null };
    expect(serializeEvidence(value)).toBe(`${JSON.stringify(value)}\n`);
    expect(() => serializeEvidence({ ...value, path: 'poison' })).toThrow('EVIDENCE_INVALID');
    expect(() => serializeEvidence({ ...value, candidate: { ...value.candidate, certificate: 'poison' } })).toThrow('EVIDENCE_INVALID');
  });

  it('validates exact support schema and rejects poisoned data', () => {
    const report = { schema: 1, app: { name: 'Void Code', version: '0.1.0' }, system: { platform: 'windows', architecture: 'x64' }, generatedAt: timestamp, state: { workspace: 'ready', runtime: 'running', recoveryCode: 'NONE' } };
    expect(supportReportValid(report)).toBe(true);
    expect(supportReportValid({ ...report, token: 'poison' })).toBe(false);
    expect(supportReportValid({ ...report, state: { ...report.state, path: 'poison' } })).toBe(false);
    expect(sha256Text(JSON.stringify(report))).toMatch(/^[a-f0-9]{64}$/);
  });

  it('builds a stable root-descendant closure and rejects ambiguity', () => {
    const rows = [{ name: 'unrelated', pid: 1, parentPid: 0 }, { name: 'node.exe', pid: 30, parentPid: 20 }, { name: 'Void Code.exe', pid: 20, parentPid: 1 }, { name: 'conhost.exe', pid: 40, parentPid: 30 }];
    expect(descendantSnapshot(rows, 20)).toEqual([{ name: 'Void Code', pid: 20, parentPid: 1 }, { name: 'node', pid: 30, parentPid: 20 }, { name: 'conhost', pid: 40, parentPid: 30 }]);
    expect(() => descendantSnapshot([...rows, { name: 'mystery.exe', pid: 50, parentPid: 20 }], 20)).toThrow('OWNERSHIP_AMBIGUOUS');
    expect(() => descendantSnapshot([...rows, { name: 'node.exe', pid: 20, parentPid: 20 }], 20)).toThrow('ROOT_NOT_FOUND');
  });

  it('source guards prohibit collection, launch, install, kill, transcript and broad export primitives', () => {
    for (const forbidden of ['Start-Transcript', 'Get-Process', 'Win32_Environment', 'Get-ChildItem', 'Stop-Process', 'taskkill', 'Start-Process', 'Invoke-Expression', 'Invoke-WebRequest', 'Remove-Item', 'Set-AuthenticodeSignature']) expect(script).not.toContain(forbidden);
    for (const forbiddenField of ['commandLine=', 'environment=', 'username=', 'homePath=', 'rawError=', 'workspace=', 'sessionId=', 'chat=']) expect(script).not.toContain(forbiddenField);
    expect(script).toContain('Get-CimInstance Win32_Process | Select-Object Name,ProcessId,ParentProcessId');
    expect(script).toContain('Get-AuthenticodeSignature -LiteralPath $Installer');
    expect(script).toContain('-Stream Zone.Identifier');
  });
});
