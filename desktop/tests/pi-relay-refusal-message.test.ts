import { existsSync, readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import * as nodePath from 'node:path';
import { pathToFileURL } from 'node:url';
import { describe, expect, it, vi } from 'vitest';
import {
  defaultCodexModel, loadManagedExtension, STUB_PI_PACKAGE_DIR,
  type FakePi, type ProviderConfig, type RelayFetch, type StreamEvent,
} from './fixtures/managed-extension-stubbed';

// Relay's refusal, in the words Relay wrote — second panel on void-code#76 (promoted by the
// advisor), narrowed by the third (C1). The client never refuses a launch over the wallet; Relay
// does, with 402 wallet_daily_charge_required under its BUDGET_ENFORCE switch, and "Pi shows that
// refusal itself". It did — as raw JSON. The managed void-codex provider (streamVoidCodex in
// cmd/vc/pi_extension.go) turned every non-2xx answer into
//
//   Void relay Codex request failed: HTTP 402: {"type":"error","error":{"type":"wallet_daily_charge_required","message":"Balance is not enough for today — message @makscee on Telegram to top up."}}
//
// and Pi shows output.errorMessage as the turn's error.
//
// Round 2 unwrapped `error.message` for every status. Round 3 (C1, 75) found what that broke: Relay
// passes ChatGPT's own error answers through with their status and body (void-relay/src/codex.ts,
// `new Response(stream.readable, { status: upstream.status, ... })`), and Pi's auto-retry is text
// only — AgentSession._isRetryableError hands the message to pi-ai's isRetryableAssistantError,
// which regexes errorMessage for 429|500|502|503|504|overloaded|… The `HTTP 5xx` prefix was what
// made an upstream 5xx retry; unwrapped, a 5xx whose sentence lacks those words stops retrying.
//
// The rule pinned here, from the spec (only Relay's 402 is meant):
//
//  - a 402 whose body is JSON carrying a non-empty string `error.message` is shown as exactly that
//    message — no status line, no JSON;
//  - every other failed answer keeps today's text, `Void relay Codex request failed: HTTP <status>:
//    <body>` — any other status even when its body carries error.message, and a 402 whose body is
//    not JSON, lacks error.message, or carries an empty or non-string one;
//  - a 5xx therefore still reads as retryable to Pi's own classifier, loaded from the pinned pi-ai
//    on disk and called on the very error the provider pushed; the 402 sentences do not.
//
// The bodies are Relay's own (void-relay/src/codex.ts deny paths and src/proxy/budget-block.ts) or
// shaped like ChatGPT's passed-through answers: the Codex door answers `{"error":{"type","message"}}`;
// the 402 frame writer adds a top-level `"type":"error"`; the grant and auth denials answer
// `{"error":"<word>"}`.
//
// Technique: the embedded source runs against stub Pi packages
// (tests/fixtures/managed-extension-stubbed.ts); the provider it registers is driven through its
// own streamSimple with a stubbed fetch that answers with a real Response, and the error event it
// pushes is read back. What this cannot prove: how Pi 0.87.1 lays the message out on screen, or that
// AgentSession's retry loop runs end to end — the classifier is called, the session is not.

const WALLET_DAY_MESSAGE = 'Balance is not enough for today — message @makscee on Telegram to top up.';
// Weekly charging (spec, "Недельное списание (решение 25.09)", part "Relay"): Keys' verdict
// chargeRequired === true makes Relay answer 402 wallet_charge_required with this sentence. The
// unwrap keys on the 402, not on error.type, so the new type needs no new branch — pinned below.
const WALLET_WEEK_MESSAGE = 'Balance is not enough for this week — message @makscee on Telegram to top up.';
const PCT_CAP_MESSAGE = 'Usage limit reached for this period — message @makscee on Telegram.';

// Pi's Responses helpers, as far as a request that fails before streaming needs them.
const responsesHelpers = {
  convertResponsesMessages: () => [{ role: 'user', content: [{ type: 'input_text', text: 'hello' }] }],
  convertResponsesTools: () => [],
  processResponsesStream: () => { throw new Error('a refused request must not be streamed'); },
};

interface Answer { status: number; body: string; contentType?: string }
type TurnError = NonNullable<StreamEvent['error']>;

// Sends one prompt through the managed void-codex provider against a relay that gives `answer`,
// and returns the error event the provider pushed — the one Pi shows the person — together with
// the model it registered (Pi reads its contextWindow before deciding on a retry).
async function refusedTurn(answer: Answer): Promise<{ error: TurnError; requests: number; model: Record<string, unknown> }> {
  let requests = 0;
  const fetch: RelayFetch = async (url) => {
    requests++;
    expect(url).toBe('https://relay.invalid/codex/responses');
    return new Response(answer.body, { status: answer.status, headers: { 'content-type': answer.contentType ?? 'application/json' } });
  };
  const factory = loadManagedExtension({ env: { PI_PACKAGE_DIR: STUB_PI_PACKAGE_DIR }, fetch, responsesHelpers });
  const providers = new Map<string, ProviderConfig>();
  const pi: FakePi = { on: vi.fn(), registerProvider: (id, config) => { providers.set(id, config); } };
  factory(pi, { clipboardIO: { platform: 'darwin', env: {}, piVersion: '0.87.1', writeText: vi.fn() } });
  const provider = providers.get('void-codex');
  expect(provider, `the managed void-codex provider did not register for the granted ${defaultCodexModel()}`).toBeDefined();
  const model = { ...provider!.models[0], provider: 'void-codex', api: provider!.api };
  const stream = provider!.streamSimple(model, { systemPrompt: 'fixture', messages: [{ role: 'user', content: 'hello', timestamp: 1 }] });
  await stream.ended;
  const errors = stream.events.filter((event) => event.type === 'error');
  expect(errors, `the provider pushed ${JSON.stringify(stream.events.map((event) => event.type))}, not one error`).toHaveLength(1);
  expect(errors[0].error?.stopReason).toBe('error');
  return { error: errors[0].error!, requests, model };
}

const json = (value: unknown) => JSON.stringify(value);
const raw = (status: number, body: string) => `Void relay Codex request failed: HTTP ${status}: ${body}`;

// Relay's 402s: the only answers whose sentence is shown bare.
const RELAY_402S: Array<[string, unknown, string]> = [
  ['the Codex door\'s wallet refusal (402 wallet_daily_charge_required)',
    { error: { type: 'wallet_daily_charge_required', message: WALLET_DAY_MESSAGE } }, WALLET_DAY_MESSAGE],
  ['the 402 frame, with its top-level "type":"error"',
    { type: 'error', error: { type: 'wallet_daily_charge_required', message: WALLET_DAY_MESSAGE } }, WALLET_DAY_MESSAGE],
  ['the weekly wallet refusal (402 wallet_charge_required), as Relay frames it',
    { type: 'error', error: { type: 'wallet_charge_required', message: WALLET_WEEK_MESSAGE } }, WALLET_WEEK_MESSAGE],
  ['the percentage cap (402 budget_exceeded)',
    { error: { type: 'budget_exceeded', message: PCT_CAP_MESSAGE } }, PCT_CAP_MESSAGE],
];

// Upstream 5xx answers Relay passes through, each carrying an error.message that holds none of the
// words Pi's classifier looks for — so only the status in the prefix can make them retry. The 503 is
// the fixture round 2 unwrapped.
const UPSTREAM_5XXS: Array<[string, number, unknown]> = [
  ['ChatGPT\'s own 500, passed through', 500,
    { error: { message: 'An error occurred while processing your request.', type: 'server_error', param: null, code: null } }],
  ['a 502 carrying error.message', 502,
    { error: { type: 'bad_gateway', message: 'The upstream answered with something unexpected.' } }],
  ['a 503 carrying error.message', 503,
    { error: { type: 'overloaded', message: 'The relay is busy — try again in a minute.' } }],
  ['a 504 carrying error.message', 504,
    { error: { type: 'gateway_timeout', message: 'The upstream did not answer in time.' } }],
];

describe('Relay\'s 402 reaches the person as the sentence Relay wrote', () => {
  it.each(RELAY_402S)('%s', async (_label, body, message) => {
    const { error, requests } = await refusedTurn({ status: 402, body: json(body) });
    expect(requests).toBe(1);
    expect(error.errorMessage, 'the person is shown something other than Relay\'s sentence').toBe(message);
    expect(error.errorMessage, 'raw JSON reached the person').not.toContain('{"type":');
    expect(error.errorMessage).not.toContain('{');
    expect(error.errorMessage, 'the transport status line reached the person').not.toMatch(/HTTP \d{3}/);
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
    expect(error.errorMessage).toBe(raw(status, body));
  });

  // C1: error.message is unwrapped for Relay's 402 only. Any other status carrying one — an upstream
  // answer Relay passed through, or one of its own — keeps its status line.
  it.each([
    ...UPSTREAM_5XXS,
    ['ChatGPT\'s 400 for a bad request', 400, { error: { type: 'invalid_request_error', message: 'Unsupported parameter: max_output_tokens' } }],
    ['a 403 whose error is an object with a message', 403, { error: { type: 'forbidden', message: 'This account cannot use this model.' } }],
    ['ChatGPT\'s 429 usage limit', 429, { error: { type: 'usage_limit_reached', message: 'The usage limit has been reached.' } }],
  ] as Array<[string, number, unknown]>)('%s — any status but 402 keeps its status line, even with error.message', async (_label, status, body) => {
    const { error } = await refusedTurn({ status, body: json(body) });
    expect(error.errorMessage, `HTTP ${status} was unwrapped — only Relay's 402 is shown bare`).toBe(raw(status, json(body)));
  });
});

// ---------------------------------------------------------------------------------------------
// Pi's own classifier, from the pinned pi-ai on disk — called, not copied.
//
// Where the tree comes from, in order:
//  1. desktop/runtime/pi — the repo's pin, installed by desktop/scripts/provision-pinned-pi-smoke.sh
//     (`npm ci --prefix runtime/pi`) before the desktop suite runs in CI;
//  2. ~/.void-code/runtime/pi — the runtime an installed Void Code unpacks, accepted only when its
//     package-lock.json is byte for byte the repo's.
// Either way the installed pi-coding-agent and pi-ai must carry the versions the repo's lock names.
// Neither present is a failure, not a skip: a skipped classifier check reads as a passed one.
//
// The import is the one AgentSession makes — `@earendil-works/pi-ai/compat` — resolved through
// pi-ai's own exports map from where the pinned pi-coding-agent sits.

const PIN_ROOT = nodePath.resolve('runtime/pi');
const PIN_LOCK = readFileSync(nodePath.join(PIN_ROOT, 'package-lock.json'));
const PIN_PACKAGES = (JSON.parse(PIN_LOCK.toString('utf8')) as { packages: Record<string, { version?: string }> }).packages;
const AGENT_KEY = 'node_modules/@earendil-works/pi-coding-agent';
// The path the lock gives pi-ai, rather than one written here (it nests under pi-coding-agent).
const PI_AI_KEY = Object.keys(PIN_PACKAGES).find((key) => key.startsWith(AGENT_KEY) && key.endsWith('/@earendil-works/pi-ai'))
  ?? Object.keys(PIN_PACKAGES).find((key) => key.endsWith('node_modules/@earendil-works/pi-ai'));

interface PinnedPi {
  root: string;
  agentDir: string;
  isRetryableAssistantError(message: unknown): boolean;
  isContextOverflow(message: unknown, contextWindow: number): boolean;
}

function readVersion(dir: string): string | undefined {
  try { return (JSON.parse(readFileSync(nodePath.join(dir, 'package.json'), 'utf8')) as { version?: string }).version; } catch { return undefined; }
}

let pinned: Promise<PinnedPi> | undefined;
function pinnedPi(): Promise<PinnedPi> {
  pinned ??= (async () => {
    expect(PI_AI_KEY, 'runtime/pi/package-lock.json names no @earendil-works/pi-ai').toBeDefined();
    const candidates = [PIN_ROOT, nodePath.join(homedir(), '.void-code', 'runtime', 'pi')];
    const refused: string[] = [];
    for (const root of candidates) {
      const agentDir = nodePath.join(root, AGENT_KEY);
      const piAiDir = nodePath.join(root, PI_AI_KEY!);
      if (!existsSync(nodePath.join(piAiDir, 'package.json'))) { refused.push(`${root}: no ${PI_AI_KEY}`); continue; }
      if (root !== PIN_ROOT) {
        const lock = nodePath.join(root, 'package-lock.json');
        if (!existsSync(lock) || !readFileSync(lock).equals(PIN_LOCK)) { refused.push(`${root}: package-lock.json is not the repo's`); continue; }
      }
      const agentVersion = readVersion(agentDir); const piAiVersion = readVersion(piAiDir);
      if (agentVersion !== PIN_PACKAGES[AGENT_KEY]?.version || piAiVersion !== PIN_PACKAGES[PI_AI_KEY!]?.version) {
        refused.push(`${root}: installed pi-coding-agent ${agentVersion} / pi-ai ${piAiVersion}, lock pins ${PIN_PACKAGES[AGENT_KEY]?.version} / ${PIN_PACKAGES[PI_AI_KEY!]?.version}`);
        continue;
      }
      const piAiPackage = JSON.parse(readFileSync(nodePath.join(piAiDir, 'package.json'), 'utf8')) as { exports?: Record<string, { import?: string }> };
      const compatEntry = piAiPackage.exports?.['./compat']?.import;
      expect(compatEntry, `pi-ai ${piAiVersion} exports no ./compat for import`).toBeDefined();
      const compat = await import(/* @vite-ignore */ pathToFileURL(nodePath.join(piAiDir, compatEntry!)).href) as Partial<PinnedPi>;
      expect(typeof compat.isRetryableAssistantError, 'the pinned pi-ai no longer exports isRetryableAssistantError').toBe('function');
      expect(typeof compat.isContextOverflow, 'the pinned pi-ai no longer exports isContextOverflow').toBe('function');
      return { root, agentDir, isRetryableAssistantError: compat.isRetryableAssistantError!, isContextOverflow: compat.isContextOverflow! };
    }
    throw new Error(`the pinned Pi is not on disk, so Pi's retry classifier cannot be called — `
      + `run \`npm ci --prefix runtime/pi --ignore-scripts --no-audit --no-fund\` in desktop/. Looked in:\n  ${refused.join('\n  ')}`);
  })();
  return pinned;
}

// What AgentSession does with a failed turn, read from the pinned source itself, not assumed.
function retryDecisionSource(agentDir: string): { imports: string; body: string } {
  const source = readFileSync(nodePath.join(agentDir, 'dist', 'core', 'agent-session.js'), 'utf8');
  const imports = source.match(/import \{([^}]*)\} from "@earendil-works\/pi-ai\/compat";/)?.[1] ?? '';
  const body = source.match(/\n {4}_isRetryableError\(message\) \{([\s\S]*?)\n {4}\}/)?.[1] ?? '';
  return { imports, body };
}

