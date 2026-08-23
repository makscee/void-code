import { EventEmitter } from 'node:events';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { desktopChildEnv } from '../src/main/desktop-child-env';

// The chat session never goes through auth-spawn.ts. It is spawned from spawn-request.ts with an
// environment desktopChildEnv builds from nothing, so a variable the desktop sets for `vc status`
// does not reach it — and `vc desktop-session` runs its own access check before Pi ever starts:
//
//   cmd/vc/desktop_session.go:73     deps.authGate(token, <host>, ...)
//   cmd/vc/main.go:541-547           authGate -> auth.FetchMe -> ErrNotLoggedIn -> error
//   cmd/vc/desktop_session.go:74-76  -> "authentication unavailable: %w"
//
// That gate runs before buildPiSpawnEnv, i.e. before anything resolved from VC_RELAY_HOST. Fixing
// only the status probe leaves the app honest about being signed in and still unable to open a
// chat, which is the whole point of the work. So the allowlist gains exactly one member.
//
// It gains a CONSTANT, not a passthrough — deliberately unlike auth-spawn.ts, and the asymmetry
// is the point rather than an oversight. FetchMe sends `Authorization: Bearer <token>` to whatever
// host it is given. Letting the parent environment name that host builds a token-exfiltration
// channel: a hostile variable points the access check at an attacker's host, collects the bearer,
// answers 200 with any identity it likes, and opens a session that should not have opened. The
// allowlist in desktopChildEnv exists against exactly this, which is why VC_RELAY_HOST already
// sits in `poison` below. auth-spawn.ts may honour the operator's value because it changes no
// trust boundary there — a hand-run `vc` reads the environment anyway.
//
// The price, stated so nobody rediscovers it as a bug: an operator can point the status probe at
// a stand and not the chat. The right fix is an application setting, not an environment variable,
// and that is separate work.

const spawnMock = vi.hoisted(() => vi.fn());
vi.mock('node:child_process', () => ({ spawn: spawnMock }));

class FakeChild extends EventEmitter {
  readonly stdout = new EventEmitter();
  readonly stderr = new EventEmitter();
}

const ACCESS_CHECK_HOST = 'https://relay.makscee.ru';
const HOSTILE = 'https://attacker.example';

const poison = {
  NODE_OPTIONS: '--require evil', ELECTRON_RUN_AS_NODE: '1', VC_RELAY_HOST: 'evil', VC_AUTH_HOST: 'evil',
  PI_CODING_AGENT_DIR: '/evil', ANTHROPIC_AUTH_TOKEN: 'secret', HTTPS_PROXY: 'evil', AWS_SECRET_ACCESS_KEY: 'secret', PATH: '/evil',
};
const darwinParent = { ...poison, HOME: '/Users/real', TMPDIR: '/private/tmp/real' };
const windowsParent = { ...poison, userprofile: 'C:\\Users\\real', SYSTEMROOT: 'D:\\Windows', temp: 'T:\\temp', tmp: 'T:\\tmp' };

const darwin = (parent: NodeJS.ProcessEnv) => desktopChildEnv('darwin', parent, '/app/private/node');
const windows = (parent: NodeJS.ProcessEnv) => desktopChildEnv('win32', parent, 'C:\\app\\node.exe');
const PLATFORMS = [['darwin', darwin, darwinParent], ['win32', windows, windowsParent]] as const;

let savedAccess: string | undefined;
beforeEach(() => { savedAccess = process.env.VC_ACCESS_CHECK_HOST; delete process.env.VC_ACCESS_CHECK_HOST; });
afterEach(() => { if (savedAccess === undefined) delete process.env.VC_ACCESS_CHECK_HOST; else process.env.VC_ACCESS_CHECK_HOST = savedAccess; });

