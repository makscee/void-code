import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { describe, expect, it, vi } from 'vitest';

type ClipboardReadResult = { kind: 'empty' } | { kind: 'text'; text: string } | { kind: 'image-path'; path: string };
type FakeImage = { isEmpty(): boolean; toPNG(): Buffer };
type ClipboardReadDependencies = {
  clipboard: { readImage(): FakeImage; readText(): string };
  filesystem: { temporaryDirectory(): string; writeFile(path: string, png: Buffer): void };
  uniqueId(): string;
};
type ClipboardPasteModule = {
  readDesktopClipboard(dependencies: ClipboardReadDependencies): ClipboardReadResult;
  createTrustedClipboardReadHandler<Event>(authorize: (event: Event) => void, dependencies: ClipboardReadDependencies): (event: Event) => Promise<ClipboardReadResult>;
};
type TerminalClipboardTarget = {
  attachCustomKeyEventHandler(handler: (event: KeyboardEvent) => boolean): void;
  paste(value: string): void;
};
type ClipboardShortcutsModule = {
  installWindowsClipboardShortcuts(target: TerminalClipboardTarget, platform: string, readTrustedClipboard: () => Promise<ClipboardReadResult>): void;
};

// These modules are deliberately loaded inside each test. On the pre-fix tree, every assertion
// says which contract is absent; once Agent B adds the modules, the same tests exercise them.
async function mainClipboard(): Promise<ClipboardPasteModule> {
  const module = await import(new URL('../src/main/clipboard-paste.ts', import.meta.url).href).catch(() => undefined);
  expect(module, 'main-process clipboard reader is absent').toBeDefined();
  return module as ClipboardPasteModule;
}

async function rendererClipboard(): Promise<ClipboardShortcutsModule> {
  const module = await import(new URL('../src/renderer/clipboard-shortcuts.ts', import.meta.url).href).catch(() => undefined);
  expect(module, 'renderer shortcut interceptor is absent').toBeDefined();
  return module as ClipboardShortcutsModule;
}

type ClipboardFixture = {
  image: FakeImage;
  text?: string;
  imageError?: Error;
  textError?: Error;
};

function dependencies(fixture: ClipboardFixture, ids = ['first', 'second']): ClipboardReadDependencies & { writes: Array<{ path: string; png: Buffer }>; reads: { image: number; text: number } } {
  const writes: Array<{ path: string; png: Buffer }> = [];
  const reads = { image: 0, text: 0 };
  return {
    clipboard: {
      readImage: () => {
        reads.image++;
        if (fixture.imageError) throw fixture.imageError;
        return fixture.image;
      },
      readText: () => {
        reads.text++;
        if (fixture.textError) throw fixture.textError;
        return fixture.text ?? '';
      },
    },
    filesystem: {
      temporaryDirectory: () => 'C:\\void-temp',
      writeFile: (file, png) => writes.push({ path: file, png }),
    },
    uniqueId: () => ids.shift()!,
    writes,
    reads,
  };
}

function nonEmptyImage(png = Buffer.from([0x89, 0x50, 0x4e, 0x47])): FakeImage {
  return { isEmpty: () => false, toPNG: () => png };
}

function emptyImage(): FakeImage {
  return { isEmpty: () => true, toPNG: () => { throw new Error('empty image must never be encoded'); } };
}

