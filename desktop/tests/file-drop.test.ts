import { File as NodeFile } from 'node:buffer';
import { readFileSync } from 'node:fs';
import { Terminal } from '@xterm/xterm';
import ts from 'typescript';
import { describe, expect, it, vi } from 'vitest';

const installerModulePath = '../src/renderer/file-drop';

type DropEvent = {
  dataTransfer: { types: readonly string[]; files: ArrayLike<File>; dropEffect?: string };
  preventDefault: ReturnType<typeof vi.fn>;
};
type DropTarget = {
  addEventListener: (type: 'dragover' | 'drop', listener: (event: DropEvent) => void) => void;
};
type Installer = (dependencies: {
  target: DropTarget;
  getPathForFile: (file: File) => string;
  getCurrentTerminal: () => { paste(value: string): void } | undefined;
}) => void;

async function loadInstaller(): Promise<Installer | undefined> {
  let module: { installFileDropHandlers?: Installer } | undefined;
  try {
    // Keep the missing production seam a test assertion, rather than letting Vite abort test loading.
    module = await import(installerModulePath) as { installFileDropHandlers?: Installer };
  } catch {
    module = undefined;
  }
  expect(module?.installFileDropHandlers, 'file-drop installer is absent').toBeTypeOf('function');
  return module?.installFileDropHandlers;
}

function targetWithListeners(): { target: DropTarget; listeners: Map<string, (event: DropEvent) => void>; add: ReturnType<typeof vi.fn> } {
  const listeners = new Map<string, (event: DropEvent) => void>();
  const add = vi.fn((type: 'dragover' | 'drop', listener: (event: DropEvent) => void) => listeners.set(type, listener));
  return { target: { addEventListener: add }, listeners, add };
}

function event(types: readonly string[], files: ArrayLike<File> = []): DropEvent {
  return { dataTransfer: { types, files }, preventDefault: vi.fn() };
}

function file(name: string): File {
  return new NodeFile(['contents'], name) as unknown as File;
}

