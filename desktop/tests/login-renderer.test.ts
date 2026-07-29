import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { automaticLoginRetry, loginCompletionAction } from '../src/renderer/login-retry';

describe('packaged login renderer', () => {
  it('starts a first chat or retries the selected chat only after successful login', () => {
    expect(loginCompletionAction({ state: 'succeeded' }, false)).toBe('create');
    expect(loginCompletionAction({ state: 'succeeded' }, true)).toBe('retry');
    for (const state of ['unavailable', 'running', 'failed', 'cancelled'] as const) {
      expect(loginCompletionAction({ state }, false)).toBe('none');
      expect(loginCompletionAction({ state }, true)).toBe('none');
    }
  });

  it('retries only early post-login exits with a bounded backoff', () => {
    expect(automaticLoginRetry('create', 0, 500)).toEqual({ attempt: 1, delayMs: 1000, mode: 'create' });
    expect(automaticLoginRetry('create', 1, 2_000)).toEqual({ attempt: 2, delayMs: 2000, mode: 'create' });
    expect(automaticLoginRetry('resume', 2, 5_000)).toEqual({ attempt: 3, delayMs: 4000, mode: 'resume' });
    expect(automaticLoginRetry('create', 3, 5_000)).toBeNull();
    expect(automaticLoginRetry('create', 0, 15_001)).toBeNull();
  });

  it('offers bounded sign-in and cancellation actions without collecting auth data', () => {
    const html = readFileSync(new URL('../src/renderer/index.html', import.meta.url), 'utf8');
    const renderer = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');
    expect(html).toContain('Sign in with VC');
    expect(html).toContain('Cancel sign-in');
    expect(html).not.toMatch(/token|device[- ]?(?:code|url)|auth[_ -]?host|relay[_ -]?host/i);
    expect(renderer).toContain('window.voidTerminal.login.start()');
    expect(renderer).toContain("await launch(tab, 'resume', 0)");
    expect(renderer).toContain("await launch(tab, 'create', 0)");
  });
});
