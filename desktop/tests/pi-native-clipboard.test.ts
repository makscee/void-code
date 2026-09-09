import { EventEmitter } from 'node:events';
import { spawn as nodeSpawn, type ChildProcess } from 'node:child_process';
import { PassThrough } from 'node:stream';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { extension, flush, localEnv, type Spawn } from './fixtures/pi-fullscreen-clipboard';

class Child extends EventEmitter {
  stdin = new PassThrough(); stdout = new PassThrough(); stderr = new PassThrough();
  bytes: Buffer[] = [];
  kill = vi.fn(() => true);
  constructor() { super(); this.stdin.on('data', (data: Buffer) => this.bytes.push(Buffer.from(data))); }
  close(code = 0): void { this.emit('exit', code, null); this.emit('close', code, null); }
  text(): string { return Buffer.concat(this.bytes).toString('utf8'); }
}
async function writer(platform = 'darwin') {
  const module = await extension();
  expect(module.createNativeClipboardWriter, 'R4/R5: managed transport lacks bounded stdin-only native writer').toBeTypeOf('function');
  const children: Child[] = [];
  const spawn = vi.fn<Spawn>(() => { const child = new Child(); children.push(child); return child; });
  const write = module.createNativeClipboardWriter!({ platform, env: localEnv, spawn });
  return { write, spawn, children };
}
beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

it.each(['darwin', 'win32'])('R5: %s plan uses absolute system executable, static code, explicit UTF-8 stdin and no payload channel other than stdin', async (platform) => {
  const r = await writer(platform);
  const payload = 'Привет 世界 😀\n"\' ; $(touch pwn) `whoami`\n[Environment]::Exit(99)';
  const first = r.write(payload); await flush();
  expect(r.spawn).toHaveBeenCalledTimes(1);
  const [file, args, options] = r.spawn.mock.calls[0];
  expect(file).toBe(platform === 'darwin' ? '/usr/bin/pbcopy' : 'C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe');
  expect(options.shell ?? false).toBe(false);
  expect(options.stdio).toEqual(['pipe', 'ignore', 'pipe']);
  expect(JSON.stringify([file, args, options])).not.toContain('Привет');
  expect(r.children[0].text()).toBe(payload);
  if (platform === 'win32') {
    expect(args.map((arg) => arg.toLowerCase())).toEqual(expect.arrayContaining(['-noprofile', '-noninteractive', '-sta']));
    // This is a process-plan assertion, not production source matching.
    const script = args.includes('-EncodedCommand') ? Buffer.from(args[args.indexOf('-EncodedCommand') + 1], 'base64').toString('utf16le') : args.at(-1)!;
    expect(script).toMatch(/UTF8|UTF-8|65001/i);
    expect(script).toMatch(/System\.Windows\.Forms/);
  }
  r.children[0].close(); await first;
  const next = r.write('distinct-marker'); await flush();
  expect(r.spawn.mock.calls[1]).toEqual(r.spawn.mock.calls[0]);
  r.children[1].close(); await next;
});

it.each(['darwin', 'win32'])('R3: %s exact 8MiB UTF-8 accepted intact; one byte over and NUL rejected before spawning with healthy recovery', async (platform) => {
  const r = await writer(platform); const limit = 'я'.repeat(4 * 1024 * 1024);
  const valid = r.write(limit); await flush(); expect(r.children[0].text()).toBe(limit);
  r.children[0].close(); await valid;
  await expect(r.write(limit + 'x')).rejects.toThrow();
  await expect(r.write('prefix\0suffix')).rejects.toThrow();
  expect(r.spawn).toHaveBeenCalledTimes(1);
  const recovery = r.write('healthy'); await flush(); expect(r.children[1].text()).toBe('healthy');
  r.children[1].close(); await recovery;
});

it.each(['darwin', 'win32'])('R4: %s 4999ms remains alive; 5000ms kills, waits for close and only then admits next write', async (platform) => {
  const r = await writer(platform);
  let outcome = 'pending'; const a = r.write('slow').then(() => { outcome = 'success'; }, () => { outcome = 'failure'; });
  await flush(); const b = r.write('healthy'); await flush();
  expect(r.spawn).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(4999); expect(r.children[0].kill).not.toHaveBeenCalled(); expect(outcome).toBe('pending');
  await vi.advanceTimersByTimeAsync(1); expect(r.children[0].kill).toHaveBeenCalled();
  expect(r.spawn).toHaveBeenCalledTimes(1); expect(outcome).not.toBe('success');
  r.children[0].close(); await a; await flush(); expect(outcome).toBe('failure');
  expect(r.spawn).toHaveBeenCalledTimes(2); expect(r.children[1].text()).toBe('healthy');
  r.children[1].close(); await b;
  await vi.advanceTimersByTimeAsync(5000); expect(r.children[1].kill).not.toHaveBeenCalled();
});

it.each(['spawn', 'spawn-event', 'stdin', 'exit'])('R4/R5: %s failure cannot leak text or leave the following healthy operation blocked', async (failure) => {
  const r = await writer();
  if (failure === 'spawn') r.spawn.mockImplementationOnce(() => { throw new Error('SECRET-selection'); });
  const result = r.write('SECRET-selection').then(() => 'unexpected success', (error: Error) => error.message);
  await flush();
  if (failure === 'spawn-event') { r.children[0].emit('error', new Error('SECRET-selection')); r.children[0].close(-1); }
  if (failure === 'stdin') { r.children[0].stdin.emit('error', new Error('SECRET-selection')); r.children[0].close(1); }
  if (failure === 'exit') { r.children[0].stderr.write('SECRET-selection'); r.children[0].close(1); }
  const error = await result; expect(error).not.toBe('unexpected success'); expect(error).not.toContain('SECRET-selection');
  const healthy = r.write('healthy'); await flush(); const child = r.children.at(-1)!;
  expect(child.text()).toBe('healthy'); child.close(); await healthy;
});

