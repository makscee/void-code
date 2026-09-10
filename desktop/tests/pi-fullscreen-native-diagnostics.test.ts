// Fake-only control for the frozen acceptance stderr repair. No Pi imports or clipboard IO.
import { mkdtempSync, mkdirSync, rmSync, writeFileSync, readFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
import { runInNewContext } from 'node:vm';
import { transformSync } from 'esbuild';
import { expect, it } from 'vitest';
import { EventEmitter, errorMonitor } from 'node:events';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import type { NativeRequest } from './fixtures/pi-fullscreen-private-clipboard';

// Execute only the actual fixture observer and finally block with harmless fakes.
// No Pi/module loading, native loop, clipboard reader, or native process spawning.
const probe = readFileSync(path.resolve('tests/fixtures/pi-fullscreen-native-probe.ts'), 'utf8');
it.each(['ok', 'mismatch', 'throw'])('independent Windows reader uses direct CLR load and private UTF-8 comparison: %s', (mode) => {
  const start = probe.indexOf('      let readback: string;');
  const end = probe.indexOf('\n    }\n    console.log', start);
  const code = transformSync(probe.slice(start, end), { loader: 'ts' }).code;
  const marker = 'synthetic Привет 世界 😀';
  let calls = 0;
  const run = () => runInNewContext(code, {
    process: { platform: 'win32', env: { SystemRoot: 'C:\\Windows' } }, path, Buffer, assert, marker,
    execFileSync: (file: string, args: string[], options: object) => {
      calls++;
      expect(file).toBe('C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe');
      expect(args.slice(0, 4)).toEqual(['-NoProfile', '-NonInteractive', '-Sta', '-Command']);
      expect(args[4]).toBe("$ErrorActionPreference='Stop'; [void][Reflection.Assembly]::Load('System.Windows.Forms, Version=4.0.0.0, Culture=neutral, PublicKeyToken=b77a5c561934e089'); [Console]::Write([Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes([Windows.Forms.Clipboard]::GetText())))");
      expect(options).toEqual({ encoding: 'utf8', timeout: 5000 });
      if (mode === 'throw') throw new Error('PRIVATE stdout/stderr');
      return Buffer.from(mode === 'ok' ? marker : 'PRIVATE mismatch', 'utf8').toString('base64');
    },
  });
  if (mode === 'ok') run();
  else expect(run).toThrow(mode === 'throw' ? 'Independent plain-text clipboard read failed' : 'OS plain-text clipboard did not exactly match complete selection');
  expect(calls).toBe(1);
});
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

it.each([
  { status: 0, marker: false, setupFailure: false },
  { status: 7, marker: false, setupFailure: false },
  { status: 0, marker: true, setupFailure: false },
  { status: 91, marker: true, setupFailure: false }, // cleanup failure must defeat a success marker
  { status: 0, marker: true, setupFailure: true },
])('fake acceptance fails closed or passes only with exit zero + marker: %j', ({ status, marker, setupFailure }) => {
  const work = mkdtempSync(path.join(tmpdir(), 'astra-native-diagnostic-'));
  try {
    // Explicit Node entry ignores all Pi arguments; the copied probe is NEVER loaded.
    mkdirSync(path.join(work, 'dist'));
    const entry = path.join(work, 'dist/cli.js');
    writeFileSync(entry, `process.stderr.write('FAKE_NATIVE_FAILURE_ONLY\\n'); process.stdout.write('fake model table\\n${marker ? 'ASTRA_NATIVE_SELECTION_READBACK_OK_4' : ''}'); process.exitCode = ${status};`);
    let acceptanceBody: (() => void) | undefined;
    let ownedWork: string | undefined;
    runInNewContext(acceptance, {
      // Capture one test body, not a nested Vitest process/test suite.
      require: (id: string) => {
        if (id === 'vitest') return { expect, it: { skipIf: (skip: boolean) => {
          expect(skip).toBe(false);
          return (_name: string, body: () => void) => { acceptanceBody = body; };
        } } };
        if (id === './fixtures/pi-fullscreen-clipboard') return { embeddedSource: () => '// fake-only; never imported' };
        // Diagnostic runner only: the fake entry never loads either probe or hook.
        // Actual factory/provenance behavior has separate consumer controls.
        if (id === './fixtures/pi-interactive-consumer') return { consumerHooks: (file: string) => {
          expect(file).toBe(path.join(work, 'dist/modes/interactive/interactive-mode.js'));
          return { file, sha256: 'fake-not-executed', factory: '', name: '', methods: new Map() };
        } };
        if (id === './fixtures/pi-fullscreen-private-clipboard') return {
          runPrivateWindowsClipboard: (request: NativeRequest) => {
            // Never import/run the Windows launcher, even on Windows. Only this fake entry runs.
            ownedWork = request.work;
            if (setupFailure) throw new Error('PRIVATE_CLIPBOARD_COMPILE_FAILED');
            expect(request.node).toBe(process.execPath);
            expect(request.args).toEqual([entry, '--offline', '--no-extensions', '--no-skills', '--no-prompt-templates', '--no-themes', '--no-context-files', '-e', path.join(request.work, 'probe.ts'), '--list-models']);
            expect(request.env.PI_PACKAGE_DIR).toBe(work);
            expect(request.env).not.toHaveProperty('LANG');
            expect(request.env).not.toHaveProperty('PRIVATE_PARENT_SECRET');
            expect(request.env).toMatchObject({ LC_ALL: 'C', PI_OFFLINE: '1', TEMP: request.work });
            return spawnSync(request.node, request.args, {
              cwd: request.work, env: request.env, encoding: 'utf8', timeout: 5000,
              maxBuffer: 2 * 1024 * 1024, stdio: ['ignore', 'pipe', 'pipe'],
            });
          },
        };
        if (id.startsWith('node:')) return require(id);
        throw new Error('Unexpected acceptance dependency');
      },
      process: {
        execPath: process.execPath, platform: 'win32',
        env: {
          PATH: process.env.PATH, SystemRoot: process.env.SystemRoot, WINDIR: process.env.WINDIR,
          VC_NATIVE_PI_ENTRY: entry, VC_NATIVE_PI_PACKAGE_DIR: work,
          PRIVATE_PARENT_SECRET: 'must-not-be-inherited',
          VC_ISOLATED_CLIPBOARD_ACCEPTANCE: 'I_OWN_THIS_ISOLATED_CLIPBOARD_SESSION',
        },
      },
    });
    expect(acceptanceBody).toBeTypeOf('function');
    let failure: unknown;
    try { acceptanceBody!(); } catch (error) { failure = error; }
    expect(ownedWork).toBeDefined();
    expect(existsSync(ownedWork!)).toBe(false);
    if (setupFailure) {
      expect(String(failure)).toContain('PRIVATE_CLIPBOARD_COMPILE_FAILED');
      return;
    }
    if (status === 0 && marker) { expect(failure).toBeUndefined(); return; }
    expect(failure).toBeDefined();
    const message = String(failure);
    expect(message).toContain(`Pi status=${status}`);
    expect(message).toContain('stderr:\nFAKE_NATIVE_FAILURE_ONLY');
    expect(message).toContain('stdout:\nfake model table');
    if (status === 0) expect(message).toContain('Required ASTRA_NATIVE_SELECTION_READBACK_OK_4');
  } finally { rmSync(work, { recursive: true, force: true }); }
});
