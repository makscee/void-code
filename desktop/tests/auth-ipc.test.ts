import { EventEmitter } from 'node:events';
import { describe, expect, it, vi } from 'vitest';
import { IPC } from '../src/shared/preload-contract';
import type { AuthChildProcess, AuthSpawner } from '../src/main/auth-session';
import type { AuthLoginPush } from '../src/main/auth-ipc';
import { startAuthLogin } from '../src/main/auth-ipc';

// Minimal double for the child_process shape auth-session actually reads (see auth-session.test.ts).
class FakeChild extends EventEmitter implements AuthChildProcess {
  readonly stdout = new EventEmitter();
  readonly stderr = new EventEmitter();
  end(code: number | null, signal: string | null = null): void { this.emit('exit', code, signal); }
}
function fixedSpawner(child: FakeChild): AuthSpawner { return () => child; }
function throwingSpawner(message: string): AuthSpawner {
  return () => { throw new Error(message); };
}
const LOGIN_ID = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa';

describe('IPC channel wiring for auth', () => {
  it('names the request channels used by auth:status and auth:login-start', () => {
    expect(IPC.authStatus).toBe('auth:status');
    expect(IPC.authLoginStart).toBe('auth:login-start');
  });

  // The progress/result channel is push-only (main -> renderer, no renderer input ever arrives
  // on it), exactly like terminal:output and chat:lifecycle — and, like those, it lives in the
  // IPC map as a normal member rather than off to the side. ipc-authority.test.ts is the one
  // place that must know it's exempt from the assertRenderer guard (it names it in that test's
  // exclusion list); this file just pins the value so a rename here is caught too.
  it('names the login push channel alongside the other channels in the IPC map', () => {
    expect(IPC.authLoginEvent).toBe('auth:login-event');
  });
});

describe('startAuthLogin', () => {
  it('delivers vc login events tagged with the loginId, and opens the verification URL', async () => {
    const child = new FakeChild();
    const delivered: AuthLoginPush[] = [];
    const openedUrls: string[] = [];
    startAuthLogin(LOGIN_ID, '/private/vc', fixedSpawner(child), (push) => delivered.push(push), (url) => openedUrls.push(url));

    child.stdout.emit('data', '{"event":"prompt","userCode":"MSKDWMSW","verificationUrl":"https://auth.makscee.ru/device","expiresInSeconds":600}\n');
    child.stdout.emit('data', '{"event":"authorized"}\n');
    child.end(0);
    await vi.waitFor(() => expect(delivered).toHaveLength(3));

    expect(delivered[0]).toEqual({ loginId: LOGIN_ID, event: 'prompt', userCode: 'MSKDWMSW', verificationUrl: 'https://auth.makscee.ru/device', expiresInSeconds: 600 });
    expect(delivered[1]).toEqual({ loginId: LOGIN_ID, event: 'authorized' });
    expect(delivered[2]).toEqual({ loginId: LOGIN_ID, event: 'closed', ok: true });
    expect(openedUrls).toEqual(['https://auth.makscee.ru/device']);
  });

  it('reports a login that exits without a terminal event as closed/not-ok, so a spinner cannot run forever', async () => {
    const child = new FakeChild();
    const delivered: AuthLoginPush[] = [];
    startAuthLogin(LOGIN_ID, '/private/vc', fixedSpawner(child), (push) => delivered.push(push), () => undefined);

    // vc's process dies mid-flow: no "authorized", no "error" event, just a non-zero exit.
    child.end(1);
    await vi.waitFor(() => expect(delivered).toHaveLength(1));
    expect(delivered[0]).toEqual({ loginId: LOGIN_ID, event: 'closed', ok: false, reason: 'exited_unexpectedly' });
  });

  it('reports a login that cannot be spawned at all as closed/not-ok instead of throwing into the caller', async () => {
    const delivered: AuthLoginPush[] = [];
    expect(() => startAuthLogin(LOGIN_ID, '/private/vc', throwingSpawner('ENOENT'), (push) => delivered.push(push), () => undefined)).not.toThrow();
    await vi.waitFor(() => expect(delivered).toHaveLength(1));
    expect(delivered[0]).toEqual({ loginId: LOGIN_ID, event: 'closed', ok: false, reason: 'spawn_failed' });
  });

  it('forwards vc\'s own login error event to the renderer as a closing failure, not a hardcoded success', async () => {
    const child = new FakeChild();
    const delivered: AuthLoginPush[] = [];
    startAuthLogin(LOGIN_ID, '/private/vc', fixedSpawner(child), (push) => delivered.push(push), () => undefined);

    child.stdout.emit('data', '{"event":"error","reason":"expired"}\n');
    child.end(1);
    await vi.waitFor(() => expect(delivered).toHaveLength(2));
    expect(delivered[0]).toEqual({ loginId: LOGIN_ID, event: 'error', reason: 'expired' });
    expect(delivered[1]).toEqual({ loginId: LOGIN_ID, event: 'closed', ok: false, reason: 'expired' });
  });

  it('never lets diagnostic text (malformed vc output, stderr) reach the renderer-facing deliver callback', async () => {
    const child = new FakeChild();
    const delivered: AuthLoginPush[] = [];
    const diagnostics: string[] = [];
    startAuthLogin(LOGIN_ID, '/private/vc', fixedSpawner(child), (push) => delivered.push(push), () => undefined, (message) => diagnostics.push(message));

    child.stdout.emit('data', 'not json at all\n');
    child.stderr.emit('data', 'pty size warning\n');
    child.end(0);
    await vi.waitFor(() => expect(delivered).toHaveLength(1));

    expect(delivered).toEqual([{ loginId: LOGIN_ID, event: 'closed', ok: true }]);
    expect(diagnostics.some((line) => line.includes('not json at all'))).toBe(true);
    expect(diagnostics.some((line) => line.includes('pty size warning'))).toBe(true);
    for (const push of delivered) expect(JSON.stringify(push)).not.toMatch(/not json at all|pty size warning/);
  });
});
