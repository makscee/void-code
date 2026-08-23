import { EventEmitter } from 'node:events';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The desktop names the host that answers "who am I and am I let in", separately from the host
// that runs sign-in. They are not the same service in production, and one switch cannot serve both:
//
//   the access check      — Relay honours our Identity token; the legacy checker behind
//                           auth.makscee.ru rejects it outright ("not logged in").
//   sign-in and providers — only Identity serves them. Probed 2026-08-23 against relay:443, where
//                           a live route answers 401 and an absent one answers the CONNECT proxy:
//                             GET  /v1/vc/me              -> 401                        (live)
//                             GET  /v1/vc/providers       -> 400 "This is a CONNECT proxy"
//                             POST /v1/public/device/start-> 400 "This is a CONNECT proxy"
//                             GET  /v1/nonsense/xyz       -> 400 "This is a CONNECT proxy"
//
// So VC_ACCESS_CHECK_HOST is the only variable the desktop sets. VC_AUTH_HOST is left exactly as
// it was, which keeps sign-in and the Pi bootstrap on the CLI's own default — behaviour every
// hand-run `vc` already depends on, and not ours to change.
//
// The name says the role, not the route. It follows ErrAccessNotGranted next door, whose comment
// records the rule: name neither the protocol (402) nor the server mechanism, both of which are
// expected to change. "/v1/vc/me" is today's spelling of an access check, not its meaning.

const spawnMock = vi.hoisted(() => vi.fn());
vi.mock('node:child_process', () => ({ spawn: spawnMock }));

class FakeChild extends EventEmitter {
  readonly stdout = new EventEmitter();
  readonly stderr = new EventEmitter();
}

const ACCESS_CHECK_HOST = 'https://relay.makscee.ru';
const VC_PATH = '/private/private-runtime/vc';
const STATUS_ARGS = ['status', '--json'];
const LOGIN_ARGS = ['login', '--json'];
const BOTH_CALLS: ReadonlyArray<[string, string[]]> = [['status', STATUS_ARGS], ['login', LOGIN_ARGS]];

// Re-imported per measurement with process.env already arranged, so a seam that reads the parent
// environment once at module load and one that reads it per call measure the same. This file pins
// the value `vc` receives, not the moment the implementation reads it.
async function spawnOptions(args: string[]): Promise<{ env?: Record<string, string>; shell?: unknown; stdio?: unknown } | undefined> {
  vi.resetModules();
  spawnMock.mockReset();
  spawnMock.mockReturnValue(new FakeChild());
  const { spawnAuthProcess } = await import('../src/main/auth-spawn');
  spawnAuthProcess(VC_PATH, args);
  expect(spawnMock).toHaveBeenCalledTimes(1);
  return spawnMock.mock.calls[0][2] as { env?: Record<string, string> } | undefined;
}

// Every test below is about a seam that does not exist yet, so the guard is stated once here
// rather than repeated: without the variable, assertions like "the relay host did not move" or
// "no shell was used" would read green describing a build that never sets the host at all.
const MISSING = 'spawn carries no VC_ACCESS_CHECK_HOST — the access check falls back to the CLI default';

async function childEnv(args: string[]): Promise<Record<string, string>> {
  const options = await spawnOptions(args);
  expect(options?.env, 'spawn options carry no env at all').toBeDefined();
  expect(options!.env!.VC_ACCESS_CHECK_HOST, MISSING).toBeDefined();
  return options!.env!;
}

const SENTINEL = 'VC_TEST_PARENT_SENTINEL';
let savedAccess: string | undefined;
let savedAuth: string | undefined;

beforeEach(() => {
  savedAccess = process.env.VC_ACCESS_CHECK_HOST;
  savedAuth = process.env.VC_AUTH_HOST;
  delete process.env.VC_ACCESS_CHECK_HOST;
  delete process.env.VC_AUTH_HOST;
  process.env[SENTINEL] = 'inherited-from-parent';
});

afterEach(() => {
  delete process.env[SENTINEL];
  for (const [name, saved] of [['VC_ACCESS_CHECK_HOST', savedAccess], ['VC_AUTH_HOST', savedAuth]] as const) {
    if (saved === undefined) delete process.env[name];
    else process.env[name] = saved;
  }
});

