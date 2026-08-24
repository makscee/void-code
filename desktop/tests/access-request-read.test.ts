import { EventEmitter } from 'node:events';
import { describe, expect, it } from 'vitest';
import type { AuthChildProcess, AuthSpawner } from '../src/main/auth-session';

// The main-process end of the "Request access" button: the module that runs `vc access-request`
// and turns its one JSON object into something a screen may believe.
//
// WHAT THIS FILE CANNOT PROVE, AND NOBODY SHOULD READ IT AS PROVING: nothing here talks to a
// Relay. The route this subcommand calls is in void-relay's main and is not deployed, and the
// `access_requests` migration is not applied on production either — applying it is outside our
// gate. Every child process below is a fake emitting bytes this test wrote. So this file pins the
// boundary's behaviour against vc's documented output (the contract block at the top of
// cmd/vc/access_request.go), and says nothing at all about whether the chain works end to end.
// That is a "не смог", not a "прошло".
//
// Why the module exists separately from auth-session.ts: `vc status --json` answers "may I in",
// `vc access-request --json` answers "have I asked, and what came back". They are different
// questions with different vocabularies, and folding the second into readAuthStatus's whitelist
// would make one function that fails loudly for two unrelated reasons.

class FakeChild extends EventEmitter implements AuthChildProcess {
  readonly stdout = new EventEmitter();
  readonly stderr = new EventEmitter();
  end(code: number | null, signal: string | null = null): void {
    this.emit('exit', code, signal);
  }
}

type Report = { state: string; requestedAt?: string; resolvedAt?: string };
type Result = { ok: true; report: Report } | { ok: false; reason: string };
type ReadAccessRequest = (vcPath: string, spawn: AuthSpawner, ask: boolean) => Promise<Result>;

async function loadReader(): Promise<ReadAccessRequest> {
  let loaded: { readAccessRequest?: ReadAccessRequest };
  try {
    loaded = (await import('../src/main/access-request')) as { readAccessRequest?: ReadAccessRequest };
  } catch (error) {
    throw new Error(
      `src/main/access-request.ts does not exist — the desktop has no way to run \`vc access-request\`, so the button on the refusal screen has nothing behind it (${String(error)})`,
    );
  }
  if (typeof loaded.readAccessRequest !== 'function') {
    throw new Error('src/main/access-request.ts exports no readAccessRequest(vcPath, spawn, ask)');
  }
  return loaded.readAccessRequest;
}

// Records what was spawned, so the difference between reading and asking is checked at the one
// place it is actually decided: argv.
function recordingSpawner(child: FakeChild): { spawn: AuthSpawner; calls: { vcPath: string; args: string[] }[] } {
  const calls: { vcPath: string; args: string[] }[] = [];
  return {
    calls,
    spawn: (vcPath, args) => {
      calls.push({ vcPath, args: [...args] });
      return child;
    },
  };
}

async function runWith(line: string, ask = false, exitCode = 0): Promise<{ result: Result; args: string[] }> {
  const readAccessRequest = await loadReader();
  const child = new FakeChild();
  const spawner = recordingSpawner(child);
  const promise = readAccessRequest('/private/private-runtime/vc', spawner.spawn, ask);
  if (line.length > 0) child.stdout.emit('data', line);
  child.end(exitCode);
  return { result: await promise, args: spawner.calls[0]?.args ?? [] };
}

// Verbatim shapes from the contract block in cmd/vc/access_request.go.
const NOT_REQUESTED = '{"accessRequest":"not_requested"}\n';
const OPEN = '{"accessRequest":"open","requestedAt":"2026-08-22T09:15:00Z"}\n';
const GRANTED = '{"accessRequest":"granted","requestedAt":"2026-08-22T09:15:00Z","resolvedAt":"2026-08-23T11:00:00Z"}\n';
const DECLINED = '{"accessRequest":"declined","requestedAt":"2026-08-22T09:15:00Z","resolvedAt":"2026-08-23T11:00:00Z"}\n';
const UNAVAILABLE =
  '{"accessRequest":"unavailable","error":"the access-request service did not answer, so nothing is known about any request right now"}\n';
const SIGNED_OUT = '{"accessRequest":"signed_out"}\n';
const INVALID_CREDENTIAL = '{"accessRequest":"invalid_credential","error":"not logged in — run: vc login"}\n';

