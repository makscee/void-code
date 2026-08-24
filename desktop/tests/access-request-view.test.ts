import { describe, expect, it } from 'vitest';
import * as authView from '../src/renderer/auth-view';

// The decision behind the "Request access" button and the line beside it, kept in auth-view.ts for
// the reason that module already states: it has no DOM dependency, so it can be called instead of
// pattern-matched as text. offersSignIn lives there for exactly this reason, and the comment above
// it names the failure mode — a rule written inline in renderAuthScreens is a rule no unit test can
// reach, so the next state added to the union silently gets the wrong answer.
//
// WHAT THIS FILE CANNOT PROVE: nothing here reaches a Relay. The route behind `vc access-request`
// is in void-relay's main and is not deployed; the `access_requests` migration is not applied on
// production and applying it is outside our gate. Every input below is an object this test built.
// So this pins how the four answers are told apart, not that any of them can be obtained.
//
// The four the person has to be able to tell apart:
//
//   не подавал         → the button is available
//   подана             → the button is not, and the submission date is beside it
//   отклонена          → said out loud, not by an unchanged screen
//   не смогли спросить → its own sentence, and NOT the first one
//
// The last line is the whole point. vc splits those outcomes on purpose (signed_out,
// invalid_credential, unavailable are three separate words in cmd/vc/access_request.go, and the
// contract block there explains why there is one field and not two). If this mapper folds a failed
// read into "you have not asked yet", the person presses the button, the same failure happens
// silently, and the conclusion available to them is that they are doing it wrong.

type Report = { state: string; requestedAt?: string; resolvedAt?: string };
type Result = { ok: true; report: Report } | { ok: false; reason: string };
type View = { kind: string; canAsk: boolean; text: string };
type Describe = (result: Result | null) => View;

function describeAccessRequest(result: Result | null): View {
  const fn = (authView as unknown as { describeAccessRequest?: Describe }).describeAccessRequest;
  if (typeof fn !== 'function') {
    throw new Error(
      'src/renderer/auth-view.ts exports no describeAccessRequest(result) — the screen has no tested place to decide what the four answers look like, so the decision can only be inline in renderAuthScreens where nothing reaches it',
    );
  }
  return fn(result);
}

const ok = (report: Report): Result => ({ ok: true, report });
const REQUESTED_AT = '2026-08-22T09:15:00.000Z';
const RESOLVED_AT = '2026-08-23T11:00:00.000Z';

const NOT_REQUESTED = ok({ state: 'not_requested' });
const OPEN = ok({ state: 'open', requestedAt: REQUESTED_AT });
const DECLINED = ok({ state: 'declined', requestedAt: REQUESTED_AT, resolvedAt: RESOLVED_AT });
const GRANTED = ok({ state: 'granted', requestedAt: REQUESTED_AT, resolvedAt: RESOLVED_AT });

// Everything that means "we did not get an answer", from both halves of the boundary: the states
// vc names, and the ways the run itself can fail before vc names anything.
const COULD_NOT_ASK: [string, Result][] = [
  ['vc: unavailable', ok({ state: 'unavailable' })],
  ['vc: signed_out', ok({ state: 'signed_out' })],
  ['vc: invalid_credential', ok({ state: 'invalid_credential' })],
  ['read failed: exit_nonzero', { ok: false, reason: 'exit_nonzero' }],
  ['read failed: empty_output', { ok: false, reason: 'empty_output' }],
  ['read failed: invalid_json', { ok: false, reason: 'invalid_json' }],
  ['read failed: invalid_state', { ok: false, reason: 'invalid_state' }],
];

