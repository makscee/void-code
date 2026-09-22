import { createHash } from 'node:crypto';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { Duplex } from 'node:stream';
import { pathToFileURL } from 'node:url';
import { expect } from 'vitest';
// Erased, narrow ABI declarations for the hash-qualified pin. No sibling-clone
// type imports or extra package dependency: every implementation below is loaded
// from the real provisioned runtime and independently exercised by pin controls.
// eslint-disable-next-line @typescript-eslint/no-namespace -- One owned support file; type-only ABI, no namespace implementation.
declare namespace Ai {
  type Api = string;
  interface Model<T extends Api> { id: string; provider: string; api: T; name: string; baseUrl: string; reasoning: boolean; input: ('text' | 'image')[]; cost: { input: number; output: number; cacheRead: number; cacheWrite: number }; contextWindow: number; maxTokens: number }
  interface AssistantMessage { stopReason: string }
  interface AssistantMessageEventStream extends AsyncIterable<{ type: string }> { result(): Promise<AssistantMessage> }
  type StreamFunction = (model: Model<Api>, context: { messages: unknown[]; systemPrompt: string }, options?: { onResponse?: () => void }) => AssistantMessageEventStream;
  class InMemoryCredentialStore { constructor(); }
}
// eslint-disable-next-line @typescript-eslint/no-namespace -- Erased pin ABI; constructors are never implemented in this fixture.
declare namespace Pinned {
  interface Entry { type: string; id: string; parentId: string | null; timestamp: string; customType?: string; data?: unknown }
  class SessionManager {
    static inMemory(cwd: string): SessionManager;
    static open(file: string, directory?: string): SessionManager;
    getSessionId(): string; getLeafId(): string | null; getLeafEntry(): Entry | undefined;
    getBranch(): Entry[]; getEntries(): Entry[]; getEntry(id: string): Entry | undefined;
    getHeader(): Record<string, unknown> | null;
    appendCustomEntry(type: string, data: unknown): string;
    appendMessage(message: { role: string; content: string; timestamp: number }): string;
    appendLabelChange(id: string, label: string): string;
    branch(id: string): void; createBranchedSession(id: string): string | undefined;
  }
  interface ProviderConfig {
    baseUrl?: string; apiKey?: string; api?: string;
    models?: (Omit<Ai.Model<Ai.Api>, 'api' | 'provider' | 'baseUrl'> & { api?: string; baseUrl?: string })[];
    streamSimple?: Ai.StreamFunction;
  }
  class ModelRuntime {
    static create(options: { credentials: unknown; modelsPath: null; allowModelNetwork: boolean; refreshOnCreate: boolean }): Promise<ModelRuntime>;
    getRegisteredProviderConfig(id: string): ProviderConfig | undefined;
    getAvailable(id?: string): Promise<Ai.Model<Ai.Api>[]>;
    hasConfiguredAuth(id: string): boolean; checkAuth(id: string): Promise<unknown>;
  }
  class ModelRegistry {
    constructor(runtime: ModelRuntime);
    getAll(): Ai.Model<Ai.Api>[]; getAvailable(): Ai.Model<Ai.Api>[];
    find(provider: string, id: string): Ai.Model<Ai.Api> | undefined;
  }
  interface ExtensionAPI {
    on(name: string, handler: (event: { type: string; [key: string]: unknown }, ctx: { sessionManager: SessionManager }) => unknown): void;
    registerProvider(id: string, config: ProviderConfig): void;
    setModel(model: Ai.Model<Ai.Api>): Promise<boolean>;
    appendEntry(type: string, data: unknown): void;
  }
  interface SessionStartEvent { type: 'session_start'; reason: 'startup' | 'resume' | 'fork' | 'new' | 'reload'; previousSessionFile?: string }
  interface LoadExtensionsResult { extensions: unknown[]; errors: { path: string; error: string }[]; runtime: unknown }
  interface ResourceLoader {
    getExtensions(): LoadExtensionsResult;
    getSkills(): { skills: unknown[]; diagnostics: unknown[] };
    getPrompts(): { prompts: unknown[]; diagnostics: unknown[] };
    getThemes(): { themes: unknown[]; diagnostics: unknown[] };
    getAgentsFiles(): { agentsFiles: unknown[] };
    getSystemPrompt(): string; getAppendSystemPrompt(): string[];
    getSystemPromptSource(): unknown; getAppendSystemPromptSources(): unknown[];
    extendResources(): void; reload(): Promise<void>;
  }
  class SettingsManager { static inMemory(options: { compaction: { enabled: boolean }; retry: { enabled: boolean } }): SettingsManager; }
  class AgentSession {
    readonly model?: Ai.Model<Ai.Api>; sessionManager: SessionManager;
    settingsManager: SettingsManager; resourceLoader: ResourceLoader;
    extensionRunner: { emit(event: { type: string; reason: string }): Promise<unknown> };
    bindExtensions(options: { mode: string; onError(event: { error: string }): void }): Promise<void>;
    navigateTree(id: string, options: { summarize: boolean }): Promise<unknown>;
    cycleModel(direction: 'forward' | 'backward'): Promise<unknown>;
    reload(): Promise<void>; dispose(): void;
  }
  interface AgentSessionServices { cwd: string; agentDir: string; modelRuntime: ModelRuntime; settingsManager: SettingsManager; resourceLoader: ResourceLoader; diagnostics: unknown[] }
  function createEventBus(): unknown;
  function createAgentSession(options: { cwd: string; agentDir: string; sessionManager: SessionManager; modelRuntime: ModelRuntime; settingsManager: SettingsManager; resourceLoader: ResourceLoader; noTools: 'all'; sessionStartEvent: SessionStartEvent }): Promise<{ session: AgentSession }>;
  class AgentSessionRuntime {
    constructor(session: AgentSession, services: AgentSessionServices, create: (options: { sessionManager: SessionManager; sessionStartEvent?: SessionStartEvent }) => Promise<{ session: AgentSession; extensionsResult: LoadExtensionsResult; services: AgentSessionServices; diagnostics: unknown[] }>);
    readonly session: AgentSession;
    fork(id: string, options: { position: 'before' | 'at' }): Promise<{ cancelled: boolean; selectedText?: string }>;
    newSession(): Promise<{ cancelled: boolean }>;
    switchSession(file: string): Promise<{ cancelled: boolean }>;
    dispose(): Promise<void>;
  }
}
interface LoaderModule {
  createExtensionRuntime(): unknown;
  loadExtensionFromFactory(factory: (pi: Pinned.ExtensionAPI) => void | Promise<void>, cwd: string, bus: unknown, runtime: unknown): Promise<unknown>;
}

