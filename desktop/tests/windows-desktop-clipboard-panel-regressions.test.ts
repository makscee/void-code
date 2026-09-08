import { spawn } from 'node:child_process';
import { chmodSync, existsSync, lstatSync, lutimesSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, symlinkSync, utimesSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
import { build } from 'esbuild';
import ts from 'typescript';
import { afterEach, describe, expect, it, vi } from 'vitest';

type ClipboardReadResult = { kind: 'empty' } | { kind: 'text'; text: string } | { kind: 'image-path'; path: string };
type FakeImage = { isEmpty(): boolean; toPNG(): Buffer };
type InvokeHandler<Event> = (event: Event, raw?: unknown) => unknown;
type ClipboardIpcOptions<Event> = {
  ipcMain: { handle(channel: string, handler: InvokeHandler<Event>): void };
  channels: { clipboardRead: string; clipboardWrite: string };
  platform: string;
  authorize(event: Event): void;
  dependencies: {
    clipboard: { readImage(): FakeImage; readText(): string; writeText(text: string): void };
    writeImage(png: Buffer): string;
  };
};
type ClipboardImageStorageOptions = {
  temporaryDirectory(): string;
  uniqueId(): string;
  processId: number;
  now(): number;
  isProcessAlive(processId: number): boolean;
};
type ClipboardImageStorage = {
  directory: string;
  writeImage(png: Buffer): string;
  cleanup(): void;
};
type ClipboardPanelModule = {
  registerDesktopClipboardHandlers?<Event>(options: ClipboardIpcOptions<Event>): void;
  createClipboardImageStorage?(options: ClipboardImageStorageOptions): ClipboardImageStorage;
  createSafeClipboardImageStorage?(platform: string, create: () => ClipboardImageStorage): ClipboardImageStorage;
  readDesktopClipboard?(dependencies: ClipboardIpcOptions<unknown>['dependencies']): ClipboardReadResult;
};

async function clipboardPanelModule(): Promise<ClipboardPanelModule> {
  return import(new URL('../src/main/clipboard-paste.ts', import.meta.url).href) as Promise<ClipboardPanelModule>;
}

class CapturedIpc<Event> {
  readonly handlers = new Map<string, InvokeHandler<Event>>();

  handle(channel: string, handler: InvokeHandler<Event>): void {
    if (this.handlers.has(channel)) throw new Error(`duplicate handler: ${channel}`);
    this.handlers.set(channel, handler);
  }
}

function image(empty: boolean, png = Buffer.from([0x89, 0x50, 0x4e, 0x47])): FakeImage {
  return { isEmpty: () => empty, toPNG: () => png };
}

function ipcFixture(platform: string) {
  const owned = { sender: 'owned' };
  const foreign = { sender: 'foreign' };
  const ipcMain = new CapturedIpc<typeof owned>();
  const reads = { image: 0, text: 0 };
  const writes: string[] = [];
  const imageWrites: Buffer[] = [];
  const dependencies: ClipboardIpcOptions<typeof owned>['dependencies'] = {
    clipboard: {
      readImage: () => { reads.image++; return image(true); },
      readText: () => { reads.text++; return 'trusted text'; },
      writeText: (text) => { writes.push(text); },
    },
    writeImage: (png) => {
      imageWrites.push(png);
      return 'C:\\owned-images\\void-code-clipboard-ipc.png';
    },
  };
  const authorize = vi.fn((event: typeof owned) => {
    if (event !== owned) throw new Error('renderer authority rejected');
  });
  return { owned, foreign, ipcMain, reads, writes, imageWrites, dependencies, authorize, platform };
}

// F1: this calls the injectable registrar that production main must call. Capturing handlers and
// invoking them catches an inline no-op, a helper that is never registered, and reordered auth /
// validation; source text containing the right words is not an oracle for any of those behaviors.
describe('F1 — production clipboard IPC registration is executable, not source-text theater', () => {
  it('registers the actual read/write channels and enforces owned-renderer authority before access', async () => {
    const module = await clipboardPanelModule();
    expect(module.registerDesktopClipboardHandlers, 'injectable production clipboard IPC registrar is absent').toBeTypeOf('function');
    const fixture = ipcFixture('win32');

    module.registerDesktopClipboardHandlers!({
      ipcMain: fixture.ipcMain,
      channels: { clipboardRead: 'clipboard:read', clipboardWrite: 'clipboard:write' },
      platform: fixture.platform,
      authorize: fixture.authorize,
      dependencies: fixture.dependencies,
    });

    expect([...fixture.ipcMain.handlers.keys()]).toEqual(['clipboard:read', 'clipboard:write']);
    const read = fixture.ipcMain.handlers.get('clipboard:read')!;
    const write = fixture.ipcMain.handlers.get('clipboard:write')!;
    await expect(Promise.resolve().then(() => read(fixture.owned))).resolves.toEqual({ kind: 'text', text: 'trusted text' } satisfies ClipboardReadResult);
    await expect(Promise.resolve().then(() => read(fixture.foreign))).rejects.toThrow('renderer authority rejected');
    await expect(Promise.resolve().then(() => write(fixture.foreign, { text: 'must not escape' }))).rejects.toThrow('renderer authority rejected');
    expect(fixture.reads).toEqual({ image: 1, text: 1 });
    expect(fixture.writes).toEqual([]);
    expect(fixture.authorize).toHaveBeenCalledTimes(3);
  });

  it('keeps non-Windows reads empty without touching Electron clipboard', async () => {
    const module = await clipboardPanelModule();
    expect(module.registerDesktopClipboardHandlers, 'injectable production clipboard IPC registrar is absent').toBeTypeOf('function');
    const fixture = ipcFixture('darwin');
    module.registerDesktopClipboardHandlers!({
      ipcMain: fixture.ipcMain,
      channels: { clipboardRead: 'clipboard:read', clipboardWrite: 'clipboard:write' },
      platform: fixture.platform,
      authorize: fixture.authorize,
      dependencies: fixture.dependencies,
    });

    await expect(Promise.resolve().then(() => fixture.ipcMain.handlers.get('clipboard:read')!(fixture.owned))).resolves.toEqual({ kind: 'empty' });
    expect(fixture.reads).toEqual({ image: 0, text: 0 });
    expect(fixture.imageWrites).toEqual([]);
  });

  it('strictly validates writes before invoking Electron clipboard.writeText with the exact text', async () => {
    const module = await clipboardPanelModule();
    expect(module.registerDesktopClipboardHandlers, 'injectable production clipboard IPC registrar is absent').toBeTypeOf('function');
    const fixture = ipcFixture('win32');
    module.registerDesktopClipboardHandlers!({
      ipcMain: fixture.ipcMain,
      channels: { clipboardRead: 'clipboard:read', clipboardWrite: 'clipboard:write' },
      platform: fixture.platform,
      authorize: fixture.authorize,
      dependencies: fixture.dependencies,
    });
    const write = fixture.ipcMain.handlers.get('clipboard:write')!;
    const exact = '  selected\r\nΔ text\t ';

    await expect(Promise.resolve().then(() => write(fixture.owned, { text: exact }))).resolves.toBeUndefined();
    expect(fixture.writes).toEqual([exact]);
    for (const raw of [null, [], {}, { text: '' }, { text: 7 }, { text: 'selected', path: 'C:\\escape.png' }]) {
      await expect(Promise.resolve().then(() => write(fixture.owned, raw))).rejects.toThrow();
    }
    expect(fixture.writes).toEqual([exact]);
  });

  it('the real main registration delegates both channels to that tested registrar', () => {
    const source = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');
    expect(source).toMatch(/import\s*\{[^}]*\bregisterDesktopClipboardHandlers\b[^}]*\}\s*from\s*['"]\.\/clipboard-paste['"]/s);
    expect(source).toMatch(/registerDesktopClipboardHandlers\s*\(\s*\{[\s\S]*?ipcMain[\s\S]*?channels\s*:\s*IPC[\s\S]*?platform\s*:\s*process\.platform[\s\S]*?authorize\s*:\s*assertRenderer[\s\S]*?dependencies\s*:\s*desktopClipboardDependencies[\s\S]*?\}\s*\)/);
    expect(source).not.toMatch(/ipcMain\.handle\(IPC\.clipboard(?:Read|Write)/);
  });
});

function callsNamed(root: ts.Node, name: string): ts.CallExpression[] {
  const calls: ts.CallExpression[] = [];
  const visit = (node: ts.Node): void => {
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === name) calls.push(node);
    ts.forEachChild(node, visit);
  };
  visit(root);
  return calls;
}

function containsSingleInstanceLock(root: ts.Node): boolean {
  let found = false;
  const visit = (node: ts.Node): void => {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression)
      && node.expression.expression.getText() === 'app' && node.expression.name.text === 'requestSingleInstanceLock') found = true;
    if (!found) ts.forEachChild(node, visit);
  };
  visit(root);
  return found;
}

