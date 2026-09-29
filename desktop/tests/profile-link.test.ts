import { EventEmitter } from 'node:events';
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import type { AuthChildProcess, AuthStatus } from '../src/main/auth-session';
import { readAuthStatus } from '../src/main/auth-session';
import { profileLinkFor } from '../src/renderer/auth-view';

// The profile link (void-board#480): `vc status --json` carries `profileUrl`, the page with the
// balance, weekly limit and usage. The header shows a Profile button next to the wallet line for a
// signed-in status, and a click opens that URL in the browser (the page asks for its own email code).

const PROFILE_URL = 'https://profile.makscee.ru/profile';

class FakeChild extends EventEmitter implements AuthChildProcess {
  readonly stdout = new EventEmitter();
  readonly stderr = new EventEmitter();
}
async function statusOf(payload: Record<string, unknown>): Promise<AuthStatus> {
  const child = new FakeChild();
  const promise = readAuthStatus('/private/vc', () => child);
  child.stdout.emit('data', `${JSON.stringify(payload)}\n`);
  child.emit('exit', 0, null);
  const result = await promise;
  if (!result.ok) throw new Error(`status rejected: ${result.reason}`);
  return result.status;
}

describe('readAuthStatus — profileUrl', () => {
  it('passes the https profile link of a signed-in status through', async () => {
    expect((await statusOf({ authState: 'signed_in', identity: 'artem', profileUrl: PROFILE_URL })).profileUrl).toBe(PROFILE_URL);
  });
  it.each([
    ['a non-https link', 'javascript:alert(1)'],
    ['a plain http link', 'http://profile.makscee.ru/profile'],
    ['not a URL', 'profile'],
    ['not a string', 42],
  ])('drops %s', async (_label, profileUrl) => {
    expect((await statusOf({ authState: 'signed_in', identity: 'artem', profileUrl })).profileUrl).toBeUndefined();
  });
  it('carries no link for a status that is not signed in', async () => {
    expect((await statusOf({ authState: 'signed_out', profileUrl: PROFILE_URL })).profileUrl).toBeUndefined();
    expect((await statusOf({ authState: 'access_not_granted', profileUrl: PROFILE_URL })).profileUrl).toBeUndefined();
  });
});

describe('profileLinkFor', () => {
  const status = (fields: Record<string, unknown>): AuthStatus => fields as unknown as AuthStatus;
  it('is the link vc sent, for a signed-in status', () => {
    expect(profileLinkFor(status({ authState: 'signed_in', profileUrl: PROFILE_URL }))).toBe(PROFILE_URL);
  });
  it.each([
    ['no status yet', null],
    ['signed in without a link', status({ authState: 'signed_in' })],
    ['signed out', status({ authState: 'signed_out', profileUrl: PROFILE_URL })],
    ['an expired sign-in', status({ authState: 'invalid_credential', profileUrl: PROFILE_URL })],
  ])('is null for %s', (_label, value) => {
    expect(profileLinkFor(value)).toBeNull();
  });
});

describe('Profile button', () => {
  const html = readFileSync(new URL('../src/renderer/index.html', import.meta.url), 'utf8');
  const renderer = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');
  it('sits in the header, next to the wallet line, hidden until a status says otherwise', () => {
    const header = html.match(/<header>([\s\S]*?)<\/header>/)?.[1] ?? '';
    expect(header).toMatch(/id="wallet-line"[^>]*><\/span><button id="profile-link" type="button"[^>]*\bhidden\b[^>]*>Profile<\/button>/);
  });
  it('shows exactly when profileLinkFor gives a link, and opens that link', () => {
    const apply = renderer.match(/^function applyAuthStatus\([\s\S]*?\n\}/m)?.[0] ?? '';
    expect(apply).toMatch(/profileUrl = profileLinkFor\(result\.ok \? result\.status : null\);/);
    expect(apply).toMatch(/profileLinkButton\.hidden = profileUrl === null;/);
    expect(renderer).toMatch(/profileLinkButton\.addEventListener\('click', \(\) => \{ if \(profileUrl !== null\) void window\.voidTerminal\.openLink\(profileUrl\); \}\);/);
  });
});
