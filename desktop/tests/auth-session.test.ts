import { EventEmitter } from 'node:events';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import type { AuthChildProcess, AuthSpawner, LoginEvent } from '../src/main/auth-session';
import { readAuthStatus, runLogin } from '../src/main/auth-session';

// Minimal double for the child_process.ChildProcess shape the module actually reads:
// separate stdout/stderr streams and an 'exit' event on the process itself.
class FakeChild extends EventEmitter implements AuthChildProcess {
  readonly stdout = new EventEmitter();
  readonly stderr = new EventEmitter();
  end(code: number | null, signal: string | null = null): void {
    this.emit('exit', code, signal);
  }
}

function fixedSpawner(child: FakeChild): AuthSpawner {
  return () => child;
}

// Splits a string at the given character offsets and delivers each piece as its own
// 'data' event — this is what a real pipe does when the OS hands back partial writes.
function deliverChunked(stream: EventEmitter, text: string, offsets: number[]): void {
  const cuts = [0, ...offsets, text.length];
  for (let index = 0; index < cuts.length - 1; index++) stream.emit('data', text.slice(cuts[index], cuts[index + 1]));
}

const PROMPT_LINE = '{"event":"prompt","userCode":"MSKDWMSW","verificationUrl":"https://auth.makscee.ru/device","expiresInSeconds":600}\n';
const AUTHORIZED_LINE = '{"event":"authorized"}\n';
const LOGIN_STREAM = PROMPT_LINE + AUTHORIZED_LINE;

const EXPECTED_LOGIN_EVENTS: LoginEvent[] = [
  { event: 'prompt', userCode: 'MSKDWMSW', verificationUrl: 'https://auth.makscee.ru/device', expiresInSeconds: 600 },
  { event: 'authorized' },
];

describe('readAuthStatus', () => {
  it('parses a signed-in status', async () => {
    const child = new FakeChild();
    const promise = readAuthStatus('/private/vc', fixedSpawner(child));
    child.stdout.emit('data', '{"authState":"signed_in","identity":"artem","pct":12.5,"resetAt":"2026-09-01T00:00:00.000Z"}\n');
    child.end(0);
    await expect(promise).resolves.toEqual({
      ok: true,
      status: { authState: 'signed_in', identity: 'artem', pct: 12.5, resetAt: '2026-09-01T00:00:00.000Z' },
    });
  });

  it('parses signed_out with no extra fields', async () => {
    const child = new FakeChild();
    const promise = readAuthStatus('/private/vc', fixedSpawner(child));
    child.stdout.emit('data', '{"authState":"signed_out"}\n');
    child.end(0);
    await expect(promise).resolves.toEqual({ ok: true, status: { authState: 'signed_out' } });
  });

  it('replaces the terminal-instruction error text with a stable reason word, never the raw text', async () => {
    const child = new FakeChild();
    const promise = readAuthStatus('/private/vc', fixedSpawner(child));
    child.stdout.emit('data', '{"authState":"invalid_credential","error":"not logged in — run: vc login"}\n');
    child.end(0);
    const result = await promise;
    expect(result).toEqual({ ok: true, status: { authState: 'invalid_credential', reason: 'not_authenticated' } });
    // The whole point: nothing handed back to the UI may still say "run" a terminal command.
    expect(JSON.stringify(result)).not.toContain('vc login');
    expect(JSON.stringify(result)).not.toContain('run:');
  });

  it('fails in a defined way on non-zero exit, never reporting signed_out', async () => {
    const child = new FakeChild();
    const promise = readAuthStatus('/private/vc', fixedSpawner(child));
    child.stdout.emit('data', '');
    child.end(1);
    await expect(promise).resolves.toEqual({ ok: false, reason: 'exit_nonzero' });
  });

  it('fails in a defined way on an empty stream, never reporting signed_out', async () => {
    const child = new FakeChild();
    const promise = readAuthStatus('/private/vc', fixedSpawner(child));
    child.end(0);
    await expect(promise).resolves.toEqual({ ok: false, reason: 'empty_output' });
  });

  it('fails in a defined way on output that is not JSON, never reporting signed_out', async () => {
    const child = new FakeChild();
    const promise = readAuthStatus('/private/vc', fixedSpawner(child));
    child.stdout.emit('data', 'segfault: core dumped\n');
    child.end(0);
    await expect(promise).resolves.toEqual({ ok: false, reason: 'invalid_json' });
  });

  // Valid JSON is not the same guarantee as a valid status: a regression that prints a
  // well-formed but unrecognised object must not be handed to the UI as ok:true — that is
  // the same "signed_out shown for someone signed in" bug, just from a different fault.
  // A distinct reason from 'invalid_json' matters because "not JSON at all" (a crash, a
  // stray log line) and "JSON but not a status" (a schema regression in vc) are different
  // bugs to chase, and the support report should say which one happened.
  it.each([
    ['an object with no authState at all', '{}\n'],
    ['an authState that is not one of the three known words', '{"authState":"nonsense"}\n'],
    ['a JSON object typo of the field name', '{"auth_state":"signed_in"}\n'],
    ['a bare JSON null', 'null\n'],
    ['a bare JSON string', '"signed_in"\n'],
    ['a bare JSON number', '42\n'],
    ['a JSON array', '["signed_in"]\n'],
  ])('fails in a defined way, never reporting ok:true, on %s', async (_label, raw) => {
    const child = new FakeChild();
    const promise = readAuthStatus('/private/vc', fixedSpawner(child));
    child.stdout.emit('data', raw);
    child.end(0);
    await expect(promise).resolves.toEqual({ ok: false, reason: 'invalid_status' });
  });
});

