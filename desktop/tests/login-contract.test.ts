import { describe, expect, it } from 'vitest';
import { IPC, loginStatus } from '../src/shared/contract';

describe('renderer login contract', () => {
  it('contains no renderer-supplied login payload and validates only safe states', () => {
    expect(IPC.loginStart).toBe('auth:login-start'); expect(IPC.loginCancel).toBe('auth:login-cancel');
    for (const state of ['unavailable', 'running', 'succeeded', 'failed', 'cancelled'] as const) expect(loginStatus({ state })).toEqual({ state });
    expect(() => loginStatus({ state: 'failed', token: 'secret' })).toThrow();
    expect(() => loginStatus({ state: 'failed', detail: 'https://device' })).toThrow();
  });
});
