import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { buildSupportReport, copySupportReport, saveSupportReport, serializeSupportReport } from '../src/main/support-report';

const base = {
  appVersion: '0.1.0', platform: 'win32', architecture: 'x64', generatedAt: '2026-07-27T01:02:03.000Z',
  workspace: 'missing' as const, runtime: 'start_failed' as const, recoveryCode: 'SESSION_START_FAILED' as const,
};

describe('allowlisted support report', () => {
  it('serializes only deterministic coarse support fields', () => {
    expect(JSON.parse(serializeSupportReport(buildSupportReport(base)))).toEqual({
      schema: 1,
      app: { name: 'Void Code', version: '0.1.0' },
      system: { platform: 'windows', architecture: 'x64' },
      generatedAt: '2026-07-27T01:02:03.000Z',
      state: { workspace: 'missing', runtime: 'start_failed', recoveryCode: 'SESSION_START_FAILED' },
    });
  });

  it('cannot collect adversarial extra fields or forbidden strings', () => {
    const poisoned = {
      ...base,
      token: 'secret-token', accessCode: 'ABCD-EFGH', env: { HOME: '/Users/accountant' },
      workspacePath: 'C:\\Clients\\Sensitive Client', terminalOutput: 'private prompt', stack: 'at /Users/accountant/app',
      commandLine: 'pi --api-key secret', fileContents: 'bank details', sessionId: 'private-session', username: 'accountant',
    };
    const text = serializeSupportReport(buildSupportReport(poisoned));
    for (const forbidden of ['secret-token', 'ABCD-EFGH', 'HOME', '/Users/', 'Sensitive Client', 'private prompt', 'bank details', 'private-session', 'accountant', 'api-key']) expect(text).not.toContain(forbidden);
    expect(Object.keys(JSON.parse(text))).toEqual(['schema', 'app', 'system', 'generatedAt', 'state']);
  });

  it('copies or saves exactly the allowlisted report and keeps cancellation inert', async () => {
    const report = buildSupportReport(base); let copied = '';
    expect(copySupportReport(report, (text) => { copied = text; })).toEqual({ action: 'copied' });
    expect(copied).toBe(serializeSupportReport(report));

    const writes: Array<{ file: string; text: string }> = [];
    await expect(saveSupportReport(report, async () => '/chosen/support.json', (file, text) => writes.push({ file, text }))).resolves.toEqual({ action: 'saved' });
    expect(writes).toEqual([{ file: '/chosen/support.json', text: serializeSupportReport(report) }]);
    await expect(saveSupportReport(report, async () => null, (file, text) => writes.push({ file, text }))).resolves.toEqual({ action: 'cancelled' });
    expect(writes).toHaveLength(1);
  });

  it('keeps copy/save authority in main without broad collection or returning a path', () => {
    const main = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');
    const preload = readFileSync(new URL('../src/preload/index.ts', import.meta.url), 'utf8');
    expect(main).toContain('copySupportReport(supportReport(raw)');
    expect(main).toContain('dialog.showSaveDialog');
    expect(main).not.toContain("action: 'saved', filePath");
    expect(preload).toContain('support: Object.freeze');
    expect(preload).not.toContain('readFile');
  });
});