describe('runLogin', () => {
  it('surfaces the verification URL to the injected opener, not to Electron shell directly', async () => {
    const child = new FakeChild();
    const opened: string[] = [];
    const events: LoginEvent[] = [];
    const promise = runLogin('/private/vc', fixedSpawner(child), (event) => events.push(event), (url) => opened.push(url));
    child.stdout.emit('data', LOGIN_STREAM);
    child.end(0);
    await expect(promise).resolves.toEqual({ ok: true });
    expect(events).toEqual(EXPECTED_LOGIN_EVENTS);
    expect(opened).toEqual(['https://auth.makscee.ru/device']);
  });

  // The point of this file: the same bytes, split at different arbitrary offsets, must
  // always parse to the same events. A per-chunk JSON.parse looks fine on a lucky split
  // and silently drops or corrupts events the moment the OS splits differently.
  it('parses identically regardless of where the stream is split into chunks', async () => {
    const midObjectOffset = LOGIN_STREAM.indexOf('"userCode"'); // inside the JSON object, before any string value
    const midUserCodeValueOffset = LOGIN_STREAM.indexOf('MSKDWMSW') + 3; // inside the userCode string's characters
    const midSecondLineOffset = PROMPT_LINE.length + AUTHORIZED_LINE.indexOf('"authorized"'); // spans the newline between the two events
    const offsetSets = [
      [1], // split one byte in, before anything meaningful has arrived
      [midObjectOffset],
      [midUserCodeValueOffset],
      [midObjectOffset, midUserCodeValueOffset, midSecondLineOffset],
      [PROMPT_LINE.length], // exactly on the line boundary — the "lucky" split a naive parser would pass on
      [LOGIN_STREAM.length - 1], // split one byte before the very end
    ];
    for (const offsets of offsetSets) {
      const child = new FakeChild();
      const events: LoginEvent[] = [];
      const promise = runLogin('/private/vc', fixedSpawner(child), (event) => events.push(event), () => {});
      deliverChunked(child.stdout, LOGIN_STREAM, offsets);
      child.end(0);
      await promise;
      expect(events).toEqual(EXPECTED_LOGIN_EVENTS);
    }
  });

  it('ignores a plain-text stderr warning: it must not corrupt parsing or reach the event listener', async () => {
    const child = new FakeChild();
    const events: LoginEvent[] = [];
    const promise = runLogin('/private/vc', fixedSpawner(child), (event) => events.push(event), () => {});
    child.stderr.emit('data', 'warning: pty size unavailable, using defaults\n');
    child.stdout.emit('data', LOGIN_STREAM);
    child.end(0);
    await promise;
    expect(events).toEqual(EXPECTED_LOGIN_EVENTS);
    expect(events.some((event) => JSON.stringify(event).includes('pty size'))).toBe(false);
  });

  it('ignores blank lines in stdout', async () => {
    const child = new FakeChild();
    const events: LoginEvent[] = [];
    const promise = runLogin('/private/vc', fixedSpawner(child), (event) => events.push(event), () => {});
    child.stdout.emit('data', `\n${PROMPT_LINE}\n${AUTHORIZED_LINE}`);
    child.end(0);
    await promise;
    expect(events).toEqual(EXPECTED_LOGIN_EVENTS);
  });

  it('reports a malformed line through the diagnostic channel, not silently and not as an event, and does not kill the login', async () => {
    const child = new FakeChild();
    const events: LoginEvent[] = [];
    const diagnostics: string[] = [];
    const promise = runLogin('/private/vc', fixedSpawner(child), (event) => events.push(event), () => {}, (message) => diagnostics.push(message));
    child.stdout.emit('data', `{"event":"prompt",NOT VALID JSON\n${PROMPT_LINE}${AUTHORIZED_LINE}`);
    child.end(0);
    await expect(promise).resolves.toEqual({ ok: true });
    expect(events).toEqual(EXPECTED_LOGIN_EVENTS);
    expect(diagnostics.length).toBeGreaterThan(0);
    expect(diagnostics.some((message) => message.includes('NOT VALID JSON'))).toBe(true);
  });

  // Same class of gap as readAuthStatus, checked on the login side: a line can be valid JSON
  // and still not be a known login event (missing "event", or an "event" word vc has never
  // sent before). It must land as a diagnostic, not as a LoginEvent the UI would branch on.
  it('treats syntactically valid JSON with no recognised event as a diagnostic, not a LoginEvent', async () => {
    const child = new FakeChild();
    const events: LoginEvent[] = [];
    const diagnostics: string[] = [];
    const promise = runLogin('/private/vc', fixedSpawner(child), (event) => events.push(event), () => {}, (message) => diagnostics.push(message));
    child.stdout.emit('data', `{"foo":"bar"}\n{"event":"denied"}\n${PROMPT_LINE}${AUTHORIZED_LINE}`);
    child.end(0);
    await expect(promise).resolves.toEqual({ ok: true });
    expect(events).toEqual(EXPECTED_LOGIN_EVENTS);
    expect(diagnostics.length).toBeGreaterThanOrEqual(2);
  });

  // A bare non-object JSON value (null, a number, a string, an array) parses without error,
  // then a naive `parsed.event` read on it either throws (null) or is simply undefined —
  // either way it must land as a diagnostic like any other unrecognised line, never crash
  // the line handler and never silently vanish, and the stream must keep delivering real
  // events afterward.
  it.each([
    ['a bare JSON null', 'null'],
    ['a bare JSON number', '42'],
    ['a bare JSON string', '"hello"'],
    ['a JSON array', '["prompt"]'],
  ])('treats %s arriving on stdout as a diagnostic, not a crash, and keeps the login going', async (_label, raw) => {
    const child = new FakeChild();
    const events: LoginEvent[] = [];
    const diagnostics: string[] = [];
    const promise = runLogin('/private/vc', fixedSpawner(child), (event) => events.push(event), () => {}, (message) => diagnostics.push(message));
    child.stdout.emit('data', `${raw}\n${PROMPT_LINE}${AUTHORIZED_LINE}`);
    child.end(0);
    await expect(promise).resolves.toEqual({ ok: true });
    expect(events).toEqual(EXPECTED_LOGIN_EVENTS);
    expect(diagnostics.length).toBeGreaterThan(0);
  });

  it('ends the login in a defined way when the process exits non-zero after only a prompt', async () => {
    const child = new FakeChild();
    const events: LoginEvent[] = [];
    const promise = runLogin('/private/vc', fixedSpawner(child), (event) => events.push(event), () => {});
    child.stdout.emit('data', PROMPT_LINE);
    child.end(17);
    await expect(promise).resolves.toEqual({ ok: false, reason: 'exited_unexpectedly' });
    expect(events).toEqual([EXPECTED_LOGIN_EVENTS[0]]);
  });

  it('resolves with the stable reason word from an explicit error event, not a generic failure', async () => {
    const child = new FakeChild();
    const events: LoginEvent[] = [];
    const promise = runLogin('/private/vc', fixedSpawner(child), (event) => events.push(event), () => {});
    child.stdout.emit('data', `${PROMPT_LINE}{"event":"error","reason":"expired"}\n`);
    child.end(1);
    await expect(promise).resolves.toEqual({ ok: false, reason: 'expired' });
    expect(events).toEqual([EXPECTED_LOGIN_EVENTS[0], { event: 'error', reason: 'expired' }]);
  });

  // Regression for the real defect: a person clicked "Sign in", vc printed a valid prompt line,
  // and it was thrown away because expiresInSeconds was absent — the binary never emits it. The
  // code and URL are what a person acts on; the lifetime only drives a countdown. Losing the
  // countdown must cost the countdown, not the whole login.
  describe('a prompt with no expiresInSeconds (unknown code lifetime)', () => {
    // tests/fixtures-login-prompt-no-expiry.txt is a byte-for-byte capture of a real line printed
    // by `vc login --json` — not a hand-written fixture. Every prior fixture in this file was
    // typed by someone imagining the output, and every one of them included expiresInSeconds,
    // which is exactly how this defect passed every existing test while the product was broken.
    // Read it from disk rather than inlining it so the provenance (a recorded artefact, not an
    // invented literal) stays visible to whoever edits this file next.
    const REAL_PROMPT_LINE_NO_EXPIRY = readFileSync(
      path.join(__dirname, 'fixtures-login-prompt-no-expiry.txt'),
      'utf8',
    );

    it('still surfaces the code and URL — an actionable prompt must not be discarded for a missing lifetime', async () => {
      const child = new FakeChild();
      const opened: string[] = [];
      const events: LoginEvent[] = [];
      const diagnostics: string[] = [];
      const promise = runLogin(
        '/private/vc',
        fixedSpawner(child),
        (event) => events.push(event),
        (url) => opened.push(url),
        (message) => diagnostics.push(message),
      );
      child.stdout.emit('data', REAL_PROMPT_LINE_NO_EXPIRY);
      child.end(0);
      await promise;

      // The unknown lifetime must be a distinguishable absence, not a fabricated number — nothing
      // here invents a value that would show a person a countdown to a moment that means nothing.
      expect(events).toHaveLength(1);
      const [event] = events;
      expect(event).toMatchObject({
        event: 'prompt',
        userCode: 'DJAHWRAF',
        verificationUrl: 'https://auth.makscee.ru/device',
      });
      expect((event as { expiresInSeconds?: number }).expiresInSeconds).toBeUndefined();
      // Still a real, actionable prompt: the URL still gets opened for the person.
      expect(opened).toEqual(['https://auth.makscee.ru/device']);
      // And it is real progress, not a fallback path — it must not also masquerade as a diagnostic.
      expect(diagnostics).toEqual([]);
    });

    // The tolerance is for the missing field only. A prompt still missing what a person actually
    // needs to act — the code, or the place to enter it — stays rejected: loosening the check
    // until everything is accepted would trade one silent failure for another.
    it.each([
      ['no userCode', '{"event":"prompt","verificationUrl":"https://auth.makscee.ru/device"}\n'],
      ['no verificationUrl', '{"event":"prompt","userCode":"DJAHWRAF"}\n'],
    ])('still rejects a prompt with %s, even with expiresInSeconds absent', async (_label, raw) => {
      const child = new FakeChild();
      const events: LoginEvent[] = [];
      const diagnostics: string[] = [];
      const promise = runLogin(
        '/private/vc',
        fixedSpawner(child),
        (event) => events.push(event),
        () => {},
        (message) => diagnostics.push(message),
      );
      child.stdout.emit('data', raw);
      child.end(0);
      await promise;
      expect(events).toEqual([]);
      expect(diagnostics.length).toBeGreaterThan(0);
    });
  });
});