export const repo = path.resolve(import.meta.dirname, '../../..');
const localStage = path.join(repo, 'desktop/resources/staged/pi');
export const runtimeRoot = process.env.VC_MODEL_TEST_PI_ROOT ?? (existsSync(localStage) ? localStage : path.resolve(repo, '../../void-code/desktop/resources/staged/pi'));
export const pinRoot = path.join(runtimeRoot, 'node_modules/@earendil-works/pi-coding-agent');
export const nodeFile = process.env.VC_MODEL_TEST_NODE ?? path.resolve(runtimeRoot, '../node/bin/node');
const nativeImport = (file: string): Promise<Record<string, unknown>> => import(/* @vite-ignore */ file);
export const importPin = (file: string): Promise<Record<string, unknown>> => nativeImport(pathToFileURL(path.join(pinRoot, file)).href);
export const pinRequire = createRequire(path.join(pinRoot, 'package.json'));
export const sha = (bytes: string | Buffer): string => createHash('sha256').update(bytes).digest('hex');
export const json = (file: string): Record<string, unknown> => JSON.parse(readFileSync(file, 'utf8'));

export function treeHash(root: string): string {
  const hash = createHash('sha256');
  const visit = (directory: string): void => {
    for (const entry of readdirSync(directory, { withFileTypes: true }).sort((a, b) => a.name < b.name ? -1 : a.name > b.name ? 1 : 0)) {
      const file = path.join(directory, entry.name);
      if (entry.isDirectory()) visit(file);
      else hash.update(path.relative(root, file).split(path.sep).join('/')).update('\0').update(readFileSync(file)).update('\0');
    }
  };
  visit(root);
  return hash.digest('hex');
}

export function assertPins(): void {
  const pins = json(path.join(repo, 'desktop/scripts/resource-pins.json')) as unknown as { pi: Record<string, string>; node: Record<string, string> };
  expect(json(path.join(pinRoot, 'package.json')).version).toBe('0.84.1');
  expect(sha(readFileSync(path.join(repo, 'desktop/runtime/pi/package.json')))).toBe(pins.pi.packageJsonSha256);
  expect(sha(readFileSync(path.join(repo, 'desktop/runtime/pi/package-lock.json')))).toBe(pins.pi.packageLockSha256);
  const lock = json(path.join(repo, 'desktop/runtime/pi/package-lock.json')) as unknown as { packages: Record<string, { integrity: string }> };
  expect(lock.packages['node_modules/@earendil-works/pi-coding-agent'].integrity).toBe(pins.pi.packageIntegrity);
  expect(treeHash(runtimeRoot)).toBe(pins.pi.treeSha256);
  expect(sha(readFileSync(nodeFile))).toBe(pins.node.executableSha256);
  expect(sha(readFileSync(process.execPath)), 'tests must actually run under the pinned executable').toBe(pins.node.executableSha256);
  expect(process.version).toBe('v22.23.1');
  expect(process.versions.undici).toBe('6.27.0');
  expect(json(path.join(pinRoot, 'node_modules/undici/package.json')).version).toBe('8.9.0');
  expect(sha(readFileSync(path.join(pinRoot, 'node_modules/undici/lib/web/fetch/headers.js')))).toBe('0ca9bcb1cb174a19311b27f55b954d53e5adc06d6aa935c42df96c9a4453d0a7');
}

