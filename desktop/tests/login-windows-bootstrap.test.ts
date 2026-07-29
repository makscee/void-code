import { EventEmitter } from 'node:events';
import { describe, expect, it, vi } from 'vitest';
import { LoginManager } from '../src/main/login-manager';
import { loginCompletionAction } from '../src/renderer/login-retry';

class WindowsLoginChild extends EventEmitter { kill = vi.fn(() => true); }

describe('Windows packaged missing-token bootstrap regression', () => {
  it('moves from the login action through bundled vc login to session retry', () => {
    const child = new WindowsLoginChild(); const spawn = vi.fn(() => child as never);
    const manager = new LoginManager(
      'C:\\Program Files\\Void Code\\resources\\private-runtime\\vc\\vc.exe',
      'C:\\Program Files\\Void Code\\resources\\private-runtime',
      spawn,
      { VC_AUTH_HOST: 'https://auth.test', VC_RELAY_HOST: 'https://relay.test', VC_RELAY_CA: 'C:\\ProgramData\\Void Code\\ca.pem' },
    );
    expect(manager.status()).toEqual({ state: 'unavailable' });
    expect(loginCompletionAction(manager.status(), true)).toBe('none');
    expect(manager.start()).toEqual({ state: 'running' });
    expect(spawn).toHaveBeenCalledWith(expect.stringMatching(/private-runtime\\vc\\vc\.exe$/), ['login'], expect.objectContaining({ shell: false, windowsHide: true }));
    child.emit('exit', 0, null);
    expect(manager.status()).toEqual({ state: 'succeeded' });
    expect(loginCompletionAction(manager.status(), true)).toBe('retry');
  });
});