describe('the chat session gets the access-check host', () => {
  it.each(PLATFORMS)('puts it in the %s allowlist', (_name, build, parent) => {
    expect(build(parent).VC_ACCESS_CHECK_HOST).toBe(ACCESS_CHECK_HOST);
  });

  it('gives it in the shape the CLI can concatenate', () => {
    const host = darwin(darwinParent).VC_ACCESS_CHECK_HOST;
    expect(host).toMatch(/^https:\/\//);
    expect(host.endsWith('/'), `${host} ends with a slash — vc would build //v1/vc/me`).toBe(false);
    const parsed = new URL(host);
    expect(parsed.pathname).toBe('/');
    expect(parsed.search).toBe('');
    expect(parsed.hash).toBe('');
  });

  // Two seams, one answer, and nothing in the type system holds them together — each carries its
  // own copy of the decision unless the implementation gives them a shared one.
  it('hands the chat the same host the status probe gets when nobody overrides anything', async () => {
    spawnMock.mockReset();
    spawnMock.mockReturnValue(new FakeChild());
    const { spawnAuthProcess } = await import('../src/main/auth-spawn');
    spawnAuthProcess('/private/private-runtime/vc', ['status', '--json']);
    const spawned = (spawnMock.mock.calls[0][2] as { env?: Record<string, string> } | undefined)?.env;
    // Named before the comparison, or two seams that both set nothing would agree perfectly.
    expect(spawned?.VC_ACCESS_CHECK_HOST, 'the status spawn carries no access-check host').toBeDefined();
    expect(darwin(darwinParent).VC_ACCESS_CHECK_HOST).toBe(spawned?.VC_ACCESS_CHECK_HOST);
  });
});

describe('the chat takes our value and refuses the environment', () => {
  // The exfiltration case, stated as a test so the next person to "make this consistent" reads
  // the reason instead of guessing it. A hostile parent variable must not choose where a bearer
  // token gets sent.
  it.each(PLATFORMS)('ignores a hostile VC_ACCESS_CHECK_HOST from the parent on %s', (_name, build, parent) => {
    const env = build({ ...parent, VC_ACCESS_CHECK_HOST: HOSTILE });
    expect(env.VC_ACCESS_CHECK_HOST, 'the parent environment chose where vc sends the bearer token').not.toBe(HOSTILE);
    expect(env.VC_ACCESS_CHECK_HOST).toBe(ACCESS_CHECK_HOST);
  });

  // Every other Windows name here is matched case-insensitively, because that is how the platform
  // hands them over. A refusal that only checks the exact spelling is not a refusal.
  it('ignores it on Windows whatever the case of the name', () => {
    const env = windows({ ...windowsParent, vc_access_check_host: HOSTILE });
    expect(env.VC_ACCESS_CHECK_HOST).toBe(ACCESS_CHECK_HOST);
  });

  // The asymmetry itself, pinned: the two seams are meant to disagree under an override, and a
  // future change that "unifies" them has to delete this test and read why it existed.
  it('diverges from auth-spawn under an override, on purpose', async () => {
    process.env.VC_ACCESS_CHECK_HOST = 'http://127.0.0.1:8449';
    spawnMock.mockReset();
    spawnMock.mockReturnValue(new FakeChild());
    vi.resetModules();
    const { spawnAuthProcess } = await import('../src/main/auth-spawn');
    spawnAuthProcess('/private/private-runtime/vc', ['status', '--json']);
    const spawned = (spawnMock.mock.calls[0][2] as { env?: Record<string, string> } | undefined)?.env;
    expect(spawned?.VC_ACCESS_CHECK_HOST, 'the operator lane stopped honouring the override').toBe('http://127.0.0.1:8449');
    expect(darwin({ ...darwinParent, VC_ACCESS_CHECK_HOST: 'http://127.0.0.1:8449' }).VC_ACCESS_CHECK_HOST).toBe(ACCESS_CHECK_HOST);
  });
});

describe('the allowlist stays an allowlist', () => {
  it.each(PLATFORMS)('lets nothing else through on %s', (_name, build, parent) => {
    const env = build(parent);
    // Stated first so this cannot read green describing an allowlist that never gained the member
    // whose arrival is the reason to re-check what else got in.
    expect(env.VC_ACCESS_CHECK_HOST, 'the allowlist never gained the access-check host').toBeDefined();
    for (const name of ['NODE_OPTIONS', 'ELECTRON_RUN_AS_NODE', 'VC_RELAY_HOST', 'VC_AUTH_HOST', 'PI_CODING_AGENT_DIR', 'ANTHROPIC_AUTH_TOKEN', 'HTTPS_PROXY', 'AWS_SECRET_ACCESS_KEY']) {
      expect(env[name], `${name} leaked from the parent environment into the chat session`).toBeUndefined();
    }
  });

  // The access check moving does not move the relay: model traffic resolves from VC_RELAY_HOST,
  // and sign-in and the Pi bootstrap resolve from VC_AUTH_HOST. Neither may follow this one.
  it.each(PLATFORMS)('adds exactly one name on %s', (_name, build, parent) => {
    const env = build(parent);
    expect(Object.keys(env)).toContain('VC_ACCESS_CHECK_HOST');
    expect(env.VC_AUTH_HOST).toBeUndefined();
    expect(env.VC_RELAY_HOST).toBeUndefined();
  });
});