// Regression for v0.2.51: a Windows image used to be discarded before Pi could receive a path.
describe('desktop clipboard read stays in the trusted main-process boundary', () => {
  it('prefers a non-empty image over text and writes each image as a distinct temporary PNG path', async () => {
    const { readDesktopClipboard } = await mainClipboard();
    const png = Buffer.from([0x89, 0x50, 0x4e, 0x47, 1]);
    const seam = dependencies({ image: nonEmptyImage(png), text: 'this text must lose to the image' });

    const first = readDesktopClipboard(seam);
    const second = readDesktopClipboard(seam);

    expect(first).toEqual({ kind: 'image-path', path: 'C:\\void-temp\\void-code-clipboard-first.png' });
    expect(second).toEqual({ kind: 'image-path', path: 'C:\\void-temp\\void-code-clipboard-second.png' });
    expect(seam.writes).toEqual([
      { path: 'C:\\void-temp\\void-code-clipboard-first.png', png },
      { path: 'C:\\void-temp\\void-code-clipboard-second.png', png },
    ]);
    expect(seam.reads).toEqual({ image: 2, text: 0 });
  });

  it('falls back to non-empty plain text only when there is no image', async () => {
    const { readDesktopClipboard } = await mainClipboard();
    const seam = dependencies({ image: emptyImage(), text: 'paste this' });

    expect(readDesktopClipboard(seam)).toEqual({ kind: 'text', text: 'paste this' });
    expect(seam.reads).toEqual({ image: 1, text: 1 });
    expect(seam.writes).toEqual([]);
  });

  it('returns the only empty result and writes nothing for an empty clipboard', async () => {
    const { readDesktopClipboard } = await mainClipboard();
    const seam = dependencies({ image: emptyImage(), text: '' });

    expect(readDesktopClipboard(seam)).toEqual({ kind: 'empty' });
    expect(seam.writes).toEqual([]);
  });

  it('does not turn an image read failure into a text injection', async () => {
    const { readDesktopClipboard } = await mainClipboard();
    const seam = dependencies({ image: emptyImage(), text: 'do not paste', imageError: new Error('clipboard unavailable') });

    expect(readDesktopClipboard(seam)).toEqual({ kind: 'empty' });
    expect(seam.reads).toEqual({ image: 1, text: 0 });
    expect(seam.writes).toEqual([]);
  });

  it('does not inject text when reading fallback text fails', async () => {
    const { readDesktopClipboard } = await mainClipboard();
    const seam = dependencies({ image: emptyImage(), textError: new Error('clipboard unavailable') });

    expect(readDesktopClipboard(seam)).toEqual({ kind: 'empty' });
    expect(seam.writes).toEqual([]);
  });

  it('does not fall back to text when persisting the preferred image fails', async () => {
    const { readDesktopClipboard } = await mainClipboard();
    const seam = dependencies({ image: nonEmptyImage(), text: 'do not fall back after a write failure' });
    seam.filesystem.writeFile = () => { throw new Error('disk full'); };

    expect(readDesktopClipboard(seam)).toEqual({ kind: 'empty' });
    expect(seam.reads).toEqual({ image: 1, text: 0 });
  });

  it('rejects a caller other than the owned renderer before it touches the clipboard', async () => {
    const { createTrustedClipboardReadHandler } = await mainClipboard();
    const ownedRenderer = { id: 7 };
    const foreignRenderer = { id: 8 };
    const seam = dependencies({ image: emptyImage(), text: 'private clipboard text' });
    const authorize = vi.fn((event: unknown) => {
      if (event !== ownedRenderer) throw new Error('renderer authority rejected');
    });
    const handle = createTrustedClipboardReadHandler(authorize, seam);

    await expect(handle(ownedRenderer)).resolves.toEqual({ kind: 'text', text: 'private clipboard text' });
    await expect(handle(foreignRenderer)).rejects.toThrow('renderer authority rejected');
    expect(authorize).toHaveBeenCalledTimes(2);
    expect(seam.reads).toEqual({ image: 1, text: 1 });
  });
});

class FakeTerminal implements TerminalClipboardTarget {
  handler: ((event: KeyboardEvent) => boolean) | undefined;
  pasted: string[] = [];

  attachCustomKeyEventHandler(handler: (event: KeyboardEvent) => boolean): void { this.handler = handler; }
  paste(value: string): void { this.pasted.push(value); }
}

function key(key: string, modifiers: Partial<Pick<KeyboardEvent, 'type' | 'code' | 'ctrlKey' | 'shiftKey' | 'altKey' | 'metaKey'>> = {}): KeyboardEvent {
  return {
    type: 'keydown',
    key,
    code: /^[a-z]$/i.test(key) ? `Key${key.toUpperCase()}` : '',
    ctrlKey: false,
    shiftKey: false,
    altKey: false,
    metaKey: false,
    ...modifiers,
  } as KeyboardEvent;
}