export interface Deferred<T> { promise: Promise<T>; resolve(value: T): void; reject(reason: Error): void }
export function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
export const A = 'fixture-expensive';
export const B = 'fixture-default';
export const F = 'fixture-economy';
export const provider = 'void-codex';
export const NS = 1_000_000_000n;
export const I64 = 9223372036854775807n;
export const config = { schemaVersion: 1, readbackUrl: 'http://fixture.invalid/opaque/readback?fixture=1', pollIntervalSeconds: '17', catalogDecisionTtlSeconds: '120', catalogExpirySkewSeconds: '2' };
export type Model = Ai.Model<Ai.Api>;
export const models: Model[] = [A, B, F].map(id => ({ id, provider, api: 'void-codex-sse', name: id, baseUrl: 'http://fixture.invalid', reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 4096, maxTokens: 512 }));
export const bootstrap = { version: 2, relayUrl: 'http://fixture.invalid', authToken: 'fixture-not-a-credential', providers: [{ kind: 'codex', relayProviderId: 'fixture-route', models: [A, B, F] }], modelDecision: config };
export interface Authority {
  effectiveAssignmentRevision: string; assignmentHeadRevision: string;
  scheduledSuccessor: null | { assignmentRevision: string; effectiveAt: string; tierModelSetDigest: string };
  policyRevision: string; tierId: string; tierModelSetDigest: string; calibrationRevision: string;
  providerGrantSetRevision: string; poolRevision: string; poolCollectionRevision: string;
  controlRevision: string; controlEpoch: string; quotaLatchRevision: string; quotaEpisode: string | null;
  inputFingerprint: string; controlMode: string; quotaState: string; restrictionActive: boolean;
  allowedCodexModelIds: string[]; defaultCodexModelId: string | null; fallbackCodexModelId: string; effectiveCodexModelId: string | null;
}
export interface Decision { schemaVersion: number; generation: string; outcome: string; evaluatedAt: string; validUntil: string; authority: Authority; display?: unknown }
export function decision(generation = '11', restricted = false): Decision {
  return { schemaVersion: 1, generation, outcome: 'catalog', evaluatedAt: '2026-09-16T12:00:00.000000000Z', validUntil: '2026-09-16T12:02:00.000000000Z', authority: {
    effectiveAssignmentRevision: '1', assignmentHeadRevision: '2', scheduledSuccessor: null,
    policyRevision: '3', tierId: 'fixture-tier', tierModelSetDigest: 'fixture-set', calibrationRevision: '4', providerGrantSetRevision: '5', poolRevision: '6', poolCollectionRevision: '7', controlRevision: '8', controlEpoch: '9', quotaLatchRevision: '10', quotaEpisode: restricted ? '1' : null,
    inputFingerprint: `fixture-fingerprint-${generation}`, controlMode: 'active', quotaState: restricted ? 'fallback' : 'normal', restrictionActive: restricted,
    allowedCodexModelIds: restricted ? [F] : [A, B, F], defaultCodexModelId: restricted ? F : B, fallbackCodexModelId: F, effectiveCodexModelId: restricted ? F : B,
  } };
}
// Serialization oracle only: no validation, reducer, permission or policy decisions.
export function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (value !== null && typeof value === 'object') return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonical((value as Record<string, unknown>)[key])}`).join(',')}}`;
  return JSON.stringify(value);
}
export const digest = (domain: 'authority' | 'catalog' | 'legacy', value: unknown): string => `v1:${sha(canonical({ domain, value, version: 1 }))}`;
export type HeaderLine = readonly [string, string];
export const headerNames = ['x-void-model-decision-version', 'x-void-model-decision', 'x-void-quota-state', 'x-void-effective-model', 'x-void-model-decision-generation', 'x-void-quota-policy-revision', 'x-void-quota-episode', 'x-void-model-selection-restricted', 'x-void-allowed-codex-models'] as const;
export function legacy(generation = '12'): HeaderLine[] {
  return [['x-void-quota-state', 'fallback'], ['x-void-effective-model', F], ['x-void-model-decision-generation', generation], ['x-void-quota-policy-revision', '3'], ['x-void-quota-episode', '1'], ['x-void-model-selection-restricted', 'true'], ['x-void-allowed-codex-models', F]];
}
export const encoded = (d: Decision): HeaderLine[] => [['x-void-model-decision-version', '1'], ['x-void-model-decision', Buffer.from(canonical(d)).toString('base64url')]];
export function replaceHeader(lines: HeaderLine[], name: string, values: string[]): HeaderLine[] {
  return [...lines.filter(([key]) => key.toLowerCase() !== name), ...values.map(value => [name, value] as const)];
}
export type HeaderSlot = { kind: 'absent' } | { kind: 'value'; combined: string } | { kind: 'over_limit' | 'unreadable' };
export type HeaderSnapshot = Record<string, HeaderSlot>;
// Controls record observations, not the product's bounded snapshot/codec.
export function observedHeaders(headers: Headers): Record<string, { has: boolean; get: string | null }> {
  return Object.fromEntries(headerNames.map(name => [name, { has: headers.has(name), get: headers.get(name) }]));
}