describe('Pi still retries an upstream 5xx — its own classifier, from the pinned pi-ai, reads the text', () => {
  it('the classifier called here is the one AgentSession\'s auto-retry calls', async () => {
    const pi = await pinnedPi();
    const { imports, body } = retryDecisionSource(pi.agentDir);
    expect(imports, 'agent-session.js does not import isRetryableAssistantError from @earendil-works/pi-ai/compat').toMatch(/\bisRetryableAssistantError\b/);
    expect(imports, 'agent-session.js does not import isContextOverflow from @earendil-works/pi-ai/compat').toMatch(/\bisContextOverflow\b/);
    expect(body, 'could not find AgentSession._isRetryableError in the pinned agent-session.js').not.toBe('');
    expect(body, `_isRetryableError is no longer "not an overflow, then isRetryableAssistantError":${body}`)
      .toMatch(/if \(isContextOverflow\(message, [^)]*\)\)\s*return false;\s*return isRetryableAssistantError\(message\);/);
  });

  it.each(UPSTREAM_5XXS)('%s is retryable', async (_label, status, body) => {
    const pi = await pinnedPi();
    const { error, model } = await refusedTurn({ status, body: json(body) });
    const contextWindow = Number(model.contextWindow ?? 0);
    expect(pi.isContextOverflow(error, contextWindow), `Pi would hand "${error.errorMessage}" to compaction, not retry`).toBe(false);
    expect(pi.isRetryableAssistantError(error), `Pi's classifier does not retry "${error.errorMessage}" — an upstream ${status} would fail the turn outright`).toBe(true);
  });

  it.each(RELAY_402S)('%s is not retryable — asking again cannot top up the wallet', async (_label, body, message) => {
    const pi = await pinnedPi();
    const { error } = await refusedTurn({ status: 402, body: json(body) });
    expect(error.errorMessage).toBe(message);
    expect(pi.isRetryableAssistantError(error), `Pi's classifier would retry "${error.errorMessage}"`).toBe(false);
  });
});