function source(path: string): ts.SourceFile {
  return ts.createSourceFile(path, readFileSync(new URL(path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true);
}

function propertyPath(node: ts.Expression): string | undefined {
  if (ts.isIdentifier(node)) return node.text;
  if (ts.isPropertyAccessExpression(node)) {
    const parent = propertyPath(node.expression);
    return parent ? `${parent}.${node.name.text}` : undefined;
  }
  return undefined;
}

function callsNamed(root: ts.Node, name: string): ts.CallExpression[] {
  const calls: ts.CallExpression[] = [];
  const visit = (node: ts.Node): void => {
    if (ts.isCallExpression(node) && propertyPath(node.expression)?.endsWith(name)) calls.push(node);
    ts.forEachChild(node, visit);
  };
  visit(root);
  return calls;
}

function insideLaunch(node: ts.Node): boolean {
  for (let parent = node.parent; parent; parent = parent.parent) {
    if (ts.isFunctionDeclaration(parent) && parent.name?.text === 'launch') return true;
  }
  return false;
}

describe('file-drop installer', () => {
  it('uses protected-mode types for dragover, leaving text drags alone', async () => {
    const install = await loadInstaller();
    if (!install) return;
    const { target, listeners } = targetWithListeners();
    install({ target, getPathForFile: vi.fn(), getCurrentTerminal: vi.fn() });
    const dragover = listeners.get('dragover');
    expect(dragover, 'dragover handler is missing').toBeTypeOf('function');
    if (!dragover) return;

    const files = event(['Files']);
    dragover(files);
    expect(files.preventDefault).toHaveBeenCalledOnce();
    expect(files.dataTransfer.dropEffect).toBe('copy');

    const text = event(['text/plain']);
    dragover(text);
    expect(text.preventDefault).not.toHaveBeenCalled();
    expect(text.dataTransfer.dropEffect).toBeUndefined();
  });

  it('reads actual drop files in order, removes empty paths, and consumes file navigation', async () => {
    const install = await loadInstaller();
    if (!install) return;
    const { target, listeners } = targetWithListeners();
    const first = file('first'); const unavailable = file('unavailable'); const last = file('last');
    const paste = vi.fn();
    const resolve = vi.fn((dropped: File) => dropped === first ? '/tmp/one file' : dropped === last ? '/tmp/東京.txt' : '');
    install({ target, getPathForFile: resolve, getCurrentTerminal: () => ({ paste }) });
    const drop = listeners.get('drop');
    expect(drop, 'drop handler is missing').toBeTypeOf('function');
    if (!drop) return;

    const dropped = event(['Files'], { 0: first, 1: unavailable, 2: last, length: 3 });
    drop(dropped);
    expect(resolve).toHaveBeenNthCalledWith(1, first);
    expect(resolve).toHaveBeenNthCalledWith(2, unavailable);
    expect(resolve).toHaveBeenNthCalledWith(3, last);
    expect(dropped.preventDefault).toHaveBeenCalledOnce();
    expect(paste).toHaveBeenCalledWith('/tmp/one file\n/tmp/東京.txt');
    expect(paste.mock.calls[0]?.[0]).not.toMatch(/\n$/);
  });

  it('resolves and filters an all-empty file drop with a live terminal', async () => {
    const install = await loadInstaller();
    if (!install) return;
    const { target, listeners } = targetWithListeners();
    const paste = vi.fn();
    const missing = file('gone');
    const resolve = vi.fn(() => '');
    install({ target, getPathForFile: resolve, getCurrentTerminal: () => ({ paste }) });
    const drop = listeners.get('drop');
    expect(drop, 'drop handler is missing').toBeTypeOf('function');
    if (!drop) return;

    const empty = event(['Files'], { 0: missing, length: 1 });
    drop(empty);
    expect(empty.preventDefault).toHaveBeenCalledOnce();
    expect(resolve).toHaveBeenCalledOnce();
    expect(resolve).toHaveBeenCalledWith(missing);
    expect(paste).not.toHaveBeenCalled();
  });

  it('consumes terminal-less file drops without resolving paths or injecting text', async () => {
    const install = await loadInstaller();
    if (!install) return;
    const { target, listeners } = targetWithListeners();
    const resolve = vi.fn(() => '/tmp/must-not-be-exposed');
    install({ target, getPathForFile: resolve, getCurrentTerminal: () => undefined });
    const drop = listeners.get('drop');
    expect(drop, 'drop handler is missing').toBeTypeOf('function');
    if (!drop) return;

    const noTerminal = event(['Files'], { 0: file('present'), length: 1 });
    const text = event(['text/plain'], { 0: file('must not resolve'), length: 1 });
    drop(noTerminal);
    drop(text);
    expect(noTerminal.preventDefault).toHaveBeenCalledOnce();
    expect(text.preventDefault).not.toHaveBeenCalled();
    expect(resolve).not.toHaveBeenCalled();
  });

  it('looks up the live terminal when each drop occurs and installs exactly one listener pair', async () => {
    const install = await loadInstaller();
    if (!install) return;
    const { target, listeners, add } = targetWithListeners();
    const first = { paste: vi.fn() }; const later = { paste: vi.fn() };
    let current: { paste(value: string): void } | undefined = first;
    install({ target, getPathForFile: () => '/tmp/file', getCurrentTerminal: () => current });
    expect(add).toHaveBeenCalledTimes(2);
    expect(add.mock.calls.map(([type]) => type).sort()).toEqual(['dragover', 'drop']);
    const drop = listeners.get('drop');
    expect(drop, 'drop handler is missing').toBeTypeOf('function');
    if (!drop) return;
    drop(event(['Files'], { 0: file('first'), length: 1 }));
    current = later;
    drop(event(['Files'], { 0: file('later'), length: 1 }));
    expect(first.paste).toHaveBeenCalledWith('/tmp/file');
    expect(later.paste).toHaveBeenCalledWith('/tmp/file');
  });

  it('drives xterm paste/onData through a real file drop', async () => {
    const install = await loadInstaller();
    if (!install) return;
    const { target, listeners } = targetWithListeners();
    const terminal = new Terminal();
    (terminal as unknown as { _core: { textarea: { value: string } } })._core.textarea = { value: '' };
    const received: string[] = [];
    terminal.onData((data) => received.push(data));
    install({ target, getPathForFile: (dropped) => dropped.name === 'first' ? '/tmp/with spaces/one' : '/tmp/東京.txt', getCurrentTerminal: () => terminal });
    const drop = listeners.get('drop');
    expect(drop, 'drop handler is missing').toBeTypeOf('function');
    if (drop) drop(event(['Files'], { 0: file('first'), 1: file('last'), length: 2 }));
    expect(received).toEqual(['/tmp/with spaces/one\r/tmp/東京.txt']);
    expect(received[0]).not.toMatch(/\r$/);
    terminal.dispose();
  });
});

describe('file-drop production wiring', () => {
  it('preload imports and exposes the typed webUtils File-to-path bridge', () => {
    const preload = source('../src/preload/index.ts');
    const contract = source('../src/shared/contract.ts');
    const electronImport = preload.statements.find((statement): statement is ts.ImportDeclaration => ts.isImportDeclaration(statement) && statement.moduleSpecifier.getText(preload).includes('electron'));
    const names = electronImport?.importClause?.namedBindings;
    expect(names && ts.isNamedImports(names) && names.elements.some((entry) => entry.name.text === 'webUtils'), 'preload must import webUtils').toBe(true);
    expect(callsNamed(preload, 'webUtils.getPathForFile')).toHaveLength(1);
    const api = contract.statements.find((statement): statement is ts.InterfaceDeclaration => ts.isInterfaceDeclaration(statement) && statement.name.text === 'TerminalApi');
    expect(api?.members.some((member) => ts.isMethodSignature(member) && member.name.getText(contract) === 'getPathForFile'), 'TerminalApi.getPathForFile is missing').toBe(true);
  });

  it('preload passes each actual File to webUtils and returns its exact result', async () => {
    const exposeInMainWorld = vi.fn();
    const getPathForFile = vi.fn((dropped: File) => dropped.name === 'available' ? '/tmp/exact path' : '');
    const windowDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'window');
    vi.resetModules();
    vi.doMock('electron', () => ({
      contextBridge: { exposeInMainWorld },
      ipcRenderer: { invoke: vi.fn(), on: vi.fn(), removeListener: vi.fn(), send: vi.fn(), sendSync: vi.fn() },
      webUtils: { getPathForFile },
    }));
    Object.defineProperty(globalThis, 'window', { configurable: true, value: { addEventListener: vi.fn() } });

    try {
      await import('../src/preload/index');
      const api = exposeInMainWorld.mock.calls[0]?.[1] as { getPathForFile?: (dropped: File) => string } | undefined;
      expect(api?.getPathForFile, 'preload did not expose getPathForFile').toBeTypeOf('function');
      if (!api?.getPathForFile) return;

      const available = file('available');
      const unavailable = file('unavailable');
      expect(api.getPathForFile(available)).toBe('/tmp/exact path');
      expect(api.getPathForFile(unavailable)).toBe('');
      expect(getPathForFile).toHaveBeenNthCalledWith(1, available);
      expect(getPathForFile).toHaveBeenNthCalledWith(2, unavailable);
    } finally {
      vi.doUnmock('electron');
      vi.resetModules();
      if (windowDescriptor) Object.defineProperty(globalThis, 'window', windowDescriptor);
      else Reflect.deleteProperty(globalThis, 'window');
    }
  });

  it('imports and installs the seam once outside launch with the preload bridge and selected live runtime resolver', () => {
    const renderer = source('../src/renderer/index.ts');
    const importsSeam = renderer.statements.some((statement) => ts.isImportDeclaration(statement)
      && ts.isStringLiteral(statement.moduleSpecifier) && statement.moduleSpecifier.text.endsWith('/file-drop'));
    expect(importsSeam, 'renderer does not import the file-drop seam').toBe(true);
    const installs = callsNamed(renderer, 'installFileDropHandlers');
    expect(installs, 'renderer must install file-drop handlers once').toHaveLength(1);
    if (installs.length !== 1) return;
    expect(insideLaunch(installs[0])).toBe(false);
    const wiring = installs[0].getText(renderer);
    expect(wiring).toContain('window.voidTerminal.getPathForFile');
    expect(wiring).toContain('selectedTab');
    expect(wiring).toContain('runtimes.get');
  });
});