describe('asking is a separate act from reading, and argv is where that is decided', () => {
  it('reads with no --ask — opening the refusal screen must not file a request', async () => {
    // The screen re-reads status every time it is shown and on every "Check again". If the read
    // path carried --ask, a person who left the window open would file a request per look, and
    // the operator queue would fill with duplicates of one person who pressed nothing.
    const { args } = await runWith(NOT_REQUESTED, false);
    expect(args[0], `vc was invoked as ${JSON.stringify(args)} — the first argument must be the subcommand`).toBe('access-request');
    expect(args, 'the read path passes --ask, so merely looking at the screen files a request').not.toContain('--ask');
    expect(args, 'the desktop has no terminal to read a sentence in — it needs --json').toContain('--json');
  });

  it('files the request only when asked to, and says so in argv', async () => {
    const { args } = await runWith(OPEN, true);
    expect(args[0]).toBe('access-request');
    expect(args, 'the ask path does not pass --ask, so pressing the button reads the state and files nothing').toContain('--ask');
    expect(args, 'the desktop needs --json on this path too').toContain('--json');
  });

  it('spawns exactly the vc path it was handed, never a bare command name', async () => {
    // Same seam auth-spawn.test.ts guards for status and login: a PATH-resolved `vc` is a
    // different, unverified build, and this one carries a bearer token to a queue.
    const readAccessRequest = await loadReader();
    const child = new FakeChild();
    const spawner = recordingSpawner(child);
    const promise = readAccessRequest('/private/private-runtime/vc', spawner.spawn, false);
    child.stdout.emit('data', NOT_REQUESTED);
    child.end(0);
    await promise;
    expect(spawner.calls).toHaveLength(1);
    expect(spawner.calls[0].vcPath).toBe('/private/private-runtime/vc');
    expect(spawner.calls[0].vcPath).not.toBe('vc');
  });

  it('runs vc exactly once per call', async () => {
    // A read that also asks "to be sure", or an ask followed by a confirming read, both put two
    // rows where the person made one gesture — and the second call is the one nobody can see.
    const readAccessRequest = await loadReader();
    const child = new FakeChild();
    const spawner = recordingSpawner(child);
    const promise = readAccessRequest('/private/vc', spawner.spawn, true);
    child.stdout.emit('data', OPEN);
    child.end(0);
    await promise;
    expect(spawner.calls.length, `vc ran ${spawner.calls.length} times for one call`).toBe(1);
  });
});

describe('the states arrive as themselves — and "we could not ask" is never "you have not asked"', () => {
  it('carries open, granted and declined through with their dates', async () => {
    const open = await runWith(OPEN);
    expect(open.result, 'an open request was not read at all').toMatchObject({ ok: true });
    expect(open.result.ok && open.result.report.state).toBe('open');
    expect(open.result.ok && open.result.report.requestedAt, 'the submission date was dropped — the screen has nothing to put beside "waiting"').toBe('2026-08-22T09:15:00Z');

    const granted = await runWith(GRANTED);
    expect(granted.result.ok && granted.result.report.state).toBe('granted');
    expect(granted.result.ok && granted.result.report.resolvedAt).toBe('2026-08-23T11:00:00Z');

    const declined = await runWith(DECLINED);
    expect(declined.result.ok && declined.result.report.state).toBe('declined');
    expect(declined.result.ok && declined.result.report.requestedAt).toBe('2026-08-22T09:15:00Z');
    expect(declined.result.ok && declined.result.report.resolvedAt).toBe('2026-08-23T11:00:00Z');
  });

  it('keeps not_requested and unavailable as two different answers', async () => {
    // The whole reason vc emits seven words instead of a boolean. Collapsing these two — by
    // mapping anything that is not open/granted/declined onto "nothing filed", which is the
    // shortest code that makes the screen work — tells a person whose relay is down that they
    // never asked. They press the button, it fails the same silent way, and the conclusion they
    // reach is that they are doing it wrong.
    const none = await runWith(NOT_REQUESTED);
    const down = await runWith(UNAVAILABLE);
    expect(none.result.ok && none.result.report.state).toBe('not_requested');
    expect(down.result.ok && down.result.report.state).toBe('unavailable');
    expect(down.result.ok && down.result.report.state, 'a failed read was reported as "no request has been filed"').not.toBe('not_requested');
  });

  it('keeps signed_out and invalid_credential as themselves too', async () => {
    const signedOut = await runWith(SIGNED_OUT);
    expect(signedOut.result.ok && signedOut.result.report.state).toBe('signed_out');
    const invalid = await runWith(INVALID_CREDENTIAL);
    expect(invalid.result.ok && invalid.result.report.state).toBe('invalid_credential');
    expect(invalid.result.ok && invalid.result.report.state, 'a refused bearer was reported as "no request has been filed"').not.toBe('not_requested');
  });

  it('rejects a word vc has never printed instead of passing it on as a state', async () => {
    // Same rule isAuthState() already enforces next door, and for the same reason: the cheapest
    // way to make every test above pass is `typeof value.accessRequest === 'string'`, which turns
    // a future rename or typo in vc into a state the renderer branches on.
    const nonsense = await runWith('{"accessRequest":"pending_review"}\n');
    expect(nonsense.result, 'the state whitelist was widened into "any string" — a future vc typo would now reach a screen as a state').toEqual({ ok: false, reason: 'invalid_state' });
    const missing = await runWith('{"requestedAt":"2026-08-22T09:15:00Z"}\n');
    expect(missing.result, 'an object with no state at all was accepted').toEqual({ ok: false, reason: 'invalid_state' });
  });

  it('a run that broke is ok:false with a reason, never a state', async () => {
    const nonZero = await runWith(NOT_REQUESTED, false, 1);
    expect(nonZero.result).toEqual({ ok: false, reason: 'exit_nonzero' });
    const empty = await runWith('', false, 0);
    expect(empty.result).toEqual({ ok: false, reason: 'empty_output' });
    const garbage = await runWith('not json at all\n', false, 0);
    expect(garbage.result).toEqual({ ok: false, reason: 'invalid_json' });
  });

  it('survives stdout arriving in pieces, which is how the OS actually delivers it', async () => {
    const readAccessRequest = await loadReader();
    const child = new FakeChild();
    const spawner = recordingSpawner(child);
    const promise = readAccessRequest('/private/vc', spawner.spawn, false);
    child.stdout.emit('data', '{"accessRequest":"op');
    child.stdout.emit('data', 'en","requestedAt":"2026-08-22T09:15:00Z"}\n');
    child.end(0);
    const result = await promise;
    expect(result.ok && result.report.state, 'a JSON object split across two chunks was read as broken output').toBe('open');
  });
});