// G2 separates optional image storage from the clipboard boundary. The factory is executable so a
// thrown mkdir is proved harmless; the one source-level assertion is limited to the Electron lock,
// which cannot be imported without starting the actual app.
describe('G2 — clipboard image storage starts only for the winning Windows instance and degrades safely', () => {
  it('creates storage only on Windows and returns the exact successful store', async () => {
    const module = await clipboardPanelModule();
    expect(module.createSafeClipboardImageStorage, 'safe clipboard image-storage factory is absent').toBeTypeOf('function');
    const expected: ClipboardImageStorage = {
      directory: 'C:\\owned-images\\void-code-clipboard-current',
      writeImage: () => 'C:\\owned-images\\void-code-clipboard-current\\image.png',
      cleanup: vi.fn(),
    };
    const create = vi.fn(() => expected);

    expect(module.createSafeClipboardImageStorage!('darwin', create).directory).toBe('');
    expect(create).not.toHaveBeenCalled();
    expect(module.createSafeClipboardImageStorage!('win32', create)).toBe(expected);
    expect(create).toHaveBeenCalledOnce();
  });

  it('turns Windows storage creation failure into image-only degradation while text paste and copy still work', async () => {
    const module = await clipboardPanelModule();
    expect(module.createSafeClipboardImageStorage, 'safe clipboard image-storage factory is absent').toBeTypeOf('function');
    expect(module.readDesktopClipboard, 'desktop clipboard reader is absent').toBeTypeOf('function');
    const create = vi.fn((): ClipboardImageStorage => { throw new Error('temporary storage unavailable'); });
    const store = module.createSafeClipboardImageStorage!('win32', create);
    const fixture = ipcFixture('win32');
    fixture.dependencies.writeImage = store.writeImage;
    fixture.dependencies.clipboard.readImage = () => image(false);

    expect(module.readDesktopClipboard!(fixture.dependencies)).toEqual({ kind: 'empty' });

    fixture.dependencies.clipboard.readImage = () => image(true);
    expect(module.readDesktopClipboard!(fixture.dependencies)).toEqual({ kind: 'text', text: 'trusted text' });

    module.registerDesktopClipboardHandlers!({
      ipcMain: fixture.ipcMain,
      channels: { clipboardRead: 'clipboard:read', clipboardWrite: 'clipboard:write' },
      platform: fixture.platform,
      authorize: fixture.authorize,
      dependencies: fixture.dependencies,
    });
    await expect(Promise.resolve(fixture.ipcMain.handlers.get('clipboard:write')!(fixture.owned, { text: 'copy still works' }))).resolves.toBeUndefined();
    expect(fixture.writes).toEqual(['copy still works']);
    expect(() => store.cleanup()).not.toThrow();
    expect(create).toHaveBeenCalledOnce();
  });

  it('wires the safe factory once inside the branch reached only after the single-instance lock succeeds', () => {
    const source = ts.createSourceFile(
      'index.ts',
      readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8'),
      ts.ScriptTarget.Latest,
      true,
      ts.ScriptKind.TS,
    );
    const lockBranch = source.statements.find((statement): statement is ts.IfStatement => ts.isIfStatement(statement)
      && containsSingleInstanceLock(statement.expression));
    expect(lockBranch, 'main has no single-instance-lock branch').toBeDefined();
    expect(lockBranch?.elseStatement, 'winning-instance initialization must be in the lock branch else').toBeDefined();

    const allSafeCreates = callsNamed(source, 'createSafeClipboardImageStorage');
    const winningSafeCreates = lockBranch?.elseStatement ? callsNamed(lockBranch.elseStatement, 'createSafeClipboardImageStorage') : [];
    expect(allSafeCreates, 'safe storage must be initialized exactly once').toHaveLength(1);
    expect(winningSafeCreates, 'losing instances must not reach storage initialization').toHaveLength(1);
    expect(winningSafeCreates[0]).toBe(allSafeCreates[0]);
    expect(winningSafeCreates[0].arguments[0]?.getText(source)).toBe('process.platform');
    expect(callsNamed(source, 'createClipboardImageStorage'), 'main must not eagerly invoke the throwing storage constructor').toHaveLength(0);
  });
});