async function install(result: ClipboardReadResult) {
  const { installWindowsClipboardShortcuts } = await rendererClipboard();
  const terminal = new FakeTerminal();
  const requestTrustedClipboard = vi.fn(async () => result);
  installWindowsClipboardShortcuts(terminal, 'win32', requestTrustedClipboard);
  return { terminal, requestTrustedClipboard };
}

async function afterMicrotasks(): Promise<void> {
  await new Promise<void>((resolve) => queueMicrotask(resolve));
}

// xterm turns Ctrl+V into 0x16 unless its custom key handler returns false first.
describe('Windows terminal paste shortcuts', () => {
  it.each([
    ['Ctrl+V', key('v', { ctrlKey: true })],
    ['Ctrl+Shift+V', key('v', { ctrlKey: true, shiftKey: true })],
    ['Alt+V', key('v', { altKey: true })],
  ])('%s consumes the key before xterm and makes exactly one trusted clipboard request', async (_label, event) => {
    const { terminal, requestTrustedClipboard } = await install({ kind: 'image-path', path: 'C:\\void-temp\\pasted.png' });

    expect(terminal.handler?.(event)).toBe(false);
    await afterMicrotasks();

    expect(requestTrustedClipboard).toHaveBeenCalledOnce();
    expect(terminal.pasted).toEqual(['C:\\void-temp\\pasted.png']);
  });

  it('consumes physical Ctrl+V under a Russian layout, reads once, and pastes once', async () => {
    const { terminal, requestTrustedClipboard } = await install({ kind: 'text', text: 'paste exactly once' });

    expect(terminal.handler?.(key('м', { code: 'KeyV', ctrlKey: true }))).toBe(false);
    await afterMicrotasks();

    expect(requestTrustedClipboard).toHaveBeenCalledOnce();
    expect(terminal.pasted).toEqual(['paste exactly once']);
  });

  it('does not intercept a non-V physical key just because its layout key is v', async () => {
    const { terminal, requestTrustedClipboard } = await install({ kind: 'text', text: 'must not paste' });

    expect(terminal.handler?.(key('v', { code: 'KeyQ', ctrlKey: true }))).toBe(true);
    await afterMicrotasks();

    expect(requestTrustedClipboard).not.toHaveBeenCalled();
    expect(terminal.pasted).toEqual([]);
  });

  // xterm invokes the custom handler for both halves of one physical Ctrl+V gesture.
  it('consumes Windows Ctrl+V keydown once but passes its keyup through', async () => {
    const { terminal, requestTrustedClipboard } = await install({ kind: 'text', text: 'paste exactly once' });

    const keydownResult = terminal.handler?.(key('v', { type: 'keydown', ctrlKey: true }));
    const keyupResult = terminal.handler?.(key('v', { type: 'keyup', ctrlKey: true }));
    await afterMicrotasks();

    expect(requestTrustedClipboard).toHaveBeenCalledOnce();
    expect(terminal.pasted).toEqual(['paste exactly once']);
    expect(keydownResult).toBe(false);
    expect(keyupResult).toBe(true);
  });

  it('inserts text from the same trusted result path', async () => {
    const { terminal, requestTrustedClipboard } = await install({ kind: 'text', text: 'plain fallback' });

    expect(terminal.handler?.(key('v', { ctrlKey: true }))).toBe(false);
    expect(requestTrustedClipboard).toHaveBeenCalledOnce();
    await afterMicrotasks();

    expect(terminal.pasted).toEqual(['plain fallback']);
  });

  it('keeps an empty trusted read inert instead of injecting terminal input', async () => {
    const { installWindowsClipboardShortcuts } = await rendererClipboard();
    const terminal = new FakeTerminal();
    const empty = vi.fn(async (): Promise<ClipboardReadResult> => ({ kind: 'empty' }));
    installWindowsClipboardShortcuts(terminal, 'win32', empty);

    expect(terminal.handler?.(key('v', { ctrlKey: true }))).toBe(false);
    await afterMicrotasks();

    expect(empty).toHaveBeenCalledOnce();
    expect(terminal.pasted).toEqual([]);
  });

  it('keeps a rejected trusted read inert instead of crashing or injecting terminal input', async () => {
    const { installWindowsClipboardShortcuts } = await rendererClipboard();
    const terminal = new FakeTerminal();
    const failed = vi.fn(async (): Promise<ClipboardReadResult> => { throw new Error('clipboard unavailable'); });
    installWindowsClipboardShortcuts(terminal, 'win32', failed);

    expect(terminal.handler?.(key('v', { altKey: true }))).toBe(false);
    await afterMicrotasks();

    expect(failed).toHaveBeenCalledOnce();
    expect(terminal.pasted).toEqual([]);
  });

  it.each([
    ['Windows Ctrl+C', 'win32', key('c', { ctrlKey: true })],
    ['macOS Command+V', 'darwin', key('v', { metaKey: true })],
    ['Linux Ctrl+V', 'linux', key('v', { ctrlKey: true })],
  ])('%s passes through unchanged and never requests Windows clipboard authority', async (label, platform, event) => {
    const { installWindowsClipboardShortcuts } = await rendererClipboard();
    const terminal = new FakeTerminal();
    const requestTrustedClipboard = vi.fn(async (): Promise<ClipboardReadResult> => ({ kind: 'text', text: label }));
    installWindowsClipboardShortcuts(terminal, platform, requestTrustedClipboard);

    expect(terminal.handler?.(event)).toBe(true);
    expect(requestTrustedClipboard).not.toHaveBeenCalled();
    expect(terminal.pasted).toEqual([]);
  });
});

