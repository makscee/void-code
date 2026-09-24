import { readFileSync } from 'node:fs';
import * as nodePath from 'node:path';
import * as nodeUrl from 'node:url';
import { transformSync } from 'esbuild';
import { expect, vi } from 'vitest';

// The managed Pi extension — the TypeScript embedded in cmd/vc/pi_extension.go — evaluated the way
// tests/fixtures/pi-fullscreen-clipboard.ts does it (same raw-string extraction, esbuild to CJS, an
// injected `process`), but against stub Pi packages instead of the pinned runtime, so the suites
// that use it run on a machine without `npm run setup`.
//
// Most stubs are inert. Three do a little more, because the suites read through them:
//  - `ctx.ui.setWidget` is modelled the way Pi 0.84.1's interactive mode keeps widgets (a map from
//    key to content; `undefined` removes the key), and renderWidget turns a widget into the lines a
//    person reads — a string[] as is, a component factory by calling it and rendering it;
//  - @earendil-works/pi-ai's createAssistantMessageEventStream records what the provider pushes, so
//    a suite can read the error a person is shown (`errorMessage` on the `error` event);
//  - Pi's Responses helpers are served at the vendored path the extension imports when
//    PI_PACKAGE_DIR is set (the desktop's bundled-runtime contract). Dynamic import is lowered to
//    `require` by esbuild for this, so the stub loader sees the request.
//
// What this cannot prove: how the real Pi draws any of it. That needs the pinned runtime.

export type Level = 'info' | 'warning' | 'error';
export interface WidgetOptions { placement?: string }
export interface WidgetComponent { render(width: number): string[]; invalidate?(): void; dispose?(): void }
export type WidgetContent = string[] | ((tui: unknown, theme: unknown) => WidgetComponent);
export interface ExtensionUI {
  notify(message: string, level?: Level): void;
  setWidget(key: string, content: WidgetContent | undefined, options?: WidgetOptions): void;
  setEditorComponent(...args: unknown[]): void;
  readonly theme: unknown;
}
export interface ExtensionContext { mode: string; hasUI: boolean; ui: ExtensionUI }
export type Handler = (event: Record<string, unknown>, ctx: ExtensionContext) => unknown;

export interface StreamEvent { type: string; reason?: string; error?: { stopReason?: string; errorMessage?: string } }
export interface RecordedStream { events: StreamEvent[]; ended: Promise<void> }
export interface ProviderConfig {
  api: string;
  models: Array<Record<string, unknown>>;
  streamSimple: (model: Record<string, unknown>, context: Record<string, unknown>, options?: Record<string, unknown>) => RecordedStream;
}
export interface FakePi {
  on(name: string, handler: Handler): void;
  registerProvider(id: string, config: ProviderConfig): void;
}
export type ExtensionFactory = (pi: FakePi, options?: { clipboardIO: Record<string, unknown> }) => unknown;

export type RelayFetch = (url: string, init: { method?: string; headers?: Record<string, string>; body?: string; signal?: AbortSignal }) => Promise<Response>;

// The key the extension's own clipboard lifecycle docks its (render-nothing) widget under. It
// shares session_start with the launch notice; suites looking for the notice look past it.
export const CLIPBOARD_WIDGET_KEY = 'void-code-fullscreen-clipboard';

// Where the extension looks for Pi's Responses helpers when PI_PACKAGE_DIR is set — the same URL
// arithmetic as openAIResponsesShared() in the embedded source.
export const STUB_PI_PACKAGE_DIR = nodePath.resolve('/stub-pi-runtime');
const RESPONSES_HELPERS_URL = new URL('./vendor/pi-ai/api/openai-responses-shared.js', nodeUrl.pathToFileURL(STUB_PI_PACKAGE_DIR + nodePath.sep)).href;

export function embeddedSource(): string {
  // Same Go raw-string extraction as tests/fixtures/pi-fullscreen-clipboard.ts and
  // scripts/check-bundled-pi-smoke.mjs.
  const go = readFileSync(nodePath.resolve('../cmd/vc/pi_extension.go'), 'utf8');
  const marker = 'const piVoidCodexExtensionSource = `';
  const start = go.indexOf(marker);
  const end = go.indexOf('`', start + marker.length);
  expect(start, 'managed transport source').toBeGreaterThanOrEqual(0);
  expect(end).toBeGreaterThan(start);
  return go.slice(start + marker.length, end);
}

// The model the embedded source registers void-codex with by default, read from the source rather
// than written here. The extension drops every granted model it does not allow and skips
// registration when none is left, so a hardcoded id silently unregisters the provider the moment
// the source retires it: main's #75 swapped gpt-5.6-terra for gpt-6-sol, and on void-code#76's
// merge ref every suite driving void-codex through this fixture failed with "did not register".
export function defaultCodexModel(source: string = embeddedSource()): string {
  const match = /^const CODEX_MODEL_ID = "([^"]+)";$/m.exec(source);
  expect(match, 'the embedded source no longer declares CODEX_MODEL_ID').not.toBeNull();
  return match![1];
}

const forbidden = (what: string) => (): never => { throw new Error(`managed-extension fixture forbids ${what}`); };

function recordingStream(): RecordedStream & { push(event: StreamEvent): void; end(): void } {
  const events: StreamEvent[] = [];
  let finish = (): void => {};
  const ended = new Promise<void>((resolve) => { finish = resolve; });
  return { events, ended, push: (event) => { events.push(event); }, end: () => finish() };
}