const roots: string[] = [];
afterEach(() => {
  for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true });
});

function temporaryRoot(): string {
  const root = mkdtempSync(path.join(os.tmpdir(), 'void-code-clipboard-test-'));
  roots.push(root);
  return root;
}

function storageOptions(root: string, processId: number, id: string, now: number, live: ReadonlySet<number>): ClipboardImageStorageOptions {
  return {
    temporaryDirectory: () => root,
    uniqueId: () => id,
    processId,
    now: () => now,
    isProcessAlive: (candidate) => live.has(candidate),
  };
}

function deterministicUuid(sequence: number): string {
  return `00000000-0000-4000-8000-${sequence.toString().padStart(12, '0')}`;
}

// G3 uses the real filesystem for permission and deletion assertions. Time and apparent process
// liveness are injected, so strict seven-day pruning needs neither a sleep nor a real reused PID.
describe('G3 — process-owned clipboard image storage and strict age-based crash retention', () => {
  it('creates one absolute owned directory, writes unique PNGs inside it, and removes it on cleanup', async () => {
    const module = await clipboardPanelModule();
    expect(module.createClipboardImageStorage, 'process-owned clipboard image storage is absent').toBeTypeOf('function');
    const root = temporaryRoot();
    const ownedId = deterministicUuid(1);
    const store = module.createClipboardImageStorage!(storageOptions(root, 4101, ownedId, Date.UTC(2026, 8, 8), new Set([4101])));

    expect(path.isAbsolute(store.directory)).toBe(true);
    expect(path.dirname(store.directory)).toBe(path.resolve(root));
    expect(path.basename(store.directory)).toBe(`void-code-clipboard-4101-${ownedId}`);
    expect(statSync(store.directory).isDirectory()).toBe(true);

    const first = store.writeImage(Buffer.from([0x89, 0x50, 0x4e, 0x47, 1]));
    const second = store.writeImage(Buffer.from([0x89, 0x50, 0x4e, 0x47, 2]));
    expect(path.dirname(first)).toBe(store.directory);
    expect(path.dirname(second)).toBe(store.directory);
    expect(path.extname(first)).toBe('.png');
    expect(path.extname(second)).toBe('.png');
    expect(first).not.toBe(second);

    // Node's mode bits are POSIX evidence only. Windows confidentiality is asserted separately
    // from the native DACL in windows-clipboard-dacl.test.ts on a real Windows runner.
    if (process.platform !== 'win32') {
      expect(statSync(store.directory).mode & 0o777).toBe(0o700);
      expect(statSync(first).mode & 0o777).toBe(0o600);
      expect(statSync(second).mode & 0o777).toBe(0o600);
    }

    store.cleanup();
    expect(existsSync(store.directory)).toBe(false);
  });

  it('prunes old exact app-owned UUID directories but retains old prefix collisions', async () => {
    const module = await clipboardPanelModule();
    expect(module.createClipboardImageStorage, 'process-owned clipboard image storage is absent').toBeTypeOf('function');
    const root = temporaryRoot();
    const now = Date.UTC(2026, 8, 8, 12);
    const old = now - 8 * 24 * 60 * 60 * 1000;
    const fresh = now - 6 * 24 * 60 * 60 * 1000;
    const exactlySevenDays = now - 7 * 24 * 60 * 60 * 1000;
    const noLiveProcesses = new Set<number>();

    const staleDead = module.createClipboardImageStorage!(storageOptions(root, 4201, deterministicUuid(2), old, noLiveProcesses));
    const staleLive = module.createClipboardImageStorage!(storageOptions(root, 4202, deterministicUuid(3), old, noLiveProcesses));
    const freshDead = module.createClipboardImageStorage!(storageOptions(root, 4203, deterministicUuid(4), old, noLiveProcesses));
    const boundaryDead = module.createClipboardImageStorage!(storageOptions(root, 4204, deterministicUuid(5), old, noLiveProcesses));
    utimesSync(staleDead.directory, old / 1000, old / 1000);
    utimesSync(staleLive.directory, old / 1000, old / 1000);
    utimesSync(freshDead.directory, fresh / 1000, fresh / 1000);
    utimesSync(boundaryDead.directory, exactlySevenDays / 1000, exactlySevenDays / 1000);

    const unrelatedDirectory = path.join(root, 'another-app-cache');
    const unrelatedFile = path.join(root, 'void-code-clipboard-not-a-directory');
    const prefixSymlink = path.join(root, 'void-code-clipboard-4999-symlink');
    const prefixCollisionDirectories = [
      path.join(root, 'void-code-clipboard-123-backups'),
      path.join(root, 'void-code-clipboard-123-00000000-0000-4000-8000-00000000000g'),
      path.join(root, 'void-code-clipboard-123-00000000-0000-4000-8000-00000000000'),
      path.join(root, `void-code-clipboard-123-${deterministicUuid(6)}-backups`),
    ];
    mkdirSync(unrelatedDirectory, { mode: 0o700 });
    for (const entry of prefixCollisionDirectories) mkdirSync(entry, { mode: 0o700 });
    writeFileSync(unrelatedFile, 'not owned directory data', { mode: 0o600 });
    symlinkSync(unrelatedDirectory, prefixSymlink, process.platform === 'win32' ? 'junction' : 'dir');
    utimesSync(unrelatedDirectory, old / 1000, old / 1000);
    for (const entry of prefixCollisionDirectories) utimesSync(entry, old / 1000, old / 1000);
    utimesSync(unrelatedFile, old / 1000, old / 1000);
    lutimesSync(prefixSymlink, old / 1000, old / 1000);
    chmodSync(unrelatedDirectory, 0o700);
    const retainedMtimes = new Map([
      [freshDead.directory, lstatSync(freshDead.directory).mtimeMs],
      [boundaryDead.directory, lstatSync(boundaryDead.directory).mtimeMs],
      [unrelatedDirectory, lstatSync(unrelatedDirectory).mtimeMs],
      [unrelatedFile, lstatSync(unrelatedFile).mtimeMs],
      [prefixSymlink, lstatSync(prefixSymlink).mtimeMs],
      ...prefixCollisionDirectories.map((entry) => [entry, lstatSync(entry).mtimeMs] as const),
    ]);

    const current = module.createClipboardImageStorage!(storageOptions(root, 4299, deterministicUuid(7), now, new Set([4202, 4299])));

    expect(existsSync(staleDead.directory), 'stale app directory with a dead encoded PID').toBe(false);
    expect(existsSync(staleLive.directory), 'stale app directory with a reused/live encoded PID').toBe(false);
    expect(existsSync(freshDead.directory), 'fresh directory belonging to a dead process').toBe(true);
    expect(existsSync(boundaryDead.directory), 'directory exactly seven days old').toBe(true);
    expect(existsSync(unrelatedDirectory), 'unrelated old directory').toBe(true);
    for (const entry of prefixCollisionDirectories) expect(existsSync(entry), `prefix collision ${path.basename(entry)}`).toBe(true);
    expect(existsSync(unrelatedFile), 'prefix-matching non-directory').toBe(true);
    expect(lstatSync(prefixSymlink).isSymbolicLink(), 'prefix-matching old symlink').toBe(true);
    for (const [entry, mtime] of retainedMtimes) expect(lstatSync(entry).mtimeMs, `pruning touched retained entry ${entry}`).toBe(mtime);
    expect(lstatSync(unrelatedFile).isFile()).toBe(true);
    expect(existsSync(current.directory), 'current process directory').toBe(true);
  });

});

