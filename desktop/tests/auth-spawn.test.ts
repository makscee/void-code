import { EventEmitter } from 'node:events';
import { describe, expect, it, vi } from 'vitest';

// spawnAuthProcess is the production AuthSpawner: the one thing standing between "sign in
// through the vc binary this app shipped" and "sign in through whatever `vc` happens to be
// first on PATH" — a different build, silently. It must hand the given path straight to
// node:child_process.spawn and never fall back to a bare command-name lookup.
const spawnMock = vi.hoisted(() => vi.fn());
vi.mock('node:child_process', () => ({ spawn: spawnMock }));

class FakeChild extends EventEmitter {
  readonly stdout = new EventEmitter();
  readonly stderr = new EventEmitter();
}

describe('spawnAuthProcess', () => {
  it('spawns exactly the given vcPath with exactly the given args', async () => {
    const { spawnAuthProcess } = await import('../src/main/auth-spawn');
    const fake = new FakeChild();
    spawnMock.mockReturnValueOnce(fake);

    const result = spawnAuthProcess('/private/private-runtime/vc', ['status', '--json']);

    expect(spawnMock).toHaveBeenCalledTimes(1);
    const [command, args] = spawnMock.mock.calls[0];
    expect(command).toBe('/private/private-runtime/vc');
    expect(args).toEqual(['status', '--json']);
    // The whole point of taking an explicit path is defeated if a shell is asked to resolve
    // the command by name — that reopens the PATH lookup this seam exists to close.
    const options = spawnMock.mock.calls[0][2] as Record<string, unknown> | undefined;
    expect(options?.shell).not.toBe(true);
    expect(result).toBe(fake);
  });

  it('never substitutes a bare "vc" for the path it was given', async () => {
    const { spawnAuthProcess } = await import('../src/main/auth-spawn');
    spawnMock.mockReturnValueOnce(new FakeChild());
    spawnAuthProcess('/opt/void-code/private-runtime/vc', ['login', '--json']);
    const [command] = spawnMock.mock.calls[0];
    expect(command).not.toBe('vc');
  });

  it('returns something whose stdout/stderr/exit wiring readAuthStatus can actually consume', async () => {
    const { spawnAuthProcess } = await import('../src/main/auth-spawn');
    const { readAuthStatus } = await import('../src/main/auth-session');
    const fake = new FakeChild();
    spawnMock.mockReturnValueOnce(fake);

    const promise = readAuthStatus('/private/vc', spawnAuthProcess);
    fake.stdout.emit('data', '{"authState":"signed_out"}\n');
    fake.emit('exit', 0, null);

    await expect(promise).resolves.toEqual({ ok: true, status: { authState: 'signed_out' } });
  });
});
