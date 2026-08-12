import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const runbook = readFileSync(new URL('../docs/windows-accountant-pilot-runbook.md', import.meta.url), 'utf8');

describe('guided Windows accountant pilot runbook', () => {
  it('orders auth, hash-before-bypass, safe workspace and exact product lifecycle', () => {
    expect(runbook.indexOf('vc login --code')).toBeLessThan(runbook.indexOf('Get-FileHash'));
    expect(runbook.indexOf('Get-FileHash')).toBeLessThan(runbook.indexOf('Get-AuthenticodeSignature'));
    expect(runbook.indexOf('Get-AuthenticodeSignature')).toBeLessThan(runbook.indexOf('-Stream Zone.Identifier'));
    expect(runbook.indexOf('-Stream Zone.Identifier')).toBeLessThan(runbook.indexOf('**More info**'));
    expect(runbook).toContain('**STOP immediately:** hash mismatch');
    expect(runbook).toContain('**Run anyway**');
    expect(runbook).toContain('Documents\\Void Code\\<pilot task>');
    for (const boundary of ['live accounting database', 'whole client archive', 'network share', 'removable disk', 'cloud-sync root']) expect(runbook).toContain(boundary);
    expect(runbook).toContain('Pi can read and change everything inside the selected folder');
    for (const step of ['FIRST_CHAT PASS', 'TWO_CHAT_STATUS PASS', 'CLOSE_RESUME PASS', 'QUIT_RELAUNCH PASS', 'MISSING_FOLDER WORKSPACE_MISSING PASS', 'SUPPORT_REPORT PASS']) expect(runbook).toContain(step);
  });

  it('distinguishes exact unsigned MOTW branches without retaining values', () => {
    expect(runbook).toContain('[System.Management.Automation.SignatureStatus]::NotSigned');
    expect(runbook).toContain('SIGNATURE_NOT_SIGNED PASS');
    const absent = runbook.slice(runbook.indexOf('### `MOTW_ABSENT PASS`'), runbook.indexOf('### `MOTW_PRESENT PASS`'));
    const present = runbook.slice(runbook.indexOf('### `MOTW_PRESENT PASS`'), runbook.indexOf('## 5. Safe first workspace'));
    expect(absent).toContain('direct launch with no SmartScreen');
    expect(absent).not.toContain('**More info**');
    expect(absent).not.toContain('**Run anyway**');
    expect(present).toContain('**More info**');
    expect(present).toContain('**Run anyway**');
    for (const stop of ['Authenticode is not exactly `NotSigned`', 'MOTW inspection fails or is ambiguous', 'hash changes', 'named or unexpected publisher', 'requests disabling security', 'absent-MOTW branch shows a prompt']) expect(runbook).toContain(stop);
    for (const forbiddenValue of ['stream contents', 'URL', 'zone value', 'signature details']) expect(runbook).toContain(forbiddenValue);
  });

  it('preserves no-secret evidence, narrow process facts and explicit persistence/rollback', () => {
    for (const forbidden of ['one-time code or token', 'terminal/chat/prompt text', 'environment', 'command lines', 'raw errors/stacks', 'username/home path', 'unredacted workspace path']) expect(runbook).toContain(forbidden);
    expect(runbook).toContain('Name,ProcessId,ParentProcessId');
    expect(runbook).toContain('Do not enable a Command Line column');
    expect(runbook).toContain('Do not kill by process name');
    for (const retained of ['Electron workspace metadata', '%USERPROFILE%\\.pi\\agent\\sessions', '%USERPROFILE%\\.void-code\\token']) expect(runbook).toContain(retained);
    expect(runbook).toContain('Token deletion is a separate operator-approved `vc logout`');
    expect(runbook).toContain('predecessor.installerSha256');
    expect(runbook).toContain('Do not delete any of them during normal uninstall/rollback');
  });

  it('documents tooling without freezing a real candidate in source', () => {
    expect(runbook).toContain('npm run candidate:generate');
    expect(runbook).toContain('npm run candidate:check');
    expect(runbook).toContain('Do not use these commands during this tooling slice to create a real candidate.');
    expect(runbook).toContain('--operator-gate blocked');
    expect(runbook).toContain('--gate-evidence VC-19');
  });
});