function runProcess(executable: string, arguments_: string[], cwd: string): Promise<{ code: number | null; stdout: string; stderr: string }> {
  return new Promise((resolve, reject) => {
    const environment = { ...process.env };
    delete environment.ELECTRON_RUN_AS_NODE;
    const child = spawn(executable, arguments_, { cwd, env: environment, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '';
    let stderr = '';
    child.stdout.setEncoding('utf8');
    child.stderr.setEncoding('utf8');
    child.stdout.on('data', (chunk: string) => { stdout += chunk; });
    child.stderr.on('data', (chunk: string) => { stderr += chunk; });
    const watchdog = setTimeout(() => {
      child.kill('SIGKILL');
      reject(new Error(`hidden Electron fixture did not finish\nstdout:\n${stdout}\nstderr:\n${stderr}`));
    }, 8_000);
    child.once('error', (error) => { clearTimeout(watchdog); reject(error); });
    child.once('close', (code) => { clearTimeout(watchdog); resolve({ code, stdout, stderr }); });
  });
}

// F4 is intentionally a Chromium integration, not another call to FakeTerminal.handler. The
// hidden window receives Electron input events; xterm owns the textarea, selection, key dispatch,
// default action, Terminal.paste(), and onData path. Clipboard read/write remain injected spies.
describe('F4 — shipped xterm keyboard/default-action/onData integration', () => {
  it('consumes selected Ctrl+C and delivers Ctrl+V plus ordinary typing through real xterm onData', async () => {
    const root = temporaryRoot();
    const rendererBundle = path.join(root, 'renderer.js');
    const mainBundle = path.join(root, 'main.cjs');
    const page = path.join(root, 'index.html');
    const resultFile = path.join(root, 'result.json');

    await build({
      entryPoints: [path.resolve('tests/fixtures/windows-desktop-clipboard-xterm-renderer.ts')],
      bundle: true,
      format: 'iife',
      platform: 'browser',
      target: 'chrome130',
      outfile: rendererBundle,
      logLevel: 'silent',
    });
    await build({
      entryPoints: [path.resolve('tests/fixtures/windows-desktop-clipboard-xterm-main.ts')],
      bundle: true,
      format: 'cjs',
      platform: 'node',
      target: 'node22',
      external: ['electron'],
      outfile: mainBundle,
      logLevel: 'silent',
    });
    writeFileSync(page, '<!doctype html><html><body><div id="terminal"></div><script src="renderer.js"></script></body></html>', 'utf8');

    const require = createRequire(import.meta.url);
    const electron = require('electron') as string;
    const platformArguments = process.platform === 'linux' ? ['--ozone-platform=headless', '--disable-gpu'] : [];
    const completed = await runProcess(electron, [...platformArguments, mainBundle, `--fixture-page=${page}`, `--fixture-result=${resultFile}`], path.resolve('.'));

    expect(completed.code, `hidden Electron fixture failed\nstdout:\n${completed.stdout}\nstderr:\n${completed.stderr}`).toBe(0);
    const result = JSON.parse(readFileSync(resultFile, 'utf8')) as {
      implementation: string;
      instance: boolean;
      copied: string[];
      terminalData: string[];
      keydowns: Array<{ code: string; defaultPrevented: boolean }>;
      keyups: Array<{ code: string; defaultPrevented: boolean }>;
      domCopyEvents: number;
      domPasteEvents: number;
    };
    expect(result).toEqual({
      implementation: '@xterm/xterm',
      instance: true,
      copied: ['selected text'],
      terminalData: ['pasted once', 'x'],
      keydowns: [
        { code: 'KeyC', defaultPrevented: true },
        { code: 'KeyV', defaultPrevented: true },
      ],
      keyups: [
        { code: 'KeyC', defaultPrevented: false },
        { code: 'KeyV', defaultPrevented: false },
      ],
      domCopyEvents: 0,
      domPasteEvents: 0,
    });
  });

  it('the renderer launch path uses only the same real-xterm wiring seam exercised by the fixture', () => {
    const source = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');
    expect(source).toMatch(/import\s*\{[^}]*\bwireProductTerminalClipboard\b[^}]*\}\s*from\s*['"]\.\/clipboard-shortcuts['"]/s);
    expect(source).toMatch(/wireProductTerminalClipboard\s*\(\s*terminal\s*,\s*rendererPlatform\s*,[\s\S]*?window\.voidTerminal\.clipboard\.read\(\)[\s\S]*?window\.voidTerminal\.clipboard\.write\([\s\S]*?window\.voidTerminal\.input\(/);
    expect(source).not.toMatch(/\b(?:createOrderedTerminalInputSink|installWindowsClipboardShortcuts)\b/);
  });
});
