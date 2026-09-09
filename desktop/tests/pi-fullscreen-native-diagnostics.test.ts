// Fake-only control for the frozen acceptance stderr repair. No Pi imports or clipboard IO.
import { mkdtempSync, rmSync, writeFileSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
import { runInNewContext } from 'node:vm';
import { transformSync } from 'esbuild';
import { expect, it } from 'vitest';
import { EventEmitter, errorMonitor } from 'node:events';

// Execute only the actual fixture observer and finally block with harmless fakes.
// No Pi/module loading, native loop, clipboard reader, or native process spawning.
const probe = readFileSync(path.resolve('tests/fixtures/pi-fullscreen-native-probe.ts'), 'utf8');
const observerStart = probe.indexOf('  const originalSpawn = childProcess.spawn;');
const observerEnd = probe.indexOf('  try {\n    syncBuiltinESMExports();', observerStart);
const cleanupStart = probe.lastIndexOf('\n  } finally {') + 1;
expect(observerStart).toBeGreaterThan(0);
expect(observerEnd).toBeGreaterThan(observerStart);
expect(cleanupStart).toBeGreaterThan(observerEnd);
const observerControl = transformSync(`(async () => {
${probe.slice(observerStart, observerEnd)}
  try { exercise();
${probe.slice(cleanupStart)}
)()`, { loader: 'ts', target: 'node22' }).code;

it.each([false, true])('observer buffers fake child events until cleanup/restoration (cleanup throws=%s)', async (cleanupThrows) => {
  const child = Object.assign(new EventEmitter(), { stdin: new EventEmitter() });
  const writes: string[] = [];
  const stages: string[] = [];
  const args = ['fake-helper', ['PRIVATE_ARG'], { env: { PRIVATE_ENV: 'secret' } }];
  const originalSpawn = function (this: unknown, ...actual: unknown[]) {
    expect(this).toBe(builtins);
    expect(actual).toEqual(args);
    actual.forEach((value, index) => expect(value).toBe(args[index]));
    return child;
  };
  const builtins = { spawn: originalSpawn };
  const cleanupError = new Error('synthetic cleanup failure');
  const result = runInNewContext(observerControl, {
    childProcess: builtins, path, performance, errorMonitor,
    process: { stderr: { write: (line: string) => {
      expect(stages).toEqual(['shutdown', 'widgets', 'stop', 'restored']);
      expect(builtins.spawn).toBe(originalSpawn);
      writes.push(line);
    } } },
    exercise: () => {
      expect(builtins.spawn(...args)).toBe(child);
      expect(writes).toEqual([]);
      for (const [emitter, event, values] of [
        [child, 'spawn', []], [child.stdin, 'finish', []],
        [child, 'exit', [0, null]], [child, 'close', [0, null]],
      ] as const) {
        emitter.emit(event, ...values);
        expect(writes).toEqual([]);
      }
      for (const emitter of [child, child.stdin]) {
        const error = Object.assign(new Error('PRIVATE_PAYLOAD'), { code: 'EPIPE' });
        expect(emitter.listenerCount('error')).toBe(0);
        expect(() => emitter.emit('error', error)).toThrow(error);
        expect(writes).toEqual([]);
      }
      // Excess synthetic events cannot grow the diagnostic output without bound.
      for (let i = 0; i < 200; i++) child.emit('spawn');
      expect(writes).toEqual([]);
      expect(child.eventNames()).not.toContain('data');
    },
    handlers: new Map([['session_shutdown', [async () => { expect(writes).toEqual([]); stages.push('shutdown'); }]]]),
    ctx: {}, receiver: {},
    consumer: { clearExtensionWidgets: () => { expect(writes).toEqual([]); stages.push('widgets'); } },
    tui: { stop: () => {
      expect(writes).toEqual([]); stages.push('stop');
      if (cleanupThrows) throw cleanupError;
    } },
    syncBuiltinESMExports: () => { expect(builtins.spawn).toBe(originalSpawn); stages.push('restored'); },
  });
  if (cleanupThrows) await expect(result).rejects.toBe(cleanupError);
  else await result;
  expect(writes).toHaveLength(128);
  const records = writes.map((line) => JSON.parse(line));
  expect(records.slice(0, 7).map((record) => record.event)).toEqual(['call', 'spawn', 'stdin-finish', 'exit', 'close', 'error', 'stdin-error']);
  for (const record of records) {
    expect(Object.keys(record)).toEqual(['executable', 'operation', 'elapsedMs', 'event', 'code', 'signal']);
    expect(record).toMatchObject({ executable: 'fake-helper', operation: 1 });
  }
  expect(records[5].code).toBe('EPIPE');
  expect(writes.join('')).not.toMatch(/PRIVATE|secret/);
});

const require = createRequire(import.meta.url);
const acceptance = transformSync(readFileSync(path.resolve('tests/pi-fullscreen-native-acceptance.test.ts'), 'utf8'), {
  loader: 'ts', format: 'cjs', target: 'node22',
}).code;

it.each([0, 7])('fake Node exit %i retains stderr when native marker is missing', (status) => {
  const work = mkdtempSync(path.join(tmpdir(), 'astra-native-diagnostic-'));
  try {
    // Explicit Node entry ignores all Pi arguments; the copied probe is NEVER loaded.
    const entry = path.join(work, 'fake-node-entry.cjs');
    writeFileSync(entry, `process.stderr.write('FAKE_NATIVE_FAILURE_ONLY\\n'); process.stdout.write('fake model table\\n'); process.exitCode = ${status};`);
    let acceptanceBody: (() => void) | undefined;
    runInNewContext(acceptance, {
      // Capture one test body, not a nested Vitest process/test suite.
      require: (id: string) => {
        if (id === 'vitest') return { expect, it: { skipIf: (skip: boolean) => {
          expect(skip).toBe(false);
          return (_name: string, body: () => void) => { acceptanceBody = body; };
        } } };
        if (id === './fixtures/pi-fullscreen-clipboard') return { embeddedSource: () => '// fake-only; never imported' };
        if (id.startsWith('node:')) return require(id);
        throw new Error('Unexpected acceptance dependency');
      },
      process: {
        execPath: process.execPath, platform: 'win32',
        env: {
          PATH: process.env.PATH, SystemRoot: process.env.SystemRoot, WINDIR: process.env.WINDIR,
          VC_NATIVE_PI_ENTRY: entry, VC_NATIVE_PI_PACKAGE_DIR: work,
          VC_ISOLATED_CLIPBOARD_ACCEPTANCE: 'I_OWN_THIS_ISOLATED_CLIPBOARD_SESSION',
        },
      },
    });
    expect(acceptanceBody).toBeTypeOf('function');
    let failure: unknown;
    try { acceptanceBody!(); } catch (error) { failure = error; }
    expect(failure).toBeDefined();
    const message = String(failure);
    expect(message).toContain(`Pi status=${status}`);
    expect(message).toContain('stderr:\nFAKE_NATIVE_FAILURE_ONLY');
    expect(message).toContain('stdout:\nfake model table');
    if (status === 0) expect(message).toContain('Required ASTRA_NATIVE_SELECTION_READBACK_OK_4');
  } finally { rmSync(work, { recursive: true, force: true }); }
});
