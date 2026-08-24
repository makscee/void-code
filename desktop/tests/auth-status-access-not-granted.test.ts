import { EventEmitter } from 'node:events';
import { describe, expect, it } from 'vitest';
import type { AuthChildProcess, AuthSpawner } from '../src/main/auth-session';
import { readAuthStatus } from '../src/main/auth-session';

// vc now separates two answers that used to arrive as one word. Same token, same production
// deployment, two verifying services:
//   legacy → {"authState":"invalid_credential","error":"not logged in"}
//   relay  → {"authState":"access_not_granted","error":"access has not been granted to this
//             account yet — an operator has to grant it"}
//
// This file guards the door that second answer has to come through before any screen can react
// to it. isValidStatusShape() in src/main/auth-session.ts whitelists exactly three words, so the
// fourth is rejected today as a schema regression: readAuthStatus answers
// { ok: false, reason: 'invalid_status' }, the renderer's applyAuthStatus passes null into
// screenForStatus, and the person is shown "Sign in to start chatting". They then sign in
// successfully and land back on the same screen. Nothing in the renderer can fix that — the state
// never reaches it — which is why this file exists separately from the screen tests.

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

// Verbatim, as `vc status --json` prints it against production today — the sentence comes from
// auth.ErrAccessNotGranted in internal/auth/errors.go, echoed by cmd/vc/status_json.go.
const ACCESS_NOT_GRANTED_LINE =
  '{"authState":"access_not_granted","error":"access has not been granted to this account yet — an operator has to grant it"}\n';

async function statusFor(line: string): Promise<Awaited<ReturnType<typeof readAuthStatus>>> {
  const child = new FakeChild();
  const promise = readAuthStatus('/private/vc', fixedSpawner(child));
  child.stdout.emit('data', line);
  child.end(0);
  return promise;
}

describe('readAuthStatus lets the fourth auth state through — the credential worked, access did not', () => {
  it('accepts access_not_granted as a real status, while still rejecting a word nobody defined', async () => {
    // Both halves belong in one test on purpose. The cheapest way to make the first half pass is
    // to stop whitelisting and let any string through (`typeof value.authState === 'string'`),
    // which quietly re-opens the exact hole the existing invalid_status tests were written for:
    // a renamed field or a typo in a future vc build would then reach the UI as an unknown state
    // instead of failing loudly here.
    const accepted = await statusFor(ACCESS_NOT_GRANTED_LINE);
    expect(accepted.ok, 'access_not_granted was rejected as a schema regression — the renderer never learns the credential was fine').toBe(true);
    expect(accepted.ok && accepted.status.authState).toBe('access_not_granted');

    const nonsense = await statusFor('{"authState":"nonsense"}\n');
    expect(nonsense, 'the whitelist was widened into "any string", so a future vc typo would reach the UI as a state instead of failing here').toEqual({ ok: false, reason: 'invalid_status' });
  });

  it('does not let vc\'s refusal sentence itself out of the module — the UI gets a stable word or nothing', async () => {
    // Same rule this module already applies to "not logged in — run: vc login": raw text from vc
    // is not copy. Here it matters twice over. The sentence names an operator, and the sign-in
    // screens are the one place in this product forbidden to send a person looking for a human
    // (tests/no-operator-copy.test.ts) — so a status object carrying it is a loaded gun pointed
    // at any renderer that decides to display `status.reason`.
    const result = await statusFor(ACCESS_NOT_GRANTED_LINE);
    expect(result.ok, 'status was rejected outright, so there is nothing to check for leakage yet').toBe(true);
    const serialised = JSON.stringify(result);
    expect(serialised).not.toContain('operator');
    expect(serialised).not.toContain('has to grant it');
    // Whatever reason word survives must be a machine word (like the existing 'not_authenticated'
    // / 'unknown_error'), not a sentence — a sentence is copy that never went through review.
    const reason = (result.ok ? result.status.reason : undefined) ?? '';
    expect(reason, `reason ${JSON.stringify(reason)} is free text, not a stable machine word`).not.toMatch(/\s/);
  });

  it('carries no identity, budget or reset date out of a refusal — not even one the refusing server volunteered', async () => {
    // The refusal lands *before* identity: vc never got an answer to "who is this", so it does
    // not read the body. A subject echoed back by the same server that just said no is not an
    // identity anyone vouched for, and this is the module that decides what the UI is allowed to
    // believe. Fed a payload carrying one (a future vc build, a proxy that helpfully merges
    // fields), the current field-by-field copy would pass it straight through to a screen.
    const result = await statusFor(
      '{"authState":"access_not_granted","error":"access has not been granted to this account yet — an operator has to grant it","identity":"someone@example.com","pct":0,"resetAt":"2026-09-01T00:00:00.000Z"}\n',
    );
    expect(result.ok, 'status was rejected outright, so the fields cannot be checked yet').toBe(true);
    const status = result.ok ? result.status : undefined;
    expect(status?.identity, 'a refusal carried an identity nobody verified into the UI').toBeUndefined();
    // pct: 0 is the sharper half — copied through, it reads on screen as "0% of your budget
    // used", a confident claim about an account the server refused to talk about.
    expect(status?.pct, 'a refusal carried a budget figure into the UI').toBeUndefined();
    expect(status?.resetAt).toBeUndefined();
  });
});