interface Dispatcher { close(): Promise<void>; destroy(): Promise<void> }
interface FixtureGlobalState {
  previous: Dispatcher;
  previousFetch: typeof globalThis.fetch;
  previousHeaders: typeof globalThis.Headers;
}
const fixtureGlobalState = new WeakMap<Dispatcher, FixtureGlobalState>();
const closedFixtureDispatchers = new WeakSet<Dispatcher>();
const fixtureHosts = new Map<string, HttpFixture[]>();
interface Undici {
  fetch: typeof fetch; Headers: typeof Headers;
  Agent: new (options: { connect: (options: { hostname: string }, callback: (error: Error | null, socket: Duplex | null) => void) => void; connections: number; pipelining: number; maxHeaderSize: number }) => Dispatcher;
  getGlobalDispatcher(): Dispatcher; setGlobalDispatcher(dispatcher: Dispatcher): void;
}
export const undici = pinRequire('undici') as Undici;
export interface HttpReply { headers: HeaderLine[]; body: string; hold?: Deferred<void>; headerHold?: Deferred<void>; status?: number; method?: string; urlPath?: string }
export interface RequestRecord { method: string; path: string; wire: string }
// An actual HTTP/1 parser consumes these bytes. No Response constructor, MockAgent
// header object, socket listener, DNS or product transport replacement is involved.
class FixtureSocket extends Duplex {
  private incoming = Buffer.alloc(0);
  private answered = false;
  constructor(private readonly serve: (request: RequestRecord, socket: FixtureSocket) => void) { super(); }
  _read(): void { /* HTTP response is released by the fixture barriers. */ }
  _write(chunk: Buffer, _encoding: BufferEncoding, done: (error?: Error | null) => void): void {
    this.incoming = Buffer.concat([this.incoming, chunk]);
    const end = this.incoming.indexOf('\r\n\r\n');
    if (!this.answered && end !== -1) {
      const head = this.incoming.subarray(0, end).toString('latin1');
      const size = Number(/\r\ncontent-length:\s*(\d+)/i.exec(head)?.[1] ?? '0');
      if (this.incoming.length >= end + 4 + size) {
        this.answered = true;
        const [method, url] = head.split('\r\n')[0].split(' ');
        queueMicrotask(() => this.serve({ method, path: url, wire: this.incoming.toString('latin1') }, this));
      }
    }
    done();
  }
  ref(): this { return this; }
  unref(): this { return this; }
  setNoDelay(): this { return this; }
  setKeepAlive(): this { return this; }
}
export interface FixtureDiagnostics {
  requests: number;
  gets: number;
  posts: number;
  pending: number;
  replies: number;
  symbols: string[];
}
export class HttpFixture {
  readonly requests: RequestRecord[] = [];
  readonly emitted: string[] = [];
  readonly trace: string[] = [];
  readonly symbolicTrace: string[] = [];
  readonly pending: { request: RequestRecord; socket: FixtureSocket }[] = [];
  private static readonly maxSymbolicEntries = 64;
  readonly replies: HttpReply[] = [];
  readonly dispatcher: Dispatcher;
  private previous: Dispatcher;
  private readonly previousFetch: typeof globalThis.fetch;
  private readonly previousHeaders: typeof globalThis.Headers;
  private readonly ownedFetch: typeof globalThis.fetch;
  private readonly ownedHeaders: typeof globalThis.Headers;
  private restoreReader: () => void;
  private closed = false;
  private waiters: (() => void)[] = [];
  constructor(readonly host = 'fixture.invalid') {
    const owners = fixtureHosts.get(host) ?? [];
    owners.push(this);
    fixtureHosts.set(host, owners);
    this.previous = undici.getGlobalDispatcher();
    const previousFetch = globalThis.fetch;
    const previousHeaders = globalThis.Headers;
    this.previousFetch = previousFetch;
    this.previousHeaders = previousHeaders;
    this.ownedFetch = undici.fetch;
    this.ownedHeaders = undici.Headers;
    const previousRead = ReadableStreamDefaultReader.prototype.read;
    const trace = this.trace;
    const observedRead: typeof previousRead = function (this: ReadableStreamDefaultReader<unknown>) {
      trace.push('body-read-enter');
      return previousRead.call(this).then(result => { if (!result.done) trace.push('body-byte-consumed'); return result; });
    };
    ReadableStreamDefaultReader.prototype.read = observedRead;
    this.restoreReader = () => { if (ReadableStreamDefaultReader.prototype.read === observedRead) ReadableStreamDefaultReader.prototype.read = previousRead; };
    this.dispatcher = new undici.Agent({ connections: 16, pipelining: 0, maxHeaderSize: 300_000, connect: (options, done) => {
      const fixture = fixtureHosts.get(options.hostname)?.at(-1);
      if (!fixture) { done(new Error(`NETWORK FORBIDDEN: ${options.hostname}`), null); return; }
      const socket = new FixtureSocket((request, sock) => fixture.accept(request, sock));
      queueMicrotask(() => done(null, socket));
    } });
    fixtureGlobalState.set(this.dispatcher, { previous: this.previous, previousFetch, previousHeaders });
    undici.setGlobalDispatcher(this.dispatcher);
    // Managed Pi is loaded dynamically and resolves the ambient fetch/Headers
    // bindings at call time. Keep those bindings paired with this fixture's
    // pinned dispatcher; never substitute a production transport.
    globalThis.fetch = this.ownedFetch;
    globalThis.Headers = this.ownedHeaders;
    this.markSymbol('fetch-owner');
  }
  private markSymbol(symbol: string): void { if (this.symbolicTrace.length < HttpFixture.maxSymbolicEntries) this.symbolicTrace.push(symbol); }
  private accept(request: RequestRecord, socket: FixtureSocket): void {
    this.requests.push(request); this.trace.push(`${request.method}:${request.path}`); this.markSymbol(`request:${request.method}`);
    this.pending.push({ request, socket }); this.flush();
    // Unexpected model traffic must drain to an ordinary zero-POST assertion,
    // not hang waiting for a fixture reply and masquerade as a timeout.
    if (request.method === 'POST' && this.pending.some(item => item.socket === socket)) {
      this.replies.push({ method: 'POST', urlPath: request.path, status: 599, headers: [], body: 'fixture-unplanned-model-request' });
      this.flush();
    }
    this.waiters.splice(0).forEach(resolve => resolve());
  }
  diagnostics(): FixtureDiagnostics {
    const bounded = (value: number): number => Math.min(value, 999);
    return { requests: bounded(this.requests.length), gets: bounded(this.gets), posts: bounded(this.posts), pending: bounded(this.pending.length), replies: bounded(this.replies.length), symbols: this.symbolicTrace.slice(-32) };
  }
  enqueue(reply: HttpReply): void { this.replies.push(reply); this.flush(); }
  private flush(): void {
    while (this.pending.length && this.replies.length) {
      const replyIndex = this.replies.findIndex(reply => this.pending.some(({ request }) => (!reply.method || request.method === reply.method) && (!reply.urlPath || request.path === reply.urlPath)));
      if (replyIndex === -1) return;
      const reply = this.replies.splice(replyIndex, 1)[0];
      const requestIndex = this.pending.findIndex(({ request }) => (!reply.method || request.method === reply.method) && (!reply.urlPath || request.path === reply.urlPath));
      const { socket } = this.pending.splice(requestIndex, 1)[0];
      const header = `HTTP/1.1 ${reply.status ?? 200} OK\r\n${reply.headers.map(([k, v]) => `${k}: ${v}\r\n`).join('')}Content-Length: ${Buffer.byteLength(reply.body)}\r\nConnection: close\r\n\r\n`;
      const send = (): void => {
        this.emitted.push(header); this.trace.push('headers-emitted'); this.markSymbol('headers-emitted'); socket.push(Buffer.from(header, 'latin1'));
        const body = (): void => { this.trace.push('body-emitted'); this.markSymbol('body-emitted'); socket.push(Buffer.from(reply.body)); socket.push(null); };
        if (reply.hold) void reply.hold.promise.then(body); else body();
      };
      if (reply.headerHold) void reply.headerHold.promise.then(send); else send();
    }
  }
  async nextRequest(): Promise<void> { await new Promise<void>(resolve => this.waiters.push(resolve)); }
  async headers(lines: HeaderLine[]): Promise<Response> {
    this.enqueue({ headers: lines, body: '' });
    return fetch('http://fixture.invalid/control');
  }
  get posts(): number { return this.requests.filter(r => r.method === 'POST').length; }
  get gets(): number { return this.requests.filter(r => r.method === 'GET').length; }
  async close(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    try {
      this.restoreReader();
      // A fixture may be torn down after another fixture has claimed the globals.
      // Only the current owner may restore them; otherwise an old teardown would
      // close over and replace the newer fixture's dispatcher/fetch path.
      if (undici.getGlobalDispatcher() === this.dispatcher) {
        let restore = this.previous;
        let restoreFetch = this.previousFetch;
        let restoreHeaders = this.previousHeaders;
        while (closedFixtureDispatchers.has(restore)) {
          const state = fixtureGlobalState.get(restore);
          if (!state) break;
          restore = state.previous;
          restoreFetch = state.previousFetch;
          restoreHeaders = state.previousHeaders;
        }
        undici.setGlobalDispatcher(restore);
        if (globalThis.fetch === this.ownedFetch) globalThis.fetch = restoreFetch;
        if (globalThis.Headers === this.ownedHeaders) globalThis.Headers = restoreHeaders;
      }
    } finally {
      const owners = fixtureHosts.get(this.host);
      if (owners) {
        const index = owners.lastIndexOf(this);
        if (index >= 0) owners.splice(index, 1);
        if (owners.length === 0) fixtureHosts.delete(this.host);
      }
      closedFixtureDispatchers.add(this.dispatcher);
      await this.dispatcher.destroy();
    }
  }
}
export async function configurePinFetch(): Promise<void> {
  const m = await importPin('dist/core/http-dispatcher.js');
  (m.configureHttpDispatcher as () => void)();
  const configured = undici.getGlobalDispatcher();
  expect(globalThis.fetch).toBe(undici.fetch);
  expect(globalThis.Headers).toBe(undici.Headers);
  expect('raw' in Headers.prototype).toBe(false);
  expect('getAll' in Headers.prototype).toBe(false);
  await configured.close();
}