// Minimal stand-ins for the pi-tui components a widget factory might build with. They render text
// as lines and nothing else; styling is the theme's business, and the theme below is plain.
class StubText {
  constructor(private text = '') {}
  setText(text: string): void { this.text = text; }
  render(): string[] { return this.text.split('\n'); }
  invalidate(): void {}
}
class StubContainer {
  children: Array<{ render(width: number): string[] }> = [];
  addChild(component: { render(width: number): string[] }): void { this.children.push(component); }
  removeChild(component: { render(width: number): string[] }): void { this.children = this.children.filter((child) => child !== component); }
  clear(): void { this.children = []; }
  render(width: number): string[] { return this.children.flatMap((child) => child.render(width)); }
  invalidate(): void {}
}
class StubSpacer {
  constructor(private lines = 1) {}
  render(): string[] { return Array.from({ length: this.lines }, () => ''); }
  invalidate(): void {}
}

// Every theme call returns its last argument unstyled: fg(color, text) → text, bold(text) → text.
export const plainTheme: unknown = new Proxy({}, {
  get: () => (...args: unknown[]): string => String(args[args.length - 1] ?? ''),
});

export interface LoadOptions {
  env: Record<string, string>;
  fetch?: RelayFetch;
  responsesHelpers?: Record<string, unknown>;
}

export function loadManagedExtension({ env, fetch, responsesHelpers }: LoadOptions): ExtensionFactory {
  const source = embeddedSource();
  const code = transformSync(source, {
    loader: 'ts', format: 'cjs', target: 'node22', logLevel: 'silent',
    // `await import(x)` becomes `require(x)`, so the stub loader below answers it too.
    supported: { 'dynamic-import': false },
  }).code;
  const bootstrap = { version: 1, relayUrl: 'https://relay.invalid', authToken: 'fixture-only', providers: [{ kind: 'codex', relayProviderId: 'fixture', models: [defaultCodexModel(source)] }] };
  const stubRequire = (id: string): unknown => {
    if (id === RESPONSES_HELPERS_URL && responsesHelpers) return responsesHelpers;
    switch (id) {
      case 'node:child_process':
      case 'child_process':
        return { execFileSync: vi.fn(() => JSON.stringify(bootstrap)), spawn: forbidden('native clipboard IO') };
      case 'node:fs':
      case 'fs':
        return { existsSync: () => false, renameSync: forbidden('filesystem IO'), writeFileSync: forbidden('filesystem IO') };
      case 'node:path':
      case 'path':
        return nodePath;
      case 'node:url':
      case 'url':
        return nodeUrl;
      case '@earendil-works/pi-coding-agent':
        return { VERSION: '0.84.1', getPackageDir: () => '/nonexistent/pi' };
      case '@earendil-works/pi-ai':
        return { clampThinkingLevel: (_model: unknown, level: string) => level, createAssistantMessageEventStream: recordingStream };
      case '@earendil-works/pi-tui':
        return {
          isKeyRelease: () => false, matchesKey: () => false,
          Text: StubText, TruncatedText: StubText, Container: StubContainer, Box: StubContainer, Spacer: StubSpacer,
          truncateToWidth: (text: string) => text, visibleWidth: (text: string) => text.length, wrapTextWithAnsi: (text: string) => text.split('\n'),
        };
      default:
        throw new Error(`managed-extension fixture: unexpected import ${id}`);
    }
  };
  const module = { exports: {} as { default?: ExtensionFactory } };
  new Function('require', 'module', 'exports', 'process', 'console', 'fetch', code)(
    stubRequire, module, module.exports,
    { env: { VC_BOOTSTRAP_EXECUTABLE: '/isolated/vc', ...env }, platform: 'darwin', pid: 999 },
    { error: vi.fn(), log: vi.fn(), warn: vi.fn() },
    fetch ?? forbidden('network'),
  );
  const factory = module.exports.default;
  expect(typeof factory, 'the embedded source has no default export').toBe('function');
  return factory as ExtensionFactory;
}

// Pi's widget board: a map from key to what was docked, as interactive-mode.js setExtensionWidget
// keeps it (a new setWidget under a key replaces the old one; `undefined` removes it).
export interface DockedWidget { content: WidgetContent; options?: WidgetOptions }
export interface FakeUI extends ExtensionUI {
  docked: Map<string, DockedWidget>;
  notified: Array<[string, Level | undefined]>;
}
export function fakeUI(): FakeUI {
  const docked = new Map<string, DockedWidget>();
  const notified: Array<[string, Level | undefined]> = [];
  return {
    docked,
    notified,
    notify: (message, level) => { notified.push([message, level]); },
    setWidget: (key, content, options) => {
      if (content === undefined) docked.delete(key);
      else docked.set(key, { content, options });
    },
    setEditorComponent: vi.fn(),
    theme: plainTheme,
  };
}

const ANSI = /\x1b\[[0-9;?]*[A-Za-z]/g; // eslint-disable-line no-control-regex

// The lines a person reads from a docked widget, styling stripped.
export function renderWidget(content: WidgetContent, width = 240): string[] {
  if (Array.isArray(content)) return content.map((line) => String(line).replace(ANSI, ''));
  const component = content({ requestRender: () => {}, terminal: { columns: width, rows: 40 } }, plainTheme);
  try {
    return component.render(width).map((line) => line.replace(ANSI, ''));
  } finally {
    component.dispose?.();
  }
}
