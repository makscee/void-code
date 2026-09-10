// Fake-only: no native executable, OS clipboard or Pi loader.
import { EventEmitter } from 'node:events';
import type { spawn } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { transformSync } from 'esbuild';
import { expect, it, vi } from 'vitest';
import { nativeWitness, preserveNativeFailure } from './fixtures/pi-fullscreen-native-witness';

const source = readFileSync('../cmd/vc/pi_extension.go', 'utf8');
const script = source.match(/args = \["-NoProfile", "-NonInteractive", "-Sta", "-Command", "(.*?)"\];/)![1];
function fake() {
  const stream = () => Object.assign(new EventEmitter(), { destroy: vi.fn(), end: vi.fn() });
  const child = Object.assign(new EventEmitter(), { stdin: stream(), stdout: stream(), stderr: stream(), kill: vi.fn() });
  const launch = vi.fn((...args: unknown[]) => { expect(args).toHaveLength(3); return child; });
  return { child, launch, spawn: launch as unknown as typeof spawn };
}
const directLoad = "[void][Reflection.Assembly]::Load('System.Windows.Forms, Version=4.0.0.0, Culture=neutral, PublicKeyToken=b77a5c561934e089')";
const legacyLoad = 'Add-Type -AssemblyName System.Windows.Forms';
const directScript = script.replace(legacyLoad, directLoad);
it.each([script, directScript])('uses original options and only synthetic first marker; accepts only whole fixed phase lines (%#)', async (script) => {
  const f = fake(); const output: object[] = [];
  const options = { windowsHide: true, stdio: ['pipe', 'ignore', 'pipe'] };
  const args = ['powershell.exe', ['-NoProfile', '-NonInteractive', '-Sta', '-Command', script], options] as Parameters<typeof spawn>;
  const marker = '{\\rtf1\\ansi literal Привет 世界 😀}';
  const result = nativeWitness(f.spawn, args, marker, (record) => output.push(record));
  expect(f.launch).toHaveBeenCalledOnce();
  expect(f.launch.mock.calls[0]).toEqual([args[0], expect.any(Array), options]);
  expect((f.launch.mock.calls[0] as unknown[])[2]).toBe(options);
  expect(args[1]![4]).toBe(script);
  const instrumented = (f.launch.mock.calls[0][1] as string[])[4];
  expect(instrumented.match(/ASTRA_PHASE_/g)).toHaveLength(6);
  const loader = script.includes(legacyLoad) ? legacyLoad : directLoad;
  expect(instrumented).toContain(`[Console]::Error.WriteLine('ASTRA_PHASE_FORMS_BEFORE'); ${loader}; [Console]::Error.WriteLine('ASTRA_PHASE_FORMS_AFTER')`);
  expect(instrumented.replace(/\[Console\]::Error.WriteLine\('ASTRA_PHASE_[A-Z_]+'\); /g, '').replace(/; \[Console\]::Error.WriteLine\('ASTRA_PHASE_[A-Z_]+'\)/g, '')).toBe(script);
  expect(f.child.stdin.end).toHaveBeenCalledWith(marker, 'utf8');
  for (const chunk of ['PRIVATE\nASTRA_PHASE_STDIN_', 'BEFORE\r\n', 'xASTRA_PHASE_FORMS_AFTER\n', 'x'.repeat(1000), 'ASTRA_PHASE_SETTEXT_AFTER\n', 'ASTRA_PHASE_STDIN_AFTER\n', 'ASTRA_PHASE_STDIN_AFTER\n']) f.child.stderr.emit('data', Buffer.from(chunk));
  f.child.emit('error', Object.assign(new Error('PRIVATE'), { code: 'EPIPE' }));
  f.child.emit('exit', 0, null); f.child.emit('close', 0, null);
  await result;
  expect(output.map((r) => (r as { event: string }).event)).toEqual(['ASTRA_PHASE_STDIN_BEFORE', 'ASTRA_PHASE_STDIN_AFTER', 'error', 'exit', 'close']);
  expect(JSON.stringify(output)).not.toMatch(/PRIVATE|literal|powershell|windowsHide/);
  for (const stream of [f.child.stdin, f.child.stdout, f.child.stderr]) expect(stream.destroy).toHaveBeenCalledOnce();
  expect(f.child.kill).not.toHaveBeenCalled();
});
it.each([
  directScript.replace(directLoad, ''),
  directScript.replace(directLoad, `${directLoad}; ${directLoad}`),
  directScript.replace(directLoad, `${directLoad}; ${legacyLoad}`),
  directScript.replace('b77a5c561934e089', '0000000000000000'),
])('refuses missing, duplicate, ambiguous or wrong-identity Forms boundaries (%#)', async (script) => {
  const f = fake();
  await expect(nativeWitness(f.spawn, ['powershell.exe', ['-Command', script], {}], 'synthetic', () => {})).rejects.toThrow('WITNESS_SCRIPT_REFUSED');
  expect(f.launch).not.toHaveBeenCalled();
});
it.each([true, false])('bounded owned child cleanup (close delivered=%s)', async (close) => {
  vi.useFakeTimers();
  try {
    const f = fake();
    if (close) f.child.kill.mockImplementation(() => { f.child.emit('close', null, 'SIGKILL'); });
    const result = nativeWitness(f.spawn, ['powershell.exe', ['-Command', script], {}], 'synthetic', () => {});
    await vi.advanceTimersByTimeAsync(5000); await result;
    expect(f.child.kill).toHaveBeenCalledWith('SIGKILL');
    expect(f.child.stderr.destroy).toHaveBeenCalledOnce();
    expect(vi.getTimerCount()).toBe(0);
  } finally { vi.useRealTimers(); }
});
it.each([false, true])('original failure identity survives diagnostic (throws=%s)', async (throws) => {
  const original = new Error('original');
  const diagnostic = vi.fn(async () => { if (throws) throw new Error('supplemental'); });
  await expect(preserveNativeFailure(original, diagnostic)).rejects.toBe(original);
  expect(diagnostic).toHaveBeenCalledOnce();
});
it.each([
  { fail: false, platform: 'win32', verified: true, calls: 0 },
  { fail: true, platform: 'win32', verified: true, calls: 1 },
  { fail: true, platform: 'darwin', verified: true, calls: 0 },
  { fail: true, platform: 'win32', verified: false, calls: 0 },
])('executes actual failure branch with fake witness: %j', async ({ fail, platform, verified, calls }) => {
  const probe = readFileSync('tests/fixtures/pi-fullscreen-native-probe.ts', 'utf8');
  const start = probe.indexOf("    console.log('ASTRA_NATIVE_SELECTION_READBACK_OK_4');");
  const branch = probe.slice(start, probe.indexOf('\n  } finally {', start));
  const original = new Error('original');
  const witness = vi.fn(async () => {});
  const success = vi.fn(); const write = vi.fn();
  const code = transformSync(`(async () => { let acceptanceFailed = false; let acceptanceError; try { if (fail) throw original; ${branch}\n } })()`, { loader: 'ts' }).code;
  const result = runInNewContext(code, {
    fail, original, console: { log: success }, process: { platform, env: { VC_R8_PRIVATE_LAUNCHER: verified ? 'VERIFIED' : undefined }, stderr: { write } },
    failureStage: 'native-completion', failureIndex: 0, firstNativeArgs: [], originalSpawn: () => {}, markers: ['synthetic'],
    nativeWitness: witness, preserveNativeFailure,
  });
  if (fail) await expect(result).rejects.toBe(original); else await result;
  expect(witness).toHaveBeenCalledTimes(calls);
  expect(success).toHaveBeenCalledTimes(fail ? 0 : 1);
  if (fail) expect(JSON.parse(write.mock.calls[0][0])).toEqual({ event: 'acceptance-failure', stage: 'native-completion', index: 0 });
});
it('fixture runs witness only in catch, Windows verified launcher, never substitutes success or readbacks', () => {
  const probe = readFileSync('tests/fixtures/pi-fullscreen-native-probe.ts', 'utf8');
  const start = probe.indexOf("    console.log('ASTRA_NATIVE_SELECTION_READBACK_OK_4');");
  const branch = probe.slice(start, probe.indexOf('\n  } finally {', start));
  expect(branch).toContain('} catch (error) {');
  expect(branch).toContain("process.platform === 'win32' && process.env.VC_R8_PRIVATE_LAUNCHER === 'VERIFIED' && firstNativeArgs");
  expect(branch).toContain('preserveNativeFailure(error');
  expect(branch).toContain('nativeWitness(originalSpawn, firstNativeArgs, markers[0]');
  expect(probe.match(/await nativeWitness\(/g)).toHaveLength(1);
  expect(probe).toContain('for (const [index, marker] of markers.entries())');
  expect(probe).toContain("assert.ok(readback === marker,");
});