describe('the four answers stay four', () => {
  it('"nothing filed" is the one state that offers the button', () => {
    const view = describeAccessRequest(NOT_REQUESTED);
    expect(view.kind).toBe('not_requested');
    expect(view.canAsk, 'the person has filed nothing and cannot file anything — the screen is a dead end again').toBe(true);
  });

  it('"filed" withholds the button and puts the submission date beside it', () => {
    const view = describeAccessRequest(OPEN);
    expect(view.kind).toBe('open');
    expect(view.canAsk, 'the button stays live on a request that is already open — every press is another row in the queue for one person').toBe(false);
    const when = new Date(REQUESTED_AT);
    expect(view.text, 'a filed request says nothing at all — indistinguishable on screen from having filed nothing').not.toBe('');
    expect(view.text, `the submission date is missing from ${JSON.stringify(view.text)} — "waiting" with no date cannot be told from "waiting" since five minutes ago`)
      .toMatch(new RegExp(`(?<!\\d)0?${when.getDate()}(?!\\d)`));
    expect(view.text, `the year is missing from ${JSON.stringify(view.text)}`).toContain(String(when.getFullYear()));
  });

  it('the date is read from the request, not baked into the sentence', () => {
    const january = describeAccessRequest(ok({ state: 'open', requestedAt: '2026-01-03T09:15:00.000Z' }));
    const august = describeAccessRequest(OPEN);
    expect(january.text, 'two requests filed seven months apart produce the same sentence — the date is a literal').not.toBe(august.text);
  });

  it('"declined" is said out loud', () => {
    // The state the screen is most likely to swallow: nothing needs to change visually for the
    // code to "work", and a person left on an unchanged screen goes on waiting for an answer that
    // already came.
    const view = describeAccessRequest(DECLINED);
    expect(view.kind).toBe('declined');
    expect(view.text, 'a declined request is reported by saying nothing').not.toBe('');
    expect(view.text, `a declined request does not say so: ${JSON.stringify(view.text)}`).toMatch(/declin|turned down|not granted|refus/i);
    expect(view.canAsk, 'the button re-files after a decision has been made — the client cannot promise the queue accepts that, and pressing it hands the person a loop instead of an answer').toBe(false);
  });

  it('"could not ask" is its own answer, and never the first one', () => {
    const nothingFiled = describeAccessRequest(NOT_REQUESTED);
    for (const [label, result] of COULD_NOT_ASK) {
      const view = describeAccessRequest(result);
      expect(view.kind, `${label} was mapped to ${JSON.stringify(view.kind)} — a failed read is not a state of the request`).toBe('unknown');
      expect(view.text, `${label} produces no sentence, so the screen looks exactly as it does when nothing was ever filed`).not.toBe('');
      expect(view.text, `${label} says the same thing as "nothing filed": ${JSON.stringify(view.text)}`).not.toBe(nothingFiled.text);
      expect(view.text, `${label} claims no request exists, which is not something anyone learned: ${JSON.stringify(view.text)}`)
        .not.toMatch(/\b(haven't|have not|has not|hasn't)\s+(yet\s+)?(been\s+)?(request|ask|file)/i);
      expect(view.text, `${label} does not say the answer is unknown: ${JSON.stringify(view.text)}`)
        .toMatch(/couldn't|could not|can't|cannot|unable|no answer|unknown|didn't|did not|don't know|do not know|not sure/i);
    }
  });

  it('a state nobody defined lands on "could not ask", not on "nothing filed"', () => {
    // main/access-request.ts whitelists, so this should be unreachable — which is exactly why it
    // is worth pinning: an unreachable branch that defaults the wrong way is invisible until the
    // day it is reachable, and the wrong default here is the one that blames the person.
    const view = describeAccessRequest(ok({ state: 'pending_review' }));
    expect(view.kind, 'an unknown state was read as "nothing has been filed"').not.toBe('not_requested');
    expect(view.kind).toBe('unknown');
  });

  it('the answer that has not arrived yet claims nothing in either direction', () => {
    // The gap between the screen appearing and the read returning. Both wrong answers are on the
    // table here: "nothing filed" invites a press that duplicates a request that already exists,
    // and "we could not ask" reports a failure that has not happened.
    const view = describeAccessRequest(null);
    expect(view.kind, 'a read still in flight is reported as a state of the request').toBe('pending');
    expect(view.text, 'a read still in flight puts a sentence on screen about something nobody has learned').toBe('');
    expect(view.canAsk, 'the button is live before anything is known — the first press can duplicate a request that is already open').toBe(false);
  });

  it('"granted" does not offer to ask again', () => {
    // The screen is supposed to be gone by then, but the two reads are separate calls and this
    // one can land first.
    const view = describeAccessRequest(GRANTED);
    expect(view.kind).toBe('granted');
    expect(view.canAsk, 'access has been granted and the screen still offers to ask for it').toBe(false);
    expect(view.text, 'the answer arrived and the screen says nothing about it').not.toBe('');
  });

  it('every kind gets an answer to "may I press the button" and none of them throws', () => {
    const inputs: (Result | null)[] = [null, NOT_REQUESTED, OPEN, DECLINED, GRANTED, ...COULD_NOT_ASK.map(([, result]) => result)];
    for (const input of inputs) {
      const view = describeAccessRequest(input);
      expect(typeof view.canAsk, `canAsk is not a boolean for ${JSON.stringify(input)}`).toBe('boolean');
      expect(typeof view.text, `text is not a string for ${JSON.stringify(input)}`).toBe('string');
    }
  });
});