export interface Target { protocolVersion: 1; branchContextEpoch: bigint; targetKind: string; safetyGeneration: string | null; inputFingerprint: string | null; authorityDigest: string | null; legacyProjectionDigest: string | null; catalogDigest: string; selectionModelId: string | null }
export interface Token { target: Target; controllerInstanceId: string; planOrdinal: bigint; attemptOrdinal: bigint }
export interface State {
  controllerInstanceId: string; stateRevision: bigint; branchContextEpoch: bigint; lastAllocatedOrdinal: bigint;
  highestSeenGeneration: string | null; authorityStatus: string; acceptedAuthority: Authority | null;
  lastLeaseEvidence: { evaluatedAt: string; validUntil: string } | null; leaseDeadline: bigint | null;
  authoritativeRefreshRequired: boolean; preRestrictionModelId: string | null;
  pendingEffectPlan: Token | null; desiredTarget: Target; selectableModelIds: string[];
  appliedEffects: { status: string; target?: Target }; legacyRestriction: unknown;
}
export interface Event { type: string; [key: string]: unknown }
export interface Envelope { protocolVersion: 1; controllerInstanceId: string; baseStateRevision: bigint; branchContextEpoch: bigint; commitMonoNs: bigint; event: Event; allocation: { kind: 'none' } | { kind: 'apply_pi_catalog'; token: Token } }
export interface Effect { type: string; token?: Token; orderedModels?: Model[]; selectionModelId?: string | null; [key: string]: unknown }
export interface Reducer {
  newClientModelState(options: { controllerInstanceId: string; compatibilityModels: Model[]; config: typeof config; currentModel: Model }): State;
  deriveTransition(state: State, event: Event, now: bigint): { piIntent: { target: Target; orderedModels: Model[]; selectionModelId: string | null } | null };
  reduce(state: State, envelope: Envelope): { state: State; effects: Effect[] };
  parseDecision(input: unknown, models: Model[], configuration: typeof config): { ok: boolean; decision?: Decision };
  parseBootstrap(input: unknown): { ok: boolean };
  canonicalSerialize(value: unknown): string;
  snapshotDecisionHeaders(headers: Pick<Headers, 'get' | 'has'>): HeaderSnapshot;
}
interface Jiti { import(file: string): Promise<Record<string, unknown>> }
export async function productModule(file: string, exports: string[]): Promise<Record<string, unknown>> {
  const full = path.join(repo, 'cmd/vc', file);
  expect(existsSync(full), `MISSING PRODUCT MODULE: cmd/vc/${file} (R5 requires live managed TypeScript authority)`).toBe(true);
  const { createJiti } = pinRequire('jiti') as { createJiti(file: string, options: unknown): Jiti };
  const aliases = Object.fromEntries(['pi-ai', 'pi-coding-agent', 'pi-agent-core', 'pi-tui'].map(name => [`@earendil-works/${name}`, name === 'pi-coding-agent' ? path.join(pinRoot, 'dist/index.js') : path.join(pinRoot, `node_modules/@earendil-works/${name}/dist/${name === 'pi-ai' ? 'compat' : 'index'}.js`)]));
  const module = await createJiti(path.join(pinRoot, 'dist/core/extensions/loader.js'), { moduleCache: false, fsCache: false, alias: aliases }).import(full);
  for (const name of exports) expect(typeof module[name], `MISSING PRODUCT EXPORT: ${file}:${name}`).toBe('function');
  return module;
}
export async function reducer(): Promise<Reducer> { return await productModule('pi_model_decision.ts', ['newClientModelState', 'deriveTransition', 'reduce', 'parseDecision', 'parseBootstrap', 'canonicalSerialize', 'snapshotDecisionHeaders']) as unknown as Reducer; }
export const initial = (r: Reducer, id = 'fixture-controller-A'): State => r.newClientModelState({ controllerInstanceId: id, compatibilityModels: structuredClone(models), config, currentModel: models[0] });
export function observation(state: State, d: unknown, source = 'readback', start = 10n * NS, receipt = 12n * NS): Event {
  return { type: 'observeDecision', bundle: d, source, requestStartedMonoNs: start, responseReceivedMonoNs: receipt, config, sourceRequestId: 'fixture-request', requestControllerInstanceId: state.controllerInstanceId, requestBranchContextEpoch: state.branchContextEpoch };
}
// Only pure reducer tests call this protocol driver. It asks the product's exact
// derive function for intent; it implements no authority/target/permission rules.
export function envelope(r: Reducer, state: State, event: Event, now = 12n * NS): Envelope {
  const intent = r.deriveTransition(state, event, now).piIntent;
  return { protocolVersion: 1, controllerInstanceId: state.controllerInstanceId, baseStateRevision: state.stateRevision, branchContextEpoch: state.branchContextEpoch, commitMonoNs: now, event,
    allocation: intent ? { kind: 'apply_pi_catalog', token: { target: intent.target, controllerInstanceId: state.controllerInstanceId, planOrdinal: state.lastAllocatedOrdinal + 1n, attemptOrdinal: state.lastAllocatedOrdinal + 1n } } : { kind: 'none' } };
}
export const commit = (r: Reducer, s: State, e: Event, now = 12n * NS): State => r.reduce(s, envelope(r, s, e, now)).state;
export const observe = (r: Reducer, s: State, d: unknown, now = 12n * NS): State => commit(r, s, observation(s, d), now);
export function acknowledge(r: Reducer, s: State, now = 12n * NS): State {
  expect(s.pendingEffectPlan, 'pure test requires a real product-derived plan').not.toBeNull();
  return commit(r, s, { type: 'piEffectsSucceeded', effectPlanToken: s.pendingEffectPlan }, now);
}
export function assertClosed(s: State): void { expect(s.selectableModelIds).toEqual([]); expect(s.appliedEffects.status === 'applied' && s.appliedEffects.target?.targetKind === 'active_catalog').toBe(false); }


