import type { AuthSpawner } from './auth-session';

// The main-process end of the "Request access" button: it runs `vc access-request` and turns its
// one JSON object into something a screen may believe.
//
// Separate from auth-session.ts on purpose. `vc status --json` answers "may I in";
// `vc access-request --json` answers "have I asked, and what came back". Two questions with two
// vocabularies — folding the second into readAuthStatus's whitelist would make one function that
// fails loudly for two unrelated reasons.
//
// The seven words below are vc's own, verbatim from the contract block at the top of
// cmd/vc/access_request.go. They stay a whitelist for the same reason isAuthState() next door is
// one: `typeof value.accessRequest === 'string'` would carry a future rename or typo in vc
// straight through to a screen as a state the renderer branches on.
const ACCESS_REQUEST_STATES = ['signed_out', 'not_requested', 'open', 'granted', 'declined', 'invalid_credential', 'unavailable'] as const;
export type AccessRequestState = (typeof ACCESS_REQUEST_STATES)[number];

// Which states are allowed to carry which date. A failed read learned nothing, so it may date
// nothing: "filed on ..." beside "we could not ask" describes a request nobody confirmed exists.
// An open request is by definition unresolved, so a resolution date on one is equally unfounded.
const REQUESTED_AT_STATES: readonly AccessRequestState[] = ['open', 'granted', 'declined'];
const RESOLVED_AT_STATES: readonly AccessRequestState[] = ['granted', 'declined'];

export interface AccessRequestReport { state: AccessRequestState; requestedAt?: string; resolvedAt?: string }
export type AccessRequestResult =
  | { ok: true; report: AccessRequestReport }
  | { ok: false; reason: 'exit_nonzero' | 'empty_output' | 'invalid_json' | 'invalid_state' };

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
function isAccessRequestState(value: unknown): value is AccessRequestState {
  return typeof value === 'string' && (ACCESS_REQUEST_STATES as readonly string[]).includes(value);
}

// An ask is a question, and a question grants nothing: no subscription, no budget, no expiry.
// That rule is enforced here, at the process boundary, by copying the three fields this module
// names and no others. `{ ...parsed }` would be shorter and would hand a screen whatever a future
// vc build — or a proxy that helpfully merges fields into the reply — decided to attach.
function reportFrom(parsed: Record<string, unknown>, state: AccessRequestState): AccessRequestReport {
  const report: AccessRequestReport = { state };
  if (REQUESTED_AT_STATES.includes(state) && typeof parsed.requestedAt === 'string') report.requestedAt = parsed.requestedAt;
  if (RESOLVED_AT_STATES.includes(state) && typeof parsed.resolvedAt === 'string') report.resolvedAt = parsed.resolvedAt;
  return report;
}

// `ask` is the whole difference between looking and filing, and argv is where it is decided. The
// refusal screen re-reads on every appearance and on every "Check again"; if that path carried
// --ask, a window left open would file one request per look and one person would arrive as a
// column of duplicates in the queue.
export function readAccessRequest(vcPath: string, spawn: AuthSpawner, ask: boolean): Promise<AccessRequestResult> {
  return new Promise((resolve) => {
    const child = spawn(vcPath, ask ? ['access-request', '--ask', '--json'] : ['access-request', '--json']);
    // The OS delivers stdout in arbitrary chunks, so the object can arrive split in two; the
    // parse happens once, on exit, against everything that was written.
    let output = '';
    child.stdout.on('data', (chunk) => { output += chunk.toString(); });
    child.on('exit', (code) => {
      if (code !== 0) { resolve({ ok: false, reason: 'exit_nonzero' }); return; }
      const text = output.trim();
      if (text.length === 0) { resolve({ ok: false, reason: 'empty_output' }); return; }
      let parsed: unknown;
      try {
        parsed = JSON.parse(text);
      } catch {
        resolve({ ok: false, reason: 'invalid_json' });
        return;
      }
      if (!isPlainObject(parsed) || !isAccessRequestState(parsed.accessRequest)) { resolve({ ok: false, reason: 'invalid_state' }); return; }
      // vc's `error` sentence stays here. It is written for a terminal ("not logged in — run: vc
      // login"), it has never been through review, and this is the last place that can stop it
      // before a window renders it as copy. The state word is what the screen branches on.
      resolve({ ok: true, report: reportFrom(parsed, parsed.accessRequest) });
    });
  });
}
