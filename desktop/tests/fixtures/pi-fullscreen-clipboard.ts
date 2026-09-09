import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { transformSync } from 'esbuild';
import { expect, vi } from 'vitest';

export const agentDir = path.resolve('runtime/pi/node_modules/@earendil-works/pi-coding-agent');
const require = createRequire(path.join(agentDir, 'package.json'));
export async function realPi(): Promise<any> {
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
  default: (pi: any, options?: any) => unknown;
  installFullscreenClipboard?: (tui: any, options: ClipboardOptions) => () => void;
  createNativeClipboardWriter?: (options: any) => (text: string, signal?: AbortSignal) => Promise<void>;
};
export const localEnv = { VC_BOOTSTRAP_EXECUTABLE: '/isolated/vc', SystemRoot: 'C:\\Windows' };
export async function extension(env = localEnv): Promise<ExtensionModule> {
  const tui = await realPi();
  const code = transformSync(embeddedSource(), { loader: 'ts', format: 'cjs', target: 'node22', logLevel: 'silent' }).code;
  const module = { exports: {} };
  const safeRequire = (id: string): any => {
    if (id === 'node:child_process' || id === 'child_process') return {
      execFileSync: vi.fn(() => JSON.stringify({ version: 1, relayUrl: 'https://relay.invalid', authToken: 'fixture-only', providers: [{ kind: 'codex', relayProviderId: 'fixture', models: ['gpt-5.6-terra'] }] })),
      spawn: () => { throw new Error('unit fixture forbids native clipboard IO'); },
    };
    if (id === '@earendil-works/pi-tui') return tui;
    if (id === '@earendil-works/pi-coding-agent') return { getPackageDir: () => agentDir };
    if (id === '@earendil-works/pi-ai') return { clampThinkingLevel: (_m: unknown, level: string) => level };
    if (['node:fs', 'fs', 'node:fs/promises', 'fs/promises'].includes(id)) return new Proxy({ existsSync: () => false }, {
      get(target, name) { return name === 'existsSync' ? target.existsSync : () => { throw new Error('unit fixture forbids filesystem IO'); }; },
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
export async function rig(lines = ['Привет 世界 😀', 'строка два'], pi?: any) {
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
export function oscCopies(terminal: MemoryTerminal): string[] {
  return terminal.output.flatMap((chunk) => [...chunk.matchAll(/\x1b\]52;c;([^\x07]*)\x07/g)].map((match) => Buffer.from(match[1], 'base64').toString('utf8')));
}