it.each(['ENOENT', 'EACCES'])('R4/R5: asynchronous child %s error then close is handled, generic, and releases a queued successor', async (code) => {
  const r = await writer();
  let outcome: unknown = 'pending';
  const failed = r.write('SECRET-selection').then(() => { outcome = 'success'; }, (error) => { outcome = error; });
  await flush();
  const healthy = r.write('healthy'); await flush();
  // Native spawn returns a ChildProcess first; error arrives later, with no exit event on ENOENT.
  const error = Object.assign(new Error('SECRET-selection'), { code, syscall: 'spawn' });
  await Promise.resolve();
  expect(() => r.children[0].emit('error', error)).not.toThrow();
  await flush(); expect(r.spawn).toHaveBeenCalledTimes(1);
  expect(outcome).not.toBe('success');
  r.children[0].emit('close', -2, null);
  await failed; await flush();
  expect(outcome).toBeInstanceOf(Error);
  expect((outcome as Error).message).toBeTruthy();
  expect(String(outcome)).not.toContain('SECRET-selection');
  expect(r.spawn).toHaveBeenCalledTimes(2); expect(r.children[1].text()).toBe('healthy');
  r.children[1].close(); await healthy;
  await vi.advanceTimersByTimeAsync(5000);
  expect(r.children[1].kill).not.toHaveBeenCalled();
});

it('R4: cancellation while B waits behind A never spawns B after close; C survives', async () => {
  const r = await writer(); const controller = new AbortController();
  const a = r.write('A'); await flush();
  const b = r.write('SECRET-waiting-B', controller.signal).then(() => 'unexpected success', (error) => error);
  // Abort before any queued microtask can attach a native child listener.
  controller.abort(new Error('SECRET-abort-reason'));
  const c = r.write('C'); await flush();
  expect(r.spawn).toHaveBeenCalledTimes(1);
  expect(r.children[0].kill).not.toHaveBeenCalled();
  r.children[0].close(); await a; await flush();
  expect(r.spawn).toHaveBeenCalledTimes(2);
  expect(r.children.map((child) => child.text())).toEqual(['A', 'C']);
  r.children[1].close(); await c;
  const error = await b;
  expect(error).toBeInstanceOf(Error); expect(error.message).toBeTruthy();
  expect(String(error)).not.toContain('SECRET');
  await flush(); expect(r.spawn).toHaveBeenCalledTimes(2);
});

it('R4: real isolated Node child drains oversized stderr before successful exit', async () => {
  vi.useRealTimers();
  const module = await extension();
  expect(module.createNativeClipboardWriter, 'R4/R5: managed transport lacks bounded stdin-only native writer').toBeTypeOf('function');
  let child: ChildProcess | undefined;
  let closed: Promise<void> | undefined;
  let deadline: ReturnType<typeof setTimeout> | undefined;
  const spawn = vi.fn<Spawn>((_file, _args, options) => {
    expect(options.stdio).toEqual(['pipe', 'ignore', 'pipe']);
    // Never launch the requested clipboard executable. No parent stderr reader:
    // only the production writer may drain it (or arrange a nonblocking sink).
    child = nodeSpawn(process.execPath, ['-e', `
      const chunks = [];
      process.stdin.on('data', chunk => chunks.push(chunk));
      process.stdin.on('end', () => {
        if (Buffer.concat(chunks).toString('utf8') !== 'isolated Unicode 世界') process.exit(7);
        process.stderr.write(Buffer.alloc(4 * 1024 * 1024, 120), () => process.exit(0));
      });
    `], { stdio: options.stdio, shell: false, env: {} });
    closed = new Promise((resolve) => child!.once('close', () => resolve()));
    return child;
  });
  const write = module.createNativeClipboardWriter!({ platform: 'darwin', env: localEnv, spawn });
  try {
    await Promise.race([
      write('isolated Unicode 世界'),
      new Promise<never>((_, reject) => { deadline = setTimeout(() => reject(new Error('stderr drain deadline exceeded')), 3500); }),
    ]);
    expect(spawn).toHaveBeenCalledTimes(1);
    expect(child!.exitCode).toBe(0);
  } finally {
    clearTimeout(deadline);
    if (child && child.exitCode === null && child.signalCode === null) child.kill('SIGKILL');
    await closed;
  }
}, 5000);

it('R4: cancellation kills active child, waits for close and rejects even late zero exit; pre-aborted request never spawns', async () => {
  const r = await writer(); const controller = new AbortController();
  const result = r.write('cancel-me', controller.signal).then(() => 'success', () => 'cancelled');
  await flush(); expect(r.spawn).toHaveBeenCalledTimes(1);
  controller.abort(); await flush(); expect(r.children[0].kill).toHaveBeenCalled();
  r.children[0].close(); expect(await result).toBe('cancelled');
  await expect(r.write('never', controller.signal)).rejects.toThrow(); expect(r.spawn).toHaveBeenCalledTimes(1);
  const healthy = r.write('healthy'); await flush(); r.children[1].close(); await healthy;
});
