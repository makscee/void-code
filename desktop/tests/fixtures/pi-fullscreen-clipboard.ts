import type { ChildProcess, SpawnOptions } from 'node:child_process';
import type { EventEmitter } from 'node:events';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { transformSync } from 'esbuild';
import { expect, vi } from 'vitest';
import { consumerHooks, referenceFactory } from './pi-interactive-consumer';

export const agentDir = path.resolve('runtime/pi/node_modules/@earendil-works/pi-coding-agent');
export const interactiveFile = path.join(agentDir, 'dist/modes/interactive/interactive-mode.js');
export const actualReference = (getTui: () => TuiView): TuiView => referenceFactory<TuiView>(interactiveFile)(getTui);
const require = createRequire(path.join(agentDir, 'package.json'));
// dist/index.js re-exports VERSION from config.js, which reads pkg.version.
export const agentMetadata = {
  VERSION: JSON.parse(readFileSync(path.join(agentDir, 'package.json'), 'utf8')).version as string,
  getPackageDir: () => agentDir,
};
// Narrow views of the real pinned Pi objects, including private selection seams.
// These describe fixture access only; no selection or keyboard behavior is emulated.
export interface ComponentView {
  render(width: number): string[];
  invalidate(): void;
  handleInput?(data: string): void;
  dispose?(): void;
}
export interface ScrollView extends ComponentView { scrollTo(row: number): void }
export interface TuiView {
  setLayoutRoot(component: ComponentView): void;
  setFocus(component: ComponentView): void;
  focusedComponent: ComponentView | null;
  start(): void;
  stop(options: { preserveScreen: boolean }): void;
  renderNow(): void;
  flash(text: string, duration?: number): void;
  getSelectionBounds(): { start: { row: number; col: number; scrollView: ScrollView }; end: { row: number; col: number; scrollView: ScrollView } } | undefined;
  copySelectionToClipboard: (() => void) | undefined;
  showOverlay(component: ComponentView): { hide(): void };
}
interface PiView {
  TuiAltScreen: new (terminal: MemoryTerminal, hardwareCursor: boolean) => TuiView;
  ScrollView: new (content: ComponentView, options: { primary: boolean; follow: string; scrollbar: string }) => ScrollView;
  Container: new () => ComponentView & { addChild(component: ComponentView): void };
  Text: new (text: string, paddingX: number, paddingY: number) => ComponentView;
  Input: new () => ComponentView & { handleInput(data: string): void; setValue(text: string): void; getValue(): string };
}
export type WidgetOptions = { placement?: string };
export type WidgetContent = string[] | ((tui: TuiView, theme: { fg(color: string, text: string): string }) => ComponentView) | undefined;
export type LifecycleHandler = (event: { reason: string }, ctx: {
  mode: string; hasUI: boolean; ui: {
    setWidget(key: string, content: WidgetContent, options?: WidgetOptions): void;
    notify(message: string, level?: string): void;
    setEditorComponent(): void;
  };
}) => unknown;
export type Spawn = (file: string, args: string[], options: SpawnOptions) => EventEmitter & Pick<ChildProcess, 'stdin' | 'stderr'> & { kill(signal?: NodeJS.Signals | number): unknown };
export async function realPi(): Promise<PiView> {
  expect(JSON.parse(readFileSync(path.join(agentDir, 'package.json'), 'utf8')).version).toBe('0.84.1');
  return import(/* @vite-ignore */ pathToFileURL(require.resolve('@earendil-works/pi-tui')).href);
}
export function embeddedSource(): string {
  // Same Go raw-string extraction used by scripts/check-bundled-pi-smoke.mjs.
  const go = readFileSync(path.resolve('../cmd/vc/pi_extension.go'), 'utf8');
  const marker = 'const piVoidCodexExtensionSource = `';
  const start = go.indexOf(marker);
  const end = go.indexOf('`', start + marker.length);
  expect(start, 'managed transport source fixture').toBeGreaterThanOrEqual(0);
  expect(end).toBeGreaterThan(start);
  return go.slice(start + marker.length, end);
}
export type WriteText = (text: string, signal: AbortSignal) => Promise<void>;
export type ClipboardOptions = { platform: string; env: Record<string, string>; piVersion: string; writeText: WriteText; notify: (message: string, level: string) => void };
export type ExtensionModule = {
  default: (pi: { on(name: string, handler: LifecycleHandler): unknown; registerProvider: (...args: unknown[]) => unknown }, options?: { clipboardIO: Omit<ClipboardOptions, 'notify'> }) => unknown;
  installFullscreenClipboard?: (tui: TuiView, options: ClipboardOptions) => () => void;
  createNativeClipboardWriter?: (options: { platform: string; env: Record<string, string>; spawn: Spawn }) => (text: string, signal?: AbortSignal) => Promise<void>;
};
export const localEnv = { VC_BOOTSTRAP_EXECUTABLE: '/isolated/vc', SystemRoot: 'C:\\Windows' };
export async function extension(env = localEnv, spawn?: Spawn, agentExports: Record<string, unknown> = {}): Promise<ExtensionModule> {
  const tui = await realPi();
  const code = transformSync(embeddedSource(), { loader: 'ts', format: 'cjs', target: 'node22', logLevel: 'silent' }).code;
  const module = { exports: {} };
  const safeRequire = (id: string): unknown => {
    if (id === 'node:child_process' || id === 'child_process') return {
      execFileSync: vi.fn(() => JSON.stringify({ version: 1, relayUrl: 'https://relay.invalid', authToken: 'fixture-only', providers: [{ kind: 'codex', relayProviderId: 'fixture', models: ['gpt-5.6-terra'] }] })),
      spawn: spawn ?? (() => { throw new Error('unit fixture forbids native clipboard IO'); }),
    };
    if (id === '@earendil-works/pi-tui') return tui;
    if (id === '@earendil-works/pi-coding-agent') return { ...agentMetadata, ...agentExports };
    if (id === '@earendil-works/pi-ai') return { clampThinkingLevel: (_m: unknown, level: string) => level };
    if (['node:fs', 'fs', 'node:fs/promises', 'fs/promises'].includes(id)) return new Proxy({
      existsSync: () => false,
      readFileSync: (file: string, options: Parameters<typeof readFileSync>[1]) => {
        if (path.resolve(String(file)) !== path.join(agentDir, 'package.json')) throw new Error('unit fixture permits only pinned package metadata reads');
        return readFileSync(file, options);
      },
    }, {
      get(target, name) { return name in target ? target[name as keyof typeof target] : () => { throw new Error('unit fixture forbids filesystem IO'); }; },
    });
    return require(id);
  };
  new Function('require', 'module', 'exports', 'process', 'console', 'fetch', code)(safeRequire, module, module.exports, { env, platform: 'darwin', pid: 999 }, { error: vi.fn(), log: vi.fn(), warn: vi.fn() }, () => { throw new Error('unit fixture forbids network'); });
  return module.exports as ExtensionModule;
}
export function install(module: ExtensionModule, rig: Rig, overrides: Partial<ClipboardOptions> = {}): () => void {
  expect(module.installFullscreenClipboard, 'R1: managed transport lacks semantic fullscreen native clipboard adapter').toBeTypeOf('function');
  const dispose = module.installFullscreenClipboard!(rig.tui, { platform: 'darwin', env: localEnv, piVersion: '0.84.1', writeText: rig.write, notify: rig.notify, ...overrides });
  rig.disposers.push(dispose);
  return dispose;
}
export function deferred<T = void>(): { promise: Promise<T>; resolve: (value: T) => void; reject: (error: Error) => void } {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
export async function flush(): Promise<void> { for (let i = 0; i < 100; i++) await Promise.resolve(); }
export class MemoryTerminal {
  columns = 80; rows = 8; kittyProtocolActive = false;
  output: string[] = [];
  input: (text: string) => void = () => { throw new Error('TUI not started'); };
  start(input: (text: string) => void): void { this.input = input; }
  stop(): void {}
  async drainInput(): Promise<void> {}
  write = (text: string): void => { this.output.push(text); };
  moveBy(): void {} hideCursor(): void {} showCursor(): void {} clearLine(): void {}
  clearFromCursor(): void {} clearScreen(): void {} setTitle(): void {} setProgress(): void {}
}
export type Rig = Awaited<ReturnType<typeof rig>>;
export async function rig(lines = ['Привет 世界 😀', 'строка два'], pi?: PiView) {
  pi ??= await realPi();
  const terminal = new MemoryTerminal();
  const tui = new pi.TuiAltScreen(terminal, false);
  const content = { render: () => lines, invalidate() {} };
  const scroll = new pi.ScrollView(content, { primary: true, follow: 'none', scrollbar: 'hidden' });
  tui.setLayoutRoot(scroll);
  const focused = { render: () => [], invalidate() {}, handleInput: vi.fn() };
  tui.setFocus(focused);
  tui.start();
  tui.renderNow();
  const flash = vi.spyOn(tui, 'flash'); // call-through, actual Pi display remains live
  const write = vi.fn<WriteText>().mockResolvedValue(undefined);
  const notify = vi.fn();
  const disposers: Array<() => void> = [];
  return { tui, terminal, scroll, lines, flash, write, notify, focused, disposers,
    draw() { tui.renderNow(); },
    drag(from = [0, 0], to = [30, 1], release = true) {
      terminal.input(`\x1b[<0;${from[0] + 1};${from[1] + 1}M`);
      terminal.input(`\x1b[<32;${to[0] + 1};${to[1] + 1}M`);
      if (release) terminal.input(`\x1b[<0;${to[0] + 1};${to[1] + 1}m`);
    },
    close() { disposers.reverse().forEach((dispose) => dispose()); tui.stop({ preserveScreen: true }); },
  };
}
// Pi 0.84.1 InteractiveMode.setExtensionWidget: dispose in BOTH placements before
// invoking a replacement factory; store its result so clearing a probe tears it down.
export async function widgetUI(r: Rig, reference = r.tui, actualConsumer = false) {
  const pi = await realPi();
  const above = new Map<string, ComponentView>(); const below = new Map<string, ComponentView>();
  const theme = { fg: (_: string, text: string) => text };
  const hooks = actualConsumer ? consumerHooks(interactiveFile) : undefined;
  const receiver = { ui: reference, extensionWidgetsAbove: above, extensionWidgetsBelow: below, renderWidgets() {} };
  const consumer = hooks ? new Function('theme', 'Container', 'Text', `return class InteractiveMode { static MAX_WIDGET_LINES = 10; ${[...hooks.methods.values()].join('\n')} }`)(theme, pi.Container, pi.Text).prototype : undefined;
  const setWidget = vi.fn((key: string, content: WidgetContent, options?: WidgetOptions) => {
    if (consumer) return consumer.setExtensionWidget.call(receiver, key, content, options);
    for (const map of [above, below]) { map.get(key)?.dispose?.(); map.delete(key); }
    if (content === undefined) return;
    let component: ComponentView;
    if (Array.isArray(content)) {
      const container = new pi.Container();
      for (const line of content.slice(0, 10)) container.addChild(new pi.Text(line, 1, 0));
      if (content.length > 10) container.addChild(new pi.Text(theme.fg('muted', '... (widget truncated)'), 1, 0));
      component = container;
    } else component = content(reference, theme);
    (options?.placement === 'belowEditor' ? below : above).set(key, component);
  });
  r.disposers.push(() => {
    if (consumer) { consumer.clearExtensionWidgets.call(receiver); return; }
    for (const map of [above, below]) { for (const component of map.values()) component.dispose?.(); map.clear(); }
  });
  return { setWidget, notify: r.notify, setEditorComponent: vi.fn() };
}
// Assertion only: mouse helpers never synthesize copy intent.
export async function expectSelectionSilent(r: Rig): Promise<void> {
  await flush();
  expect.soft(r.write, 'C1: mouse-only selection must not start native writing').not.toHaveBeenCalled();
  expect.soft(oscCopies(r.terminal), 'C1: no live OSC52 before explicit copy').toEqual([]);
  expect.soft(r.flash, 'C1: no Copied before explicit copy').not.toHaveBeenCalledWith('Copied!');
}
export function oscCopies(terminal: MemoryTerminal): string[] {
  const esc = String.fromCharCode(27); const bel = String.fromCharCode(7);
  const osc52 = new RegExp(`${esc}\\]52;c;([^${bel}]*)${bel}`, 'g');
  return terminal.output.flatMap((chunk) => [...chunk.matchAll(osc52)].map((match) => Buffer.from(match[1], 'base64').toString('utf8')));
}
