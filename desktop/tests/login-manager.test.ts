import { EventEmitter } from 'node:events';
import { describe, expect, it, vi } from 'vitest';
import { LoginManager } from '../src/main/login-manager';

class FakeChild extends EventEmitter { kill = vi.fn(() => true); }

describe('packaged login process', () => {
  it('launches only the package-owned vc login with inherited deployment endpoints and no output pipes', () => {
    const child = new FakeChild();
    const spawn = vi.fn(() => child as never);
    const manager = new LoginManager('/package/private-runtime/vc/bin/vc', '/package/private-runtime', spawn, {
      PATH: '/host/path', HOME: '/home/user', VC_AUTH_HOST: 'https://staging-auth', VC_RELAY_HOST: 'https://staging-relay', VC_RELAY_CA: '/staging/ca.pem',
    });
    expect(manager.start()).toEqual({ state: 'running' });
    expect(spawn).toHaveBeenCalledWith('/package/private-runtime/vc/bin/vc', ['login'], expect.objectContaining({
      cwd: '/package/private-runtime', stdio: 'ignore', shell: false, windowsHide: true,
      env: expect.objectContaining({ VC_AUTH_HOST: 'https://staging-auth', VC_RELAY_HOST: 'https://staging-relay', VC_RELAY_CA: '/staging/ca.pem' }),
    }));
    expect(JSON.stringify(manager.status())).not.toMatch(/staging|token|device|environment/i);
  });

  it('prevents concurrent attempts and reports only coarse completion state', () => {
    const first = new FakeChild(); const spawn = vi.fn(() => first as never);
    const states: string[] = []; const manager = new LoginManager('/owned/vc', '/owned', spawn, {});
    manager.onStatus((status) => states.push(status.state));
    expect(manager.start()).toEqual({ state: 'running' });
    expect(manager.start()).toEqual({ state: 'running' });
    expect(spawn).toHaveBeenCalledTimes(1);
    first.emit('exit', 7, null);
    expect(manager.status()).toEqual({ state: 'failed' });
    expect(states).toEqual(['running', 'failed']);
  });

  it('does not permit a replacement until a cancelled child exits', () => {
    const first = new FakeChild(); const second = new FakeChild(); const spawn = vi.fn().mockReturnValueOnce(first).mockReturnValueOnce(second);
    const manager = new LoginManager('/owned/vc', '/owned', spawn, {});
    manager.start(); expect(manager.cancel()).toEqual({ state: 'cancelled' });
    expect(first.kill).toHaveBeenCalledWith();
    expect(manager.start()).toEqual({ state: 'cancelled' }); expect(spawn).toHaveBeenCalledTimes(1);
    first.emit('exit', null, 'SIGTERM');
    expect(manager.status()).toEqual({ state: 'cancelled' });
    expect(manager.start()).toEqual({ state: 'running' }); expect(spawn).toHaveBeenCalledTimes(2);
  });

  it('isolates status observers and escalates a cancelled child that does not exit', () => {
    vi.useFakeTimers();
    try {
      const child = new FakeChild(); child.kill.mockReturnValue(false);
      const spawn = vi.fn(() => child as never); const manager = new LoginManager('/owned/vc', '/owned', spawn, {});
      manager.onStatus(() => { throw new Error('observer failed'); });
      expect(() => manager.start()).not.toThrow(); expect(child.listenerCount('exit')).toBe(1); expect(child.listenerCount('error')).toBe(1);
      expect(() => manager.cancel()).not.toThrow(); vi.advanceTimersByTime(1000);
      expect(child.kill).toHaveBeenNthCalledWith(1); expect(child.kill).toHaveBeenNthCalledWith(2, 'SIGKILL');
      expect(manager.start()).toEqual({ state: 'cancelled' }); expect(spawn).toHaveBeenCalledTimes(1);
      child.emit('exit', null, 'SIGKILL'); expect(manager.status()).toEqual({ state: 'cancelled' });
    } finally { vi.useRealTimers(); }
  });

  it('handles success, spawn errors, and shutdown with forced child cleanup', () => {
    const success = new FakeChild(); const failing = new FakeChild(); const spawn = vi.fn().mockReturnValueOnce(success).mockReturnValueOnce(failing);
    const manager = new LoginManager('/owned/vc', '/owned', spawn, {});
    manager.start(); success.emit('exit', 0, null); expect(manager.status()).toEqual({ state: 'succeeded' });
    manager.start(); failing.emit('error', new Error('secret URL')); expect(manager.status()).toEqual({ state: 'failed' });
    const shutdownChild = new FakeChild(); spawn.mockReturnValueOnce(shutdownChild);
    manager.start(); manager.shutdown(); expect(shutdownChild.kill).toHaveBeenCalledWith('SIGKILL'); expect(manager.status()).toEqual({ state: 'cancelled' });
  });
});