describe('the access-check host the desktop hands vc', () => {
  it.each(BOTH_CALLS)('names it for the %s call', async (_name, args) => {
    const env = await childEnv(args);
    expect(env.VC_ACCESS_CHECK_HOST).toBe(ACCESS_CHECK_HOST);
  });

  // The seam this replaces chose the host from args[0]. That is what has to stop: the same
  // question ("am I let in") gets the same answer whoever asks it, and the next subcommand that
  // needs it would otherwise be one more arm of the same if.
  it('gives both calls the same host, with nothing decided from the argv', async () => {
    const [status, login] = [await childEnv(STATUS_ARGS), await childEnv(LOGIN_ARGS)];
    expect(status.VC_ACCESS_CHECK_HOST).toBe(login.VC_ACCESS_CHECK_HOST);
  });

  it.each(BOTH_CALLS)('gives %s a host in the shape the CLI can concatenate', async (_name, args) => {
    const host = (await childEnv(args)).VC_ACCESS_CHECK_HOST;
    // internal/auth/me.go builds the URL by string concatenation — `host + "/v1/vc/me"`. A
    // trailing slash yields `//v1/vc/me`, and internal/auth/device.go:45-47 shows the house rule
    // for a base URL: a query or a fragment on it is rejected outright.
    expect(host).toMatch(/^https:\/\//);
    expect(host.endsWith('/'), `${host} ends with a slash — vc would build //v1/vc/me`).toBe(false);
    const parsed = new URL(host);
    expect(parsed.pathname).toBe('/');
    expect(parsed.search).toBe('');
    expect(parsed.hash).toBe('');
  });
});

describe('the hosts the desktop must leave alone', () => {
  // Sign-in and the Pi bootstrap read VC_AUTH_HOST, and neither route exists on Relay. The
  // desktop setting it at all is the mistake this seam replaced; the fix is not a better value
  // but no value — whatever the parent had, untouched, so the CLI default keeps applying.
  it.each(BOTH_CALLS)('does not set VC_AUTH_HOST for %s', async (_name, args) => {
    const env = await childEnv(args);
    expect(env.VC_AUTH_HOST).toBe(process.env.VC_AUTH_HOST);
  });

  it.each(BOTH_CALLS)('passes an operator VC_AUTH_HOST through %s unchanged, neither dropped nor rewritten', async (_name, args) => {
    process.env.VC_AUTH_HOST = 'https://identity.stand.example';
    const env = await childEnv(args);
    expect(env.VC_AUTH_HOST).toBe('https://identity.stand.example');
  });

  // Setting the access-check host must not drag the relay along: model traffic is resolved from
  // VC_RELAY_HOST and has nothing to do with this check.
  it.each(BOTH_CALLS)('does not set VC_RELAY_HOST for %s', async (_name, args) => {
    const env = await childEnv(args);
    expect(env.VC_RELAY_HOST).toBe(process.env.VC_RELAY_HOST);
  });
});

describe('an operator pointing the app at a stand', () => {
  // Both halves in one test, because the behaviour is a precedence and either half alone is
  // vacuous: with no constant in the seam there is nothing to lose to, so "the override survived"
  // would pass on a build that merely inherits the parent environment and decides nothing.
  it.each(BOTH_CALLS)('lets an explicit VC_ACCESS_CHECK_HOST win over the desktop default for %s', async (_name, args) => {
    process.env.VC_ACCESS_CHECK_HOST = 'http://127.0.0.1:8449';
    expect((await childEnv(args)).VC_ACCESS_CHECK_HOST).toBe('http://127.0.0.1:8449');
    delete process.env.VC_ACCESS_CHECK_HOST;
    expect((await childEnv(args)).VC_ACCESS_CHECK_HOST).toBe(ACCESS_CHECK_HOST);
  });

  // "" is not a choice anyone made. `vc` reads an empty override as unset and falls back to its
  // own default, so honouring one here hands the check straight back to the legacy checker that
  // 401s our token — the exact bug, restored silently by a variable that looks set.
  it.each([['empty', ''], ['whitespace', '   ']])('treats an %s VC_ACCESS_CHECK_HOST as no choice at all', async (_name, value) => {
    process.env.VC_ACCESS_CHECK_HOST = value;
    const env = await childEnv(STATUS_ARGS);
    expect(env.VC_ACCESS_CHECK_HOST).toBe(ACCESS_CHECK_HOST);
  });
});

describe('what carrying an env must not cost', () => {
  // `env:` replaces the child environment wholesale — it does not merge. Handing over the one
  // variable would start vc with no HOME (no ~/.void-code token to read) and no PATH.
  it.each(BOTH_CALLS)('adds to the parent environment for %s rather than replacing it', async (_name, args) => {
    const env = await childEnv(args);
    const lost = Object.keys(process.env).filter((key) => key !== 'VC_ACCESS_CHECK_HOST').filter((key) => env[key] !== process.env[key]);
    expect(lost, `child environment dropped or altered ${lost.length} parent variable(s)`).toEqual([]);
    expect(env[SENTINEL]).toBe('inherited-from-parent');
  });

  // Asserted together with env on purpose: rewriting the options object is exactly where the
  // no-shell guarantee gets dropped, and checking it alone would read green before the rewrite
  // that endangers it has happened.
  it.each(BOTH_CALLS)('still spawns %s without a shell and with pipes attached', async (_name, args) => {
    const options = await spawnOptions(args);
    expect(options?.env?.VC_ACCESS_CHECK_HOST, MISSING).toBeDefined();
    expect(options?.shell).toBe(false);
    expect(options?.stdio).toEqual(['ignore', 'pipe', 'pipe']);
    expect(spawnMock.mock.calls[0][0]).toBe(VC_PATH);
    expect(spawnMock.mock.calls[0][1]).toEqual(args);
  });
});
