import { mkdirSync, readFileSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { STARTUP_STAGES, startupDiagnostic, startupDialogMessage, writeStartupDiagnostic } from '../src/main/startup-diagnostic';

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

describe('a startup failure says which of the two screens failed', () => {
  // There used to be one renderer load, so one stage name was enough. There are two now -- the
  // loading page and the application page -- and both are reported as 'renderer-load', so
  // startup-error.json cannot tell "the loading screen never appeared" from "the app never
  // replaced it". Those are different faults with different first suspects: the first is the
  // window and a local scriptless page, the second is the built renderer bundle.
  //
  // The stage list is asserted at runtime rather than as a type, so that a name can be checked at
  // all: `StartupStage` is erased before anything can look at it.
  it('carries a stage for the loading page that is not the stage for the application page', () => {
    expect(STARTUP_STAGES, 'startup-diagnostic.ts exports no runtime list of startup stages').toBeInstanceOf(Array);
    expect(STARTUP_STAGES, 'the loading page has no stage of its own').toContain('loading-page-load');
    expect(STARTUP_STAGES, 'the application page load lost its stage').toContain('renderer-load');
  });

  it('uses each stage at the load it names', () => {
    // Without this the union can grow a member nobody passes, and the file keeps saying
    // 'renderer-load' for both screens while the test above stays green.
    //
    // Honest limit: source text. index.ts builds its window through Electron and cannot be run
    // here, so what is proved is which literal sits at which call, not what gets written when a
    // load actually fails.
    const mainSource = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');
    const loadingPage = /loadLoadingPage\s*:\s*async[\s\S]*?\n {4}\}/.exec(mainSource)?.[0] ?? '';
    const applicationPage = /loadApplicationPage\s*:\s*async[^\n]*/.exec(mainSource)?.[0] ?? '';
    expect(loadingPage, 'could not locate loadLoadingPage in src/main/index.ts').not.toBe('');
    expect(applicationPage, 'could not locate loadApplicationPage in src/main/index.ts').not.toBe('');
    expect(loadingPage, 'the loading page still reports itself as a plain renderer load').toMatch(/startupStage\('loading-page-load'/);
    expect(applicationPage, 'the application page no longer reports the renderer load stage').toMatch(/startupStage\('renderer-load'/);
  });
});
