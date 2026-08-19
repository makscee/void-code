import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import type { ChildProcessWithoutNullStreams } from 'node:child_process';
import { describe, expect, it } from 'vitest';
import { authEvent, DesktopAuthController } from '../src/main/desktop-auth';

function child() {
  const process = new EventEmitter() as ChildProcessWithoutNullStreams & { killed: boolean };
  process.stdout = new PassThrough(); process.stderr = new PassThrough(); process.stdin = new PassThrough(); process.killed = false;
  process.kill = (() => { process.killed = true; process.emit('close', null); return true; }) as typeof process.kill;
  return process;
}
const runtime = { vc: 'C:\\private\\vc.exe', node: '', piEntry: '', fixture: '', manifest: {} } as never;

describe('private desktop authorization protocol', () => {
  it('accepts only value-free allowlisted events', () => {
    expect(authEvent('{"type":"status","state":"ready"}')).toEqual({ type: 'status', state: 'ready' });
    expect(authEvent('{"type":"authorization","verificationUrl":"https://auth.example/device","userCode":"ABCD-EFGH","expiresIn":600}')).toMatchObject({ type: 'authorization', userCode: 'ABCD-EFGH' });
    expect(() => authEvent('{"type":"complete","state":"ready","token":"secret"}')).toThrow('protocol rejected');
    expect(() => authEvent('{"type":"authorization","verificationUrl":"file:///tmp/x","userCode":"CODE","expiresIn":600}')).toThrow('protocol rejected');
  });

  it('checks readiness through bundled vc without a shell', async () => {
    const owned = child(); let invocation: unknown;
    const controller = new DesktopAuthController(runtime, ((file, args, options) => { invocation = { file, args, options }; return owned; }) as never);
    const result = controller.status();
    owned.stdout.write('{"type":"status","state":"sign_in_required"}\n'); owned.emit('close', 0);
    await expect(result).resolves.toBe('sign_in_required');
    expect(invocation).toEqual({ file: 'C:\\private\\vc.exe', args: ['desktop-auth', 'status'], options: { windowsHide: true, shell: false, stdio: ['ignore', 'pipe', 'pipe'] } });
  });

  it('streams browser authorization and completes without exposing credentials', async () => {
    const owned = child(); const events: unknown[] = [];
    const controller = new DesktopAuthController(runtime, (() => owned) as never);
    const result = controller.start((event) => events.push(event));
    owned.stdout.write('{"type":"authorization","verificationUrl":"https://auth.example/device","userCode":"ABCD-EFGH","expiresIn":600}\n');
    owned.stdout.write('{"type":"complete","state":"ready"}\n'); owned.emit('close', 0);
    await expect(result).resolves.toBe('ready');
    expect(events).toHaveLength(2); expect(JSON.stringify(events)).not.toContain('token');
  });
});