function sourceFile(path: string): ts.SourceFile {
  return ts.createSourceFile(path, readFileSync(new URL(path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
}

function namedFunction(source: ts.SourceFile, name: string): ts.FunctionDeclaration | undefined {
  return source.statements.find((statement): statement is ts.FunctionDeclaration => ts.isFunctionDeclaration(statement) && statement.name?.text === name);
}

function directCall(statement: ts.Statement | undefined, name: string): ts.CallExpression | undefined {
  if (!statement || !ts.isExpressionStatement(statement) || !ts.isCallExpression(statement.expression)
    || !ts.isIdentifier(statement.expression.expression) || statement.expression.expression.text !== name) return undefined;
  return statement.expression;
}

function objectLiteral(expression: ts.Expression | undefined): ts.ObjectLiteralExpression | undefined {
  if (expression && ts.isObjectLiteralExpression(expression)) return expression;
  if (expression && ts.isCallExpression(expression) && ts.isPropertyAccessExpression(expression.expression)
    && ts.isIdentifier(expression.expression.expression) && expression.expression.expression.text === 'Object'
    && expression.expression.name.text === 'freeze' && ts.isObjectLiteralExpression(expression.arguments[0])) return expression.arguments[0];
  return undefined;
}

function objectProperty(object: ts.ObjectLiteralExpression | undefined, name: string): ts.Expression | undefined {
  const property = object?.properties.find((item): item is ts.PropertyAssignment => ts.isPropertyAssignment(item)
    && (ts.isIdentifier(item.name) || ts.isStringLiteral(item.name)) && item.name.text === name);
  return property?.initializer;
}

// Deleting either boundary call silently removes Windows paste despite both units being tested.
describe('Windows clipboard process-boundary wiring', () => {
  it('installs the trusted clipboard handler on the terminal created by launch before open or onData', () => {
    const source = sourceFile('../src/renderer/index.ts');
    const launch = namedFunction(source, 'launch');
    expect(launch, 'the real renderer launch path exists').toBeDefined();
    const statements = (launch!.body as ts.Block).statements;
    const createdIndex = statements.findIndex((statement) => ts.isVariableStatement(statement)
      && statement.declarationList.declarations.some((declaration) => ts.isIdentifier(declaration.name) && ts.isCallExpression(declaration.initializer)
        && ts.isIdentifier(declaration.initializer.expression) && declaration.name.text === 'created' && declaration.initializer.expression.text === 'createProductTerminal'));
    const terminalIndex = statements.findIndex((statement) => ts.isVariableStatement(statement)
      && statement.declarationList.declarations.some((declaration) => ts.isObjectBindingPattern(declaration.name)
        && ts.isIdentifier(declaration.initializer) && declaration.initializer.text === 'created'
        && declaration.name.elements.some((element) => ts.isIdentifier(element.name) && element.name.text === 'terminal')));
    const installerIndex = statements.findIndex((statement) => Boolean(directCall(statement, 'installWindowsClipboardShortcuts')));
    const openIndex = statements.findIndex((statement) => ts.isExpressionStatement(statement) && ts.isCallExpression(statement.expression)
      && ts.isPropertyAccessExpression(statement.expression.expression) && ts.isIdentifier(statement.expression.expression.expression)
      && statement.expression.expression.expression.text === 'terminal' && statement.expression.expression.name.text === 'open');
    const onDataIndex = statements.findIndex((statement) => ts.isExpressionStatement(statement) && ts.isCallExpression(statement.expression)
      && ts.isPropertyAccessExpression(statement.expression.expression) && ts.isIdentifier(statement.expression.expression.expression)
      && statement.expression.expression.expression.text === 'terminal' && statement.expression.expression.name.text === 'onData');
    const installer = directCall(statements[installerIndex]!, 'installWindowsClipboardShortcuts');

    expect(createdIndex).toBeGreaterThan(-1);
    expect(terminalIndex).toBeGreaterThan(createdIndex);
    expect(installerIndex).toBeGreaterThan(terminalIndex);
    expect(installerIndex).toBeLessThan(openIndex);
    expect(installerIndex).toBeLessThan(onDataIndex);
    expect(ts.isIdentifier(installer!.arguments[0]) && installer!.arguments[0].text).toBe('terminal');
    expect(ts.isIdentifier(installer!.arguments[1]) && installer!.arguments[1].text).toBe('rendererPlatform');
    const trustedRead = installer!.arguments[2];
    expect(ts.isArrowFunction(trustedRead)).toBe(true);
    expect(ts.isCallExpression((trustedRead as ts.ArrowFunction).body)).toBe(true);
    const read = (trustedRead as ts.ArrowFunction).body as ts.CallExpression;
    expect(ts.isPropertyAccessExpression(read.expression) && read.expression.getText(source)).toBe('window.voidTerminal.clipboard.read');
  });

  it('preload exposes clipboard.read as the narrow clipboardRead IPC invocation', () => {
    const source = sourceFile('../src/preload/index.ts');
    const apiStatement = source.statements.find((statement): statement is ts.VariableStatement => ts.isVariableStatement(statement)
      && statement.declarationList.declarations.some((declaration) => ts.isIdentifier(declaration.name) && declaration.name.text === 'api'));
    const apiDeclaration = apiStatement?.declarationList.declarations.find((declaration) => ts.isIdentifier(declaration.name) && declaration.name.text === 'api');
    const api = objectLiteral(apiDeclaration?.initializer);
    const clipboard = objectLiteral(objectProperty(api, 'clipboard'));
    const read = objectProperty(clipboard, 'read');

    expect(read, 'preload exposes clipboard.read').toBeDefined();
    if (!read) return;
    expect(ts.isArrowFunction(read)).toBe(true);
    const body = (read as ts.ArrowFunction).body;
    expect(ts.isCallExpression(body), 'clipboard.read directly invokes IPC').toBe(true);
    const invocation = body as ts.CallExpression;
    expect(ts.isPropertyAccessExpression(invocation.expression) && invocation.expression.expression.getText(source)).toBe('ipcRenderer');
    expect(ts.isPropertyAccessExpression(invocation.expression) && invocation.expression.name.text).toBe('invoke');
    expect(invocation.arguments).toHaveLength(1);
    expect(invocation.arguments[0].getText(source)).toBe('IPC.clipboardRead');
  });
});
