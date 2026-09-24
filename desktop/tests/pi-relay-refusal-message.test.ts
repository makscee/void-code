import { describe, expect, it, vi } from 'vitest';
import {
  loadManagedExtension, STUB_PI_PACKAGE_DIR,
  type FakePi, type ProviderConfig, type RelayFetch, type StreamEvent,
} from './fixtures/managed-extension-stubbed';

// Relay's refusal, in the words Relay wrote — second panel on void-code#76 (promoted by the
// advisor). The client never refuses a launch over the wallet; Relay does, with
// 402 wallet_daily_charge_required under its BUDGET_ENFORCE switch, and "Pi shows that refusal
// itself". It did — as raw JSON. The managed void-codex provider (streamVoidCodex in
// cmd/vc/pi_extension.go) turned every non-2xx answer into
//
//   Void relay Codex request failed: HTTP 402: {"type":"error","error":{"type":"wallet_daily_charge_required","message":"Balance is not enough for today — message @makscee on Telegram to top up."}}
//
// and Pi shows output.errorMessage as the turn's error. The rule pinned here:
//
//  - a non-2xx answer whose body is JSON carrying a non-empty string `error.message` is shown as
//    exactly that message — no status line, no JSON;
//  - every other failed answer (a body that is not JSON, JSON without error.message, an empty or
//    non-string message) keeps today's text, `Void relay Codex request failed: HTTP <status>:
//    <body>`, so nothing is lost for debugging.
//
// The bodies are Relay's own (void-relay/src/codex.ts deny paths and src/proxy/budget-block.ts):
// the Codex door answers `{"error":{"type","message"}}`; the 402 frame writer adds a top-level
// `"type":"error"`; the grant and auth denials answer `{"error":"<word>"}`.
//
// Technique: the embedded source runs against stub Pi packages
// (tests/fixtures/managed-extension-stubbed.ts); the provider it registers is driven through its
// own streamSimple with a stubbed fetch that answers with a real Response, and the error event it
// pushes is read back. What this cannot prove: how Pi 0.84.1 lays the message out on screen.

const WALLET_DAY_MESSAGE = 'Balance is not enough for today — message @makscee on Telegram to top up.';
const PCT_CAP_MESSAGE = 'Usage limit reached for this period — message @makscee on Telegram.';

// Pi's Responses helpers, as far as a request that fails before streaming needs them.
const responsesHelpers = {
  convertResponsesMessages: () => [{ role: 'user', content: [{ type: 'input_text', text: 'hello' }] }],
  convertResponsesTools: () => [],
  processResponsesStream: () => { throw new Error('a refused request must not be streamed'); },
};

interface Answer { status: number; body: string; contentType?: string }

// Sends one prompt through the managed void-codex provider against a relay that gives `answer`,
// and returns the error event the provider pushed — the one Pi shows the person.
async function refusedTurn(answer: Answer): Promise<{ error: StreamEvent['error']; requests: number }> {
  let requests = 0;
  const fetch: RelayFetch = async (url) => {
    requests++;
    expect(url).toBe('https://relay.invalid/codex/responses');
    return new Response(answer.body, { status: answer.status, headers: { 'content-type': answer.contentType ?? 'application/json' } });
  };
  const factory = loadManagedExtension({ env: { PI_PACKAGE_DIR: STUB_PI_PACKAGE_DIR }, fetch, responsesHelpers });
  const providers = new Map<string, ProviderConfig>();
  const pi: FakePi = { on: vi.fn(), registerProvider: (id, config) => { providers.set(id, config); } };
  factory(pi, { clipboardIO: { platform: 'darwin', env: {}, piVersion: '0.84.1', writeText: vi.fn() } });
  const provider = providers.get('void-codex');
  expect(provider, 'the managed void-codex provider did not register').toBeDefined();
  const model = { ...provider!.models[0], provider: 'void-codex', api: provider!.api };
  const stream = provider!.streamSimple(model, { systemPrompt: 'fixture', messages: [{ role: 'user', content: 'hello', timestamp: 1 }] });
  await stream.ended;
  const errors = stream.events.filter((event) => event.type === 'error');
  expect(errors, `the provider pushed ${JSON.stringify(stream.events.map((event) => event.type))}, not one error`).toHaveLength(1);
  expect(errors[0].error?.stopReason).toBe('error');
  return { error: errors[0].error, requests };
}

const json = (value: unknown) => JSON.stringify(value);

describe('Relay\'s refusal reaches the person as the sentence Relay wrote', () => {
  it.each([
    ['the Codex door\'s wallet refusal (402 wallet_daily_charge_required)', 402,
      { error: { type: 'wallet_daily_charge_required', message: WALLET_DAY_MESSAGE } }, WALLET_DAY_MESSAGE],
    ['the 402 frame, with its top-level "type":"error"', 402,
      { type: 'error', error: { type: 'wallet_daily_charge_required', message: WALLET_DAY_MESSAGE } }, WALLET_DAY_MESSAGE],
    ['the percentage cap (402 budget_exceeded)', 402,
      { error: { type: 'budget_exceeded', message: PCT_CAP_MESSAGE } }, PCT_CAP_MESSAGE],
    ['any other status whose body carries error.message', 503,
      { error: { type: 'overloaded', message: 'The relay is busy — try again in a minute.' } }, 'The relay is busy — try again in a minute.'],
  ])('%s', async (_label, status, body, message) => {
    const { error, requests } = await refusedTurn({ status, body: json(body) });
    expect(requests).toBe(1);
    expect(error?.errorMessage, 'the person is shown something other than Relay\'s sentence').toBe(message);
    expect(error?.errorMessage, 'raw JSON reached the person').not.toContain('{"type":');
    expect(error?.errorMessage).not.toContain('{');
    expect(error?.errorMessage, 'the transport status line reached the person').not.toMatch(/HTTP \d{3}/);
  });
});

describe('every other failed answer keeps today\'s text, so nothing is lost for debugging', () => {
  it.each([
    ['a body that is not JSON', 502, 'Bad Gateway', 'text/plain'],
    ['an empty body', 500, '', 'text/plain'],
    ['Relay\'s grant denial — error is a word, not an object', 403, json({ error: 'provider not granted' }), undefined],
    ['Relay\'s auth denial', 401, json({ error: 'unauthorized' }), undefined],
    ['an error object without a message', 402, json({ error: { type: 'budget_exceeded' } }), undefined],
    ['an empty message', 402, json({ error: { type: 'wallet_daily_charge_required', message: '' } }), undefined],
    ['a message that is not a string', 402, json({ error: { type: 'wallet_daily_charge_required', message: 42 } }), undefined],
    ['a message at the top level, not under error', 400, json({ message: 'top-level only' }), undefined],
    ['JSON null', 500, 'null', undefined],
    ['a JSON array', 500, json([{ error: { message: 'inside an array' } }]), undefined],
  ])('%s', async (_label, status, body, contentType) => {
    const { error } = await refusedTurn({ status, body, contentType });
    expect(error?.errorMessage).toBe(`Void relay Codex request failed: HTTP ${status}: ${body}`);
  });
});
