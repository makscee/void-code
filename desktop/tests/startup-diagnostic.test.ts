import { mkdirSync, readFileSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { startupDiagnostic, startupDialogMessage, writeStartupDiagnostic } from '../src/main/startup-diagnostic';

const roots: string[] = [];
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

describe('startup diagnostics', () => {
  it('retains safe integrity failures and stack frames without arbitrary secret values', () => {
    const secret = 'TOKEN_very_secret_123';
    const known = startupDiagnostic('runtime-validation', new Error('Node resource hash mismatch'), '0.1.0', '2026-07-28T00:00:00.000Z');
    const hostile = new Error(`bootstrap exploded ${secret}`);
    hostile.name = `${secret}Error`;
    hostile.stack = `${hostile.name}: ${hostile.message}\n    at ${secret} (/tmp/${secret}.js:12:34)\n    at C:\\Users\\${secret}\\bootstrap.js:56:78`;
    const unknown = startupDiagnostic('renderer-load', hostile, '0.1.0', '2026-07-28T00:00:00.000Z');
    expect(known.error.message).toBe('Node resource hash mismatch');
    expect(unknown).toMatchObject({ error: { name: 'Error', message: 'Unexpected startup error', stack: 'at 12:34\nat 56:78' } });
    expect(JSON.stringify(unknown)).not.toContain(secret);
    expect(unknown.error.stack).not.toContain('bootstrap exploded');
    expect(startupDialogMessage()).toBe('Void Code could not open. Reinstall the application and try again. A startup-error.json diagnostic was saved in Void Code application data.');
    expect(startupDialogMessage()).not.toContain(secret);
  });

  it('writes one private durable diagnostic under userData', () => {
    const root = path.join(os.tmpdir(), `void-startup-${crypto.randomUUID()}`); roots.push(root); mkdirSync(root);
    const file = writeStartupDiagnostic(root, startupDiagnostic('renderer-load', new Error('renderer failed to load'), '0.1.0', '2026-07-28T00:00:00.000Z'));
    expect(file).toBe(path.join(root, 'startup-error.json'));
    expect(JSON.parse(readFileSync(file, 'utf8'))).toMatchObject({ schema: 1, code: 'STARTUP_FAILED', stage: 'renderer-load' });
    expect((readFileSync(file).length)).toBeGreaterThan(20);
  });
});