export async function pinned(): Promise<typeof Pinned> { return await importPin('dist/index.js') as unknown as typeof Pinned; }
export async function loader(): Promise<LoaderModule> { return await importPin('dist/core/extensions/loader.js') as unknown as LoaderModule; }
export interface Trace { kind: string; state?: State; envelope?: Envelope; event?: Event; effect?: Effect; token?: Token; [key: string]: unknown }
export interface Controller {
  snapshot(): State;
  whenIdle(): Promise<void>;
  requestReadback(): Promise<void>;
  retryPiEffects(target: Target): Promise<void>;
  isSelectable(provider: string, id: string): boolean;
}
export interface Product extends Record<string, unknown> {
  default(pi: Pinned.ExtensionAPI, options: unknown): void | Promise<void>;
  getModelDecisionController(pi: Pinned.ExtensionAPI): Controller;
  parseBootstrap(input: unknown): { ok: boolean; bootstrap?: unknown };
  streamVoidCodex: Ai.StreamFunction;
}
export async function managed(): Promise<Product> { return await productModule('pi_extension.ts', ['default', 'getModelDecisionController', 'parseBootstrap', 'streamVoidCodex']) as Product; }
export type Fault = 'register_invalid' | 'false' | 'throw' | 'reject_before' | 'reject_after' | 'native_false' | 'native_reject';
export class MethodBoundary {
  fault: Fault | null = null;
  holdSet: Deferred<void> | null = null;
  entered = deferred<void>();
  readonly calls: { method: string; model?: string; ids?: string[]; snapshot?: State; snapshotFrozen?: boolean }[] = [];
  currentSnapshot?: () => State;
  onMethod?: (name: string) => void;
  active = 0;
  maximumActive = 0;
  wrap(api: Pinned.ExtensionAPI, runtime: Pinned.ModelRuntime): Pinned.ExtensionAPI {
    const register = api.registerProvider.bind(api);
    const set = api.setModel.bind(api);
    const wrapped = Object.create(api) as Pinned.ExtensionAPI;
    wrapped.registerProvider = ((id: string, input: Pinned.ProviderConfig): void => {
      this.onMethod?.('register-entry');
      this.maximumActive = Math.max(this.maximumActive, this.active + 1);
      const state = this.currentSnapshot?.();
      this.calls.push({ method: 'register', ids: input.models?.map(m => m.id), snapshot: state && structuredClone(state), snapshotFrozen: state && Object.isFrozen(state) });
      if (this.fault === 'register_invalid') {
        this.fault = null;
        register(id, { ...input, streamSimple: input.streamSimple ?? runtime.getRegisteredProviderConfig(id)?.streamSimple, api: undefined }); // real synchronous validation, including partial registrations
        return;
      }
      register(id, input);
    }) as Pinned.ExtensionAPI['registerProvider'];
    wrapped.setModel = (model): Promise<boolean> => {
      this.onMethod?.('set-entry');
      const state = this.currentSnapshot?.();
      this.calls.push({ method: 'set', model: model.id, snapshot: state && structuredClone(state), snapshotFrozen: state && Object.isFrozen(state) });
      const fault = this.fault; this.fault = null;
      this.entered.resolve();
      if (fault === 'throw') throw new Error('fixture-method-fault');
      if (fault === 'false') return Promise.resolve(false);
      if (fault === 'reject_before') return Promise.reject(new Error('fixture-method-fault'));
      if (fault === 'native_false') {
        const original = runtime.hasConfiguredAuth;
        runtime.hasConfiguredAuth = () => false;
        const result = set(model);
        runtime.hasConfiguredAuth = original;
        return result;
      }
      if (fault === 'native_reject') {
        const original = runtime.checkAuth;
        runtime.checkAuth = async () => undefined;
        const result = set(model);
        runtime.checkAuth = original;
        return result;
      }
      this.active++; this.maximumActive = Math.max(this.active, this.maximumActive);
      const result = set(model);
      return result.then(async value => {
        if (this.holdSet) await this.holdSet.promise;
        if (fault === 'reject_after') throw new Error('fixture-method-fault');
        return value;
      }).finally(() => { this.active--; });
    };
    return wrapped;
  }
}
export interface Rig {
  pi: Pinned.ExtensionAPI;
  session: Pinned.AgentSession;
  runtime: Pinned.ModelRuntime;
  registry: Pinned.ModelRegistry;
  sm: Pinned.SessionManager;
  loadResult: Pinned.LoadExtensionsResult;
  selections: unknown[];
  close(): void;
}
export async function pinRig(factory?: (pi: Pinned.ExtensionAPI, runtime: Pinned.ModelRuntime) => void | Promise<void>, sm?: Pinned.SessionManager, reason: Pinned.SessionStartEvent | 'startup' | 'resume' | 'fork' | 'new' | 'reload' = 'startup'): Promise<Rig> {
  const p = await pinned();
  const l = await loader();
  const ai = await importPin('node_modules/@earendil-works/pi-ai/dist/index.js') as unknown as typeof Ai;
  const runtime = await p.ModelRuntime.create({ credentials: new ai.InMemoryCredentialStore(), modelsPath: null, allowModelNetwork: false, refreshOnCreate: false });
  const manager = sm ?? p.SessionManager.inMemory(repo);
  const extensionRuntime = l.createExtensionRuntime();
  const selections: unknown[] = [];
  let api!: Pinned.ExtensionAPI;
  const extension = await l.loadExtensionFromFactory(async pi => {
    api = pi;
    pi.on('model_select', event => { selections.push(event); });
    await factory?.(pi, runtime);
  }, repo, p.createEventBus(), extensionRuntime);
  let loadResult = { extensions: [extension], errors: [], runtime: extensionRuntime };
  const resourceLoader: Pinned.ResourceLoader = {
    getExtensions: () => loadResult,
    getSkills: () => ({ skills: [], diagnostics: [] }),
    getPrompts: () => ({ prompts: [], diagnostics: [] }),
    getThemes: () => ({ themes: [], diagnostics: [] }),
    getAgentsFiles: () => ({ agentsFiles: [] }),
    getSystemPrompt: () => 'fixture', getAppendSystemPrompt: () => [],
    getSystemPromptSource: () => undefined, getAppendSystemPromptSources: () => [],
    extendResources: () => {},
    reload: async () => {
      const nextRuntime = l.createExtensionRuntime();
      const next = await l.loadExtensionFromFactory(async pi => { api = pi; pi.on('model_select', e => { selections.push(e); }); await factory?.(pi, runtime); }, repo, p.createEventBus(), nextRuntime);
      loadResult = { extensions: [next], errors: [], runtime: nextRuntime };
    },
  };
  const { session } = await p.createAgentSession({ cwd: repo, agentDir: repo, sessionManager: manager, modelRuntime: runtime, settingsManager: p.SettingsManager.inMemory({ compaction: { enabled: false }, retry: { enabled: false } }), resourceLoader, noTools: 'all', sessionStartEvent: typeof reason === 'string' ? { type: 'session_start', reason } : reason });
  await session.bindExtensions({ mode: 'rpc', onError: event => { throw new Error(`PIN RUNNER ERROR: ${event.error}`); } });
  return { get pi() { return api; }, session, runtime, registry: new p.ModelRegistry(runtime), sm: manager, loadResult, selections, close: () => session.dispose() };
}
export function controlConfig(ids = [A, B, F]): Pinned.ProviderConfig {
  return { baseUrl: 'http://fixture.invalid', apiKey: 'fixture-not-a-credential', api: 'void-codex-sse', models: models.filter(m => ids.includes(m.id)) as unknown as Pinned.ProviderConfig['models'], streamSimple: () => { throw new Error('control must never stream'); } };
}
export const registryIds = (rig: Rig): string[] => rig.registry.getAll().filter(m => m.provider === provider).map(m => m.id);
export function requiredModel(rig: Rig, id: string): Ai.Model<Ai.Api> {
  const model = rig.registry.find(provider, id);
  expect(model, `real registry missing ${id}`).toBeDefined();
  return model!;
}
export interface ProductRig extends Rig {
  controller(): Controller; trace: Trace[]; http: HttpFixture; boundary: MethodBoundary;
  now: { value: bigint }; product: Product;
  workerBarrier: { value: Deferred<void> | null }; dequeueBarrier: { value: Deferred<void> | null };
  readback(d: Decision): Promise<void>;
  respondReadback(d: Decision): Promise<void>;
  waitForTrace(predicate: (trace: Trace) => boolean): Promise<void>;
  stream(model: Ai.Model<Ai.Api>, onResponse?: () => void): Promise<Ai.AssistantMessage>;
  shutdown(): Promise<void>;
}
export async function productRig(options: { sm?: Pinned.SessionManager; reason?: Pinned.SessionStartEvent | 'startup' | 'resume' | 'fork' | 'new' | 'reload'; startup?: Decision; compatibility?: Model[]; bootstrapValue?: unknown } = {}): Promise<ProductRig> {
  const product = await managed();
  const http = new HttpFixture();
  const trace: Trace[] = [];
  const traceWaiters: { predicate: (trace: Trace) => boolean; done: Deferred<void> }[] = [];
  const waitForTrace = (predicate: (trace: Trace) => boolean): Promise<void> => {
    const done = deferred<void>(); traceWaiters.push({ predicate, done }); return done.promise;
  };
  const isReadback = (generation: string) => (event: Trace): boolean => event.kind === 'commit' && event.envelope?.event.source === 'readback' && (event.envelope.event.bundle as Decision)?.generation === generation;
  const startupSeen = options.startup ? waitForTrace(isReadback(options.startup.generation)) : Promise.resolve();
  const now = { value: 12n * NS };
  const boundary = new MethodBoundary();
  boundary.onMethod = name => { http.trace.push(name); };
  const workerBarrier = { value: null as Deferred<void> | null };
  const dequeueBarrier = { value: null as Deferred<void> | null };
  let bound!: Pinned.ExtensionAPI;
  if (options.startup) http.enqueue({ method: 'GET', headers: [['content-type', 'application/json']], body: canonical(options.startup) });
  const rig = await pinRig(async (api, runtime) => {
    bound = boundary.wrap(api, runtime);
    await product.default(bound, { modelDecision: {
      bootstrap: structuredClone(options.bootstrapValue ?? bootstrap), compatibilityModels: options.compatibility ?? models,
      nowMonoNs: () => now.value,
      observe: (event: Trace) => {
        trace.push({ ...structuredClone(event), bundleFrozen: Object.isFrozen(event.envelope?.event.bundle), stateFrozen: Object.isFrozen(event.state) }); http.trace.push(event.kind);
        for (let i = traceWaiters.length - 1; i >= 0; i--) if (traceWaiters[i].predicate(event)) traceWaiters.splice(i, 1)[0].done.resolve();
      },
      beforeWorker: () => workerBarrier.value?.promise,
      beforeDequeue: (event: Event) => event.type.startsWith('piEffects') ? dequeueBarrier.value?.promise : undefined,
    } });
    boundary.currentSnapshot = () => product.getModelDecisionController(bound).snapshot();
  }, options.sm, options.reason);
  const controller = (): Controller => product.getModelDecisionController(bound);
  boundary.currentSnapshot = () => controller().snapshot();
  const respondReadback = async (d: Decision): Promise<void> => {
    const seen = waitForTrace(isReadback(d.generation));
    http.enqueue({ method: 'GET', headers: [['content-type', 'application/json']], body: canonical(d) });
    await seen;
  };
  const readback = async (d: Decision): Promise<void> => {
    const pending = http.pending.some(({ request }) => request.method === 'GET');
    const seen = respondReadback(d);
    if (!pending) await controller().requestReadback();
    await seen; await controller().whenIdle();
  };
  const stream = async (model: Ai.Model<Ai.Api>, onResponse?: () => void): Promise<Ai.AssistantMessage> => {
    // Exercise the actual registered provider closure once registration exists;
    // the exported function is only the closed-state fallback before registration.
    const registration = rig.runtime.getRegisteredProviderConfig(provider);
    const streamSimple = registration ? registration.streamSimple! : product.streamVoidCodex;
    const events = streamSimple(model, { messages: [], systemPrompt: 'fixture' }, { onResponse });
    expect(typeof events?.[Symbol.asyncIterator], 'managed stream must return the real event-stream contract').toBe('function');
    expect(typeof events.result, 'managed stream must expose its terminal result').toBe('function');
    for await (const event of events) trace.push({ kind: `stream:${event.type}` });
    return events.result();
  };
  if (options.startup) { await startupSeen; await controller().whenIdle(); }
  return Object.assign(rig, { controller, trace, http, now, product, boundary, workerBarrier, dequeueBarrier, readback, respondReadback, waitForTrace, stream,
    shutdown: async (): Promise<void> => { workerBarrier.value?.resolve(); dequeueBarrier.value?.resolve(); boundary.holdSet?.resolve(); await rig.session.extensionRunner.emit({ type: 'session_shutdown', reason: 'quit' }); rig.close(); await http.close(); } });
}
export async function blocked(rig: ProductRig, candidates: Ai.Model<Ai.Api>[]): Promise<void> {
  const count = rig.http.posts;
  for (const model of candidates) {
    expect(rig.controller().isSelectable(provider, model.id)).toBe(false);
    expect((await rig.stream(model)).stopReason).toBe('error');
  }
  expect(rig.http.posts).toBe(count);
}
export function checkpoint(sm: Pinned.SessionManager, generation = '12', status = 'quarantined', preference: unknown = { preRestrictionModelId: A }): Record<string, unknown> {
  const d = decision(generation);
  const accepted = status === 'active' || status === 'expired';
  return { schemaVersion: 1, branchProof: { sessionId: sm.getSessionId(), parentEntryId: sm.getLeafId(), writeReason: 'safety_commit' }, safety: { highestSeenGeneration: generation, authorityStatus: status, acceptedAuthority: accepted ? d.authority : null, lastLeaseEvidence: accepted ? { evaluatedAt: d.evaluatedAt, validUntil: d.validUntil } : null, legacyRestriction: null }, preference };
}
export async function replacementRuntime(first: Rig, make: (sm: Pinned.SessionManager, event?: Pinned.SessionStartEvent) => Promise<Rig>): Promise<Pinned.AgentSessionRuntime> {
  const p = await pinned();
  const services = (rig: Rig): Pinned.AgentSessionServices => ({ cwd: repo, agentDir: repo, modelRuntime: rig.runtime, settingsManager: rig.session.settingsManager, resourceLoader: rig.session.resourceLoader, diagnostics: [] });
  return new p.AgentSessionRuntime(first.session, services(first), async options => {
    const next = await make(options.sessionManager, options.sessionStartEvent);
    return { session: next.session, extensionsResult: next.loadResult, services: services(next), diagnostics: [] };
  });
}
export const customType = 'void-client-model-state-v1';
export const completedSSE = 'data: {"type":"response.completed","response":{"id":"fixture-response","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}\n\n';
export async function delayedHeaders(rig: ProductRig, lines: HeaderLine[]): Promise<{ deliver(): Promise<void>; release(): Promise<Ai.AssistantMessage> }> {
  const headers = deferred<void>(); const body = deferred<void>(); const receipt = deferred<void>();
  rig.http.enqueue({ method: 'POST', headers: [...lines, ['content-type', 'text/event-stream']], body: completedSSE, headerHold: headers, hold: body });
  const dispatched = rig.http.nextRequest();
  const response = rig.stream(rig.session.model!, () => { receipt.resolve(); });
  await dispatched;
  return { deliver: async () => { headers.resolve(); await receipt.promise; }, release: async () => { headers.resolve(); body.resolve(); return response; } };
}
export async function receiveHeaders(rig: ProductRig, lines: HeaderLine[]): Promise<{ release(): Promise<Ai.AssistantMessage>; response: Promise<Ai.AssistantMessage> }> {
  const body = deferred<void>(); const receipt = deferred<void>(); let received = false;
  rig.http.enqueue({ method: 'POST', headers: [...lines, ['content-type', 'text/event-stream']], body: completedSSE, hold: body });
  const current = rig.session.model!;
  const response = rig.stream(current, () => { received = true; rig.http.trace.push('external-response-hook'); receipt.resolve(); });
  await Promise.race([receipt.promise, response.then(() => { expect(received, 'production stream ended before the required response-header path').toBe(true); })]);
  return { response, release: async () => { body.resolve(); return response; } };
}