describe('a broken date is not a sentence a person should ever read', () => {
  const BROKEN: [string, Report][] = [
    ['no requestedAt at all', { state: 'open' }],
    ['a requestedAt that is not a date', { state: 'open', requestedAt: 'whenever' }],
    ['an empty requestedAt', { state: 'open', requestedAt: '' }],
    ['a declined request with no dates', { state: 'declined' }],
  ];

  for (const [label, report] of BROKEN) {
    it(`says something true when the request arrives with ${label}`, () => {
      // `new Date(undefined).toLocaleDateString()` is "Invalid Date" and it renders. It reads as a
      // bug in the app rather than as a missing field, and it displaces the one sentence the
      // person needed.
      const view = describeAccessRequest(ok(report));
      for (const junk of ['Invalid Date', 'NaN', 'undefined', 'null']) {
        expect(view.text.includes(junk), `${label} rendered as ${JSON.stringify(view.text)}`).toBe(false);
      }
      expect(view.text, `${label} produced an empty line`).not.toBe('');
      expect(view.kind, `${label} changed which state the screen believes it is in`).toBe(report.state);
    });
  }
});

describe('the sentences obey the rules the rest of the sign-in copy already lives under', () => {
  // Same two patterns tests/no-operator-copy.test.ts applies to every other string that reaches a
  // person, restated here because this copy is generated at runtime from a state word: it exists
  // as no literal that the whole-file scans in that test would find. describeLoginFailure has its
  // own case there for exactly this reason.
  const HUMAN_HANDOFF =
    /\b(operator|administrator|admin|developer)\b|\b(support|it)\s+(team|staff|department|desk)\b|\b(ask|find|contact|call|reach out to)\s+(someone|a person|your\s+(?:it|admin|support))\b/i;
  const TERMINAL_INSTRUCTION =
    /\b(open|use|run|start|type)\s+(a\s+|the\s+|your\s+)?(terminal|shell|command(?:\s+(?:line|prompt))?)\b|\brun\s+(a|this|the)\s+command\b/i;

  // What the product has not decided. Same list tests/access-not-granted-screen.test.ts applies to
  // this screen's static markup — the runtime line sits inside that same panel, and a rule that
  // stops at the markup boundary is a rule the next sentence walks straight past.
  const UNDECIDED: [RegExp, string][] = [
    [/\b402\b/, 'a status code, which is not a sentence a person can act on'],
    [/\bsubscri\w*/i, 'a subscription — the server has one row for 72 subjects; this is not what happened'],
    [/\bbudget\b/i, "Relay's misleading wire name for this refusal, about a monthly budget this has nothing to do with"],
    [/\b(pay|paid|payment|billing|invoice|purchase|buy)\b/i, 'money, which nobody has decided is part of this'],
    [/\b(upgrade|plan|tier|trial|quota|credits?)\b/i, 'a product shape that does not exist yet'],
    [/[$€₽]/, 'a currency symbol'],
  ];

  const EVERY_INPUT: [string, Result | null][] = [
    ['pending', null],
    ['not_requested', NOT_REQUESTED],
    ['open', OPEN],
    ['declined', DECLINED],
    ['granted', GRANTED],
    ...COULD_NOT_ASK,
  ];

  it('never hands the person to a human they cannot reach, or to a terminal', () => {
    // vc\'s own sentence for this situation ends "— an operator has to grant it", and it is one
    // copy-paste away from here.
    const found: string[] = [];
    for (const [label, input] of EVERY_INPUT) {
      const { text } = describeAccessRequest(input);
      const handoff = text.match(HUMAN_HANDOFF);
      if (handoff) found.push(`${label}: "${handoff[0]}" — hands the problem to someone the person has no way to reach`);
      const terminal = text.match(TERMINAL_INSTRUCTION);
      if (terminal) found.push(`${label}: "${terminal[0]}" — tells the person to do the thing this app exists to do for them`);
    }
    expect(found).toEqual([]);
  });

  it('names nothing the product has not decided', () => {
    const found: string[] = [];
    for (const [label, input] of EVERY_INPUT) {
      const { text } = describeAccessRequest(input);
      for (const [pattern, why] of UNDECIDED) {
        const hit = text.match(pattern);
        if (hit) found.push(`${label}: "${hit[0]}" — ${why}`);
      }
    }
    expect(found).toEqual([]);
  });

  it('never tells the person their sign-in failed — it worked, and that is the fact this screen exists to keep straight', () => {
    // The old refusal screen said "Sign in to start chatting" to someone who had just signed in
    // successfully; that is the defect the whole screen was rebuilt for. A status line that now
    // reads "not signed in" when the relay is unreachable would restore it one line lower.
    const found: string[] = [];
    for (const [label, input] of EVERY_INPUT) {
      const { text } = describeAccessRequest(input);
      for (const pattern of [/\bexpired\b/i, /\brevoked\b/i, /\bnot signed in\b/i, /\bsigned out\b/i, /\bsign in again\b/i, /\byour sign-?in failed\b/i]) {
        const hit = text.match(pattern);
        if (hit) found.push(`${label}: "${hit[0]}" — not true of someone whose sign-in just succeeded`);
      }
    }
    expect(found).toEqual([]);
  });
});