describe('an ask is a question, so nothing it carries may look like a grant', () => {
  it('does not let vc\'s free-text sentence out of the module', async () => {
    // The rule auth-session.ts already applies to "not logged in — run: vc login": raw text from
    // vc is not copy. It has never been through review, it is written for a terminal, and this is
    // the last place that can stop it before a window renders it.
    const result = (await runWith(UNAVAILABLE)).result;
    const serialised = JSON.stringify(result);
    expect(serialised, 'vc\'s sentence for a failed read travelled out of the module as copy').not.toContain('did not answer');
    expect(serialised).not.toContain('service');
    const credential = JSON.stringify((await runWith(INVALID_CREDENTIAL)).result);
    expect(credential, 'vc\'s terminal instruction travelled out as copy — a window has no terminal to run it in').not.toContain('vc login');
  });

  it('drops any field that would amount to an entitlement, however it arrives', async () => {
    // Not a hypothetical shape: the spec's one hard rule is that the request grants nothing, and
    // it is written down three times over (client, relay, keys) because each layer is a place a
    // helpful proxy could merge an expiry or a subscription into the reply. A field-by-field copy
    // is fine; a `{...parsed}` spread is what this test exists to fail.
    const result = (await runWith(
      '{"accessRequest":"granted","requestedAt":"2026-08-22T09:15:00Z","resolvedAt":"2026-08-23T11:00:00Z","expiresAt":"2026-09-23T00:00:00Z","subscription":{"active":true},"balanceUsd":42,"pct":0}\n',
    )).result;
    expect(result.ok, 'the payload was rejected outright, so the extra fields cannot be checked yet').toBe(true);
    const serialised = JSON.stringify(result);
    for (const field of ['expiresAt', 'subscription', 'balanceUsd', 'pct']) {
      expect(serialised.includes(field), `a resolved request carried "${field}" out of the module — the ask hands out nothing, and a screen that receives this can show it`).toBe(false);
    }
  });

  it('dates only the states that can have one', async () => {
    // Nothing was learned, so nothing may be dated. A date beside "we could not ask" reads as a
    // request that exists — the exact confusion the seven words were split up to prevent.
    const unavailable = (await runWith('{"accessRequest":"unavailable","requestedAt":"2026-08-22T09:15:00Z"}\n')).result;
    expect(unavailable.ok && unavailable.report.requestedAt, 'a failed read carried a submission date, so the screen can show "filed on ..." for a request nobody confirmed exists').toBeUndefined();
    const none = (await runWith('{"accessRequest":"not_requested","requestedAt":"2026-08-22T09:15:00Z"}\n')).result;
    expect(none.ok && none.report.requestedAt, '"nothing filed" carried a submission date').toBeUndefined();
    const open = (await runWith('{"accessRequest":"open","requestedAt":"2026-08-22T09:15:00Z","resolvedAt":"2026-08-23T11:00:00Z"}\n')).result;
    expect(open.ok && open.report.resolvedAt, 'an open request carried a resolution date — it is by definition unresolved').toBeUndefined();
  });
});
