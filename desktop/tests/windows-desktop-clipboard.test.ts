import { readFileSync } from 'node:fs';
import path from 'node:path';
import ts from 'typescript';
import { describe, expect, it, vi } from 'vitest';

type ClipboardReadResult = { kind: 'empty' } | { kind: 'text'; text: string } | { kind: 'image-path'; path: string };
type FakeImage = { isEmpty(): boolean; toPNG(): Buffer };
// This is deliberately narrower than a filesystem seam: image persistence is owned by the
// process-scoped store, and every valid reader dependency must carry that writer.
type ClipboardReadDependencies = {
  clipboard: { readImage(): FakeImage; readText(): string };
  writeImage(png: Buffer): string;
};
type ClipboardPasteModule = {
  readDesktopClipboard(dependencies: ClipboardReadDependencies): ClipboardReadResult;
  createTrustedClipboardReadHandler<Event>(authorize: (event: Event) => void, dependencies: ClipboardReadDependencies): (event: Event) => Promise<ClipboardReadResult>;
};
type TerminalClipboardTarget = {
  attachCustomKeyEventHandler(handler: (event: KeyboardEvent) => boolean): void;
  getSelection(): string;
  paste(value: string): void;
};
// The reservation is created at keydown, before the async read. emit() captures the
// synchronous xterm onData caused by paste() in that reservation's FIFO position.
type TerminalInputReservation = {
  emit(action: () => void): void;
  discard(): void;
};
type OrderedTerminalInputSink = {
  send(data: string): void;
  reserve(): TerminalInputReservation;
};
type ClipboardShortcutsModule = {
  createOrderedTerminalInputSink(send: (data: string) => void): OrderedTerminalInputSink;
  installWindowsClipboardShortcuts(
    target: TerminalClipboardTarget,
    platform: string,
    readTrustedClipboard: () => Promise<ClipboardReadResult>,
    terminalInput: OrderedTerminalInputSink,
    writeTrustedClipboard?: (text: string) => Promise<void>,
    now?: () => number,
  ): void;
};
type ClipboardContractModule = {
  clipboardWriteRequest?: (raw: unknown) => string;
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

async function clipboardContract(): Promise<ClipboardContractModule> {
  return import(new URL('../src/shared/contract.ts', import.meta.url).href) as Promise<ClipboardContractModule>;
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
    writeImage: (png) => {
      const id = ids.shift();
      if (!id) throw new Error('test owned image store exhausted');
      const file = `C:\\void-code-owned-images\\void-code-clipboard-${id}.png`;
      writes.push({ path: file, png });
      return file;
    },
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
  it('prefers a non-empty image over text and writes its exact bytes to distinct absolute paths in the owned store', async () => {
    const { readDesktopClipboard } = await mainClipboard();
    const png = Buffer.from([0x89, 0x50, 0x4e, 0x47, 1]);
    const seam = dependencies({ image: nonEmptyImage(png), text: 'this text must lose to the image' });

    const first = readDesktopClipboard(seam);
    const second = readDesktopClipboard(seam);

    expect(first).toEqual({ kind: 'image-path', path: 'C:\\void-code-owned-images\\void-code-clipboard-first.png' });
    expect(second).toEqual({ kind: 'image-path', path: 'C:\\void-code-owned-images\\void-code-clipboard-second.png' });
    expect(seam.writes).toEqual([
      { path: 'C:\\void-code-owned-images\\void-code-clipboard-first.png', png },
      { path: 'C:\\void-code-owned-images\\void-code-clipboard-second.png', png },
    ]);
    for (const result of [first, second]) {
      expect(result.kind).toBe('image-path');
      if (result.kind === 'image-path') {
        expect(path.win32.isAbsolute(result.path)).toBe(true);
        expect(path.win32.dirname(result.path)).toBe('C:\\void-code-owned-images');
      }
    }
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

  it('does not fall back to text when the owned image writer fails', async () => {
    const { readDesktopClipboard } = await mainClipboard();
    const png = Buffer.from([0x89, 0x50, 0x4e, 0x47, 3]);
    const seam = dependencies({ image: nonEmptyImage(png), text: 'do not fall back after a write failure' });
    const failedWrite = vi.fn((): string => { throw new Error('disk full'); });
    seam.writeImage = failedWrite;

    expect(readDesktopClipboard(seam)).toEqual({ kind: 'empty' });
    expect(failedWrite).toHaveBeenCalledOnce();
    expect(failedWrite).toHaveBeenCalledWith(png);
    expect(seam.reads).toEqual({ image: 1, text: 0 });
  });

  it.each([
    ['missing', {}],
    ['malformed', { writeImage: null }],
  ])('treats a %s owned image writer as an image-only failure, never as permission to write a loose shared-temp file', async (_label, malformedWriter) => {
    const { readDesktopClipboard } = await mainClipboard();
    const temporaryDirectory = vi.fn(() => 'C:\\shared-temp');
    const writeFile = vi.fn();
    const uniqueId = vi.fn(() => 'loose-file');
    const looseFallback = {
      clipboard: {
        readImage: () => nonEmptyImage(Buffer.from([0x89, 0x50, 0x4e, 0x47, 4])),
        readText: vi.fn(() => 'text must not be injected'),
      },
      filesystem: { temporaryDirectory, writeFile },
      uniqueId,
      ...malformedWriter,
    };

    // IPC inputs are runtime data. The deliberate cast reaches the malformed state that the
    // required fixture contract forbids at compile time.
    expect(readDesktopClipboard(looseFallback as unknown as ClipboardReadDependencies)).toEqual({ kind: 'empty' });
    expect(temporaryDirectory).not.toHaveBeenCalled();
    expect(writeFile).not.toHaveBeenCalled();
    expect(uniqueId).not.toHaveBeenCalled();
    expect(looseFallback.clipboard.readText).not.toHaveBeenCalled();
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
  selection = '';
  onPaste: ((value: string) => void) | undefined;

  attachCustomKeyEventHandler(handler: (event: KeyboardEvent) => boolean): void { this.handler = handler; }
  getSelection(): string { return this.selection; }
  paste(value: string): void { this.pasted.push(value); this.onPaste?.(value); }
}

type TestKeyEvent = KeyboardEvent & { preventDefault: ReturnType<typeof vi.fn> };

function key(key: string, modifiers: Partial<Pick<KeyboardEvent, 'type' | 'code' | 'ctrlKey' | 'shiftKey' | 'altKey' | 'metaKey' | 'repeat'>> = {}): TestKeyEvent {
  return {
    type: 'keydown',
    key,
    code: /^[a-z]$/i.test(key) ? `Key${key.toUpperCase()}` : '',
    ctrlKey: false,
    shiftKey: false,
    altKey: false,
    metaKey: false,
    repeat: false,
    preventDefault: vi.fn(),
    ...modifiers,
  } as TestKeyEvent;
}

class RecordingOrderedTerminalInput implements OrderedTerminalInputSink {
  sent: string[] = [];
  private readonly entries: Array<{ ready: boolean; data: string[] }> = [];
  private active: { ready: boolean; data: string[] } | undefined;

  send(data: string): void {
    if (this.active) this.active.data.push(data);
    else { this.entries.push({ ready: true, data: [data] }); this.flush(); }
  }

  reserve(): TerminalInputReservation {
    const entry = { ready: false, data: [] as string[] };
    this.entries.push(entry);
    return {
      emit: (action) => {
        this.active = entry;
        try { action(); } finally { this.active = undefined; entry.ready = true; this.flush(); }
      },
      discard: () => { entry.ready = true; this.flush(); },
    };
  }

  private flush(): void {
    while (this.entries[0]?.ready) this.sent.push(...this.entries.shift()!.data);
  }
}

function inertTerminalInput(): OrderedTerminalInputSink {
  return { send: () => undefined, reserve: () => ({ emit: (action) => action(), discard: () => undefined }) };
}

async function install(result: ClipboardReadResult) {
  const { installWindowsClipboardShortcuts } = await rendererClipboard();
  const terminal = new FakeTerminal();
  const requestTrustedClipboard = vi.fn(async () => result);
  installWindowsClipboardShortcuts(terminal, 'win32', requestTrustedClipboard, inertTerminalInput());
  return { terminal, requestTrustedClipboard };
}

async function installCopy(selection: string, platform = 'win32', write = vi.fn(async () => undefined)) {
  const { installWindowsClipboardShortcuts } = await rendererClipboard();
  const terminal = new FakeTerminal();
  terminal.selection = selection;
  const readTrustedClipboard = vi.fn(async (): Promise<ClipboardReadResult> => ({ kind: 'text', text: 'must not read while copying' }));
  const terminalInput = {
    send: vi.fn(() => undefined),
    reserve: vi.fn((): TerminalInputReservation => ({ emit: vi.fn(), discard: vi.fn() })),
  };
  installWindowsClipboardShortcuts(terminal, platform, readTrustedClipboard, terminalInput, write);
  return { terminal, readTrustedClipboard, terminalInput, write };
}

function deferred<T>(): { promise: Promise<T>; resolve(value: T): void; reject(reason: unknown): void } {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => { resolve = resolvePromise; reject = rejectPromise; });
  return { promise, resolve, reject };
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
    expect(event.preventDefault).toHaveBeenCalledOnce();
    await afterMicrotasks();

    expect(requestTrustedClipboard).toHaveBeenCalledOnce();
    expect(terminal.pasted).toEqual(['C:\\void-temp\\pasted.png']);
  });

  it('consumes physical Ctrl+V under a Russian layout, prevents its default, reads once, and pastes once', async () => {
    const { terminal, requestTrustedClipboard } = await install({ kind: 'text', text: 'paste exactly once' });
    const event = key('м', { code: 'KeyV', ctrlKey: true });

    expect(terminal.handler?.(event)).toBe(false);
    expect(event.preventDefault).toHaveBeenCalledOnce();
    await afterMicrotasks();

    expect(requestTrustedClipboard).toHaveBeenCalledOnce();
    expect(terminal.pasted).toEqual(['paste exactly once']);
  });

  it('does not intercept a non-V physical key just because its layout key is v', async () => {
    const { terminal, requestTrustedClipboard } = await install({ kind: 'text', text: 'must not paste' });

    const unrelated = key('v', { code: 'KeyQ', ctrlKey: true });
    expect(terminal.handler?.(unrelated)).toBe(true);
    await afterMicrotasks();

    expect(unrelated.preventDefault).not.toHaveBeenCalled();
    expect(requestTrustedClipboard).not.toHaveBeenCalled();
    expect(terminal.pasted).toEqual([]);
  });

  // xterm invokes the custom handler for both halves of one physical Ctrl+V gesture.
  it('consumes Windows Ctrl+V keydown once but leaves keyup default behavior alone', async () => {
    const { terminal, requestTrustedClipboard } = await install({ kind: 'text', text: 'paste exactly once' });
    const keydown = key('v', { type: 'keydown', ctrlKey: true });
    const keyup = key('v', { type: 'keyup', ctrlKey: true });

    const keydownResult = terminal.handler?.(keydown);
    const keyupResult = terminal.handler?.(keyup);
    await afterMicrotasks();

    expect(requestTrustedClipboard).toHaveBeenCalledOnce();
    expect(terminal.pasted).toEqual(['paste exactly once']);
    expect(keydownResult).toBe(false);
    expect(keyupResult).toBe(true);
    expect(keydown.preventDefault).toHaveBeenCalledOnce();
    expect(keyup.preventDefault).not.toHaveBeenCalled();
  });

  // A held shortcut repeats keydown events; only the initial gesture may read the clipboard.
  it('consumes and prevents repeated Ctrl+V keydown without starting another clipboard read', async () => {
    const { installWindowsClipboardShortcuts } = await rendererClipboard();
    const terminal = new FakeTerminal();
    const pending = deferred<ClipboardReadResult>();
    const readTrustedClipboard = vi.fn(() => pending.promise);
    installWindowsClipboardShortcuts(terminal, 'win32', readTrustedClipboard, inertTerminalInput());
    const initial = key('v', { ctrlKey: true });
    const repeated = key('v', { ctrlKey: true, repeat: true });

    expect(terminal.handler?.(initial)).toBe(false);
    expect(terminal.handler?.(repeated)).toBe(false);
    expect(readTrustedClipboard).toHaveBeenCalledOnce();
    expect(initial.preventDefault).toHaveBeenCalledOnce();
    expect(repeated.preventDefault).toHaveBeenCalledOnce();

    pending.resolve({ kind: 'text', text: 'paste once' });
    await afterMicrotasks();

    expect(terminal.pasted).toEqual(['paste once']);
  });

  // Without the reservation created at keydown, later xterm onData reaches Pi before the async clipboard result.
  it('sends a deferred clipboard paste through the terminal-input sink before later terminal data', async () => {
    const { installWindowsClipboardShortcuts } = await rendererClipboard();
    const terminalInput = new RecordingOrderedTerminalInput();
    const terminal = new FakeTerminal();
    terminal.onPaste = (value) => { terminalInput.send(value); };
    const pending = deferred<ClipboardReadResult>();
    installWindowsClipboardShortcuts(terminal, 'win32', () => pending.promise, terminalInput);

    expect(terminal.handler?.(key('v', { ctrlKey: true }))).toBe(false);
    terminalInput.send('later terminal data');
    pending.resolve({ kind: 'text', text: 'clipboard text' });
    await afterMicrotasks();

    expect(terminal.pasted).toEqual(['clipboard text']);
    expect(terminalInput.sent).toEqual(['clipboard text', 'later terminal data']);
  });

  // An empty async read must settle its reservation, or all later xterm input stays stuck behind it.
  it('releases later terminal data without pasting when a pending trusted read resolves empty', async () => {
    const { createOrderedTerminalInputSink, installWindowsClipboardShortcuts } = await rendererClipboard();
    const sent: string[] = [];
    const terminalInput = createOrderedTerminalInputSink((data) => { sent.push(data); });
    const terminal = new FakeTerminal();
    terminal.onPaste = (value) => { terminalInput.send(value); };
    const pending = deferred<ClipboardReadResult>();
    const readTrustedClipboard = vi.fn(() => pending.promise);
    installWindowsClipboardShortcuts(terminal, 'win32', readTrustedClipboard, terminalInput);

    expect(terminal.handler?.(key('v', { ctrlKey: true }))).toBe(false);
    expect(readTrustedClipboard).toHaveBeenCalledOnce();
    terminalInput.send('later terminal data');
    expect(sent).toEqual([]);

    pending.resolve({ kind: 'empty' });
    await afterMicrotasks();

    expect(terminal.pasted).toEqual([]);
    expect(sent).toEqual(['later terminal data']);
  });

  // A failed async read must settle the same FIFO slot rather than permanently block later input.
  it('releases later terminal data without pasting when a pending trusted read rejects', async () => {
    const { createOrderedTerminalInputSink, installWindowsClipboardShortcuts } = await rendererClipboard();
    const sent: string[] = [];
    const terminalInput = createOrderedTerminalInputSink((data) => { sent.push(data); });
    const terminal = new FakeTerminal();
    terminal.onPaste = (value) => { terminalInput.send(value); };
    const pending = deferred<ClipboardReadResult>();
    const readTrustedClipboard = vi.fn(() => pending.promise);
    installWindowsClipboardShortcuts(terminal, 'win32', readTrustedClipboard, terminalInput);

    expect(terminal.handler?.(key('v', { altKey: true }))).toBe(false);
    expect(readTrustedClipboard).toHaveBeenCalledOnce();
    terminalInput.send('later terminal data');
    expect(sent).toEqual([]);

    pending.reject(new Error('clipboard unavailable'));
    await afterMicrotasks();

    expect(terminal.pasted).toEqual([]);
    expect(sent).toEqual(['later terminal data']);
  });

  it('at exactly five seconds discards a pending trusted read, releases FIFO input, and ignores a late successful result', async () => {
    vi.useFakeTimers();
    try {
      const { createOrderedTerminalInputSink, installWindowsClipboardShortcuts } = await rendererClipboard();
      const sent: string[] = [];
      const terminalInput = createOrderedTerminalInputSink((data) => { sent.push(data); });
      const terminal = new FakeTerminal();
      terminal.onPaste = (value) => { terminalInput.send(value); };
      const pending = deferred<ClipboardReadResult>();
      const readTrustedClipboard = vi.fn(() => pending.promise);
      installWindowsClipboardShortcuts(terminal, 'win32', readTrustedClipboard, terminalInput);

      expect(terminal.handler?.(key('v', { ctrlKey: true }))).toBe(false);
      expect(readTrustedClipboard).toHaveBeenCalledOnce();
      terminalInput.send('later terminal data');
      expect(sent).toEqual([]);

      await vi.advanceTimersByTimeAsync(4_999);
      expect(sent).toEqual([]);
      await vi.advanceTimersByTimeAsync(1);
      expect(terminal.pasted).toEqual([]);
      expect(sent).toEqual(['later terminal data']);

      pending.resolve({ kind: 'text', text: 'late clipboard text' });
      await vi.advanceTimersByTimeAsync(0);

      expect(readTrustedClipboard).toHaveBeenCalledOnce();
      expect(terminal.pasted).toEqual([]);
      expect(sent).toEqual(['later terminal data']);
    } finally {
      vi.useRealTimers();
    }
  });

  it('does not discard a prompt clipboard result when Date.now jumps forward', async () => {
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date(0));
      const { installWindowsClipboardShortcuts } = await rendererClipboard();
      const terminal = new FakeTerminal();
      const pending = deferred<ClipboardReadResult>();
      installWindowsClipboardShortcuts(terminal, 'win32', () => pending.promise, inertTerminalInput());

      expect(terminal.handler?.(key('v', { ctrlKey: true }))).toBe(false);
      vi.setSystemTime(new Date(60_000));
      pending.resolve({ kind: 'text', text: 'on-time clipboard text' });
      await afterMicrotasks();

      expect(terminal.pasted).toEqual(['on-time clipboard text']);
    } finally {
      vi.useRealTimers();
    }
  });

  it('G4 — discards a clipboard promise that settles after its deadline before the stalled timeout callback can run', async () => {
    vi.useFakeTimers();
    try {
      const { createOrderedTerminalInputSink, installWindowsClipboardShortcuts } = await rendererClipboard();
      const sent: string[] = [];
      const terminalInput = createOrderedTerminalInputSink((data) => { sent.push(data); });
      const terminal = new FakeTerminal();
      terminal.onPaste = (value) => { terminalInput.send(value); };
      const pending = deferred<ClipboardReadResult>();
      let elapsed = 0;
      installWindowsClipboardShortcuts(terminal, 'win32', () => pending.promise, terminalInput, undefined, () => elapsed);

      expect(terminal.handler?.(key('v', { ctrlKey: true }))).toBe(false);
      terminalInput.send('queued after paste');
      expect(sent).toEqual([]);

      // Advance the injected elapsed-time clock without executing the overdue timeout callback.
      elapsed = 5_001;
      expect(vi.getTimerCount(), 'the five-second timeout callback has not run').toBe(1);
      pending.resolve({ kind: 'text', text: 'already too late' });
      await afterMicrotasks();

      expect(terminal.pasted).toEqual([]);
      expect(sent).toEqual(['queued after paste']);

      await vi.runOnlyPendingTimersAsync();
      expect(terminal.pasted).toEqual([]);
      expect(sent).toEqual(['queued after paste']);
    } finally {
      vi.useRealTimers();
    }
  });

  // The real renderer must use the same reservation semantics, not only satisfy the shortcut seam with a fake sink.
  it('keeps terminal data sent during an emitted reservation behind that reservation', async () => {
    const { createOrderedTerminalInputSink } = await rendererClipboard();
    const sent: string[] = [];
    const terminalInput = createOrderedTerminalInputSink((data) => { sent.push(data); });
    const reservation = terminalInput.reserve();

    terminalInput.send('later terminal data');
    reservation.emit(() => { terminalInput.send('clipboard text'); });

    expect(sent).toEqual(['clipboard text', 'later terminal data']);
  });

  // A reservation is one transaction: a second emit must not invoke another terminal paste.
  it('allows only the first emit to contribute terminal data', async () => {
    const { createOrderedTerminalInputSink } = await rendererClipboard();
    const sent: string[] = [];
    const terminalInput = createOrderedTerminalInputSink((data) => { sent.push(data); });
    const terminal = new FakeTerminal();
    terminal.onPaste = (value) => { terminalInput.send(value); };
    const reservation = terminalInput.reserve();

    terminalInput.send('later terminal data');
    reservation.emit(() => { terminal.paste('first clipboard text'); });
    reservation.emit(() => { terminal.paste('second clipboard text'); });

    expect(terminal.pasted).toEqual(['first clipboard text']);
    expect(sent).toEqual(['first clipboard text', 'later terminal data']);
  });

  // A discarded transaction cannot revive; the queued successor must be forwarded only once.
  it('does not emit after discard and releases later terminal data exactly once', async () => {
    const { createOrderedTerminalInputSink } = await rendererClipboard();
    const sent: string[] = [];
    const terminalInput = createOrderedTerminalInputSink((data) => { sent.push(data); });
    const terminal = new FakeTerminal();
    terminal.onPaste = (value) => { terminalInput.send(value); };
    const reservation = terminalInput.reserve();

    terminalInput.send('later terminal data');
    reservation.discard();
    reservation.emit(() => { terminal.paste('discarded clipboard text'); });

    expect(terminal.pasted).toEqual([]);
    expect(sent).toEqual(['later terminal data']);
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
    installWindowsClipboardShortcuts(terminal, 'win32', empty, inertTerminalInput());

    expect(terminal.handler?.(key('v', { ctrlKey: true }))).toBe(false);
    await afterMicrotasks();

    expect(empty).toHaveBeenCalledOnce();
    expect(terminal.pasted).toEqual([]);
  });

  it('keeps a rejected trusted read inert instead of crashing or injecting terminal input', async () => {
    const { installWindowsClipboardShortcuts } = await rendererClipboard();
    const terminal = new FakeTerminal();
    const failed = vi.fn(async (): Promise<ClipboardReadResult> => { throw new Error('clipboard unavailable'); });
    installWindowsClipboardShortcuts(terminal, 'win32', failed, inertTerminalInput());

    expect(terminal.handler?.(key('v', { altKey: true }))).toBe(false);
    await afterMicrotasks();

    expect(failed).toHaveBeenCalledOnce();
    expect(terminal.pasted).toEqual([]);
  });

});

// xterm maps an unshifted Ctrl+C to the terminal interrupt byte. It may only be
// consumed when there is an exact selection to send across the trusted write IPC.
describe('Windows terminal copy shortcuts', () => {
  it.each([
    ['Ctrl+C', key('c', { ctrlKey: true })],
    ['Ctrl+Shift+C', key('c', { ctrlKey: true, shiftKey: true })],
    ['physical Ctrl+C under a non-Latin layout', key('с', { code: 'KeyC', ctrlKey: true })],
  ])('%s copies the exact non-empty xterm selection once and consumes the browser and xterm key paths', async (_label, event) => {
    const selection = '  first line\r\nΔругая\tline  ';
    const { terminal, readTrustedClipboard, terminalInput, write } = await installCopy(selection);

    expect(terminal.handler?.(event)).toBe(false);
    expect(event.preventDefault).toHaveBeenCalledOnce();
    await afterMicrotasks();

    expect(write).toHaveBeenCalledOnce();
    expect(write).toHaveBeenCalledWith(selection);
    expect(readTrustedClipboard).not.toHaveBeenCalled();
    expect(terminalInput.reserve).not.toHaveBeenCalled();
    expect(terminalInput.send).not.toHaveBeenCalled();
    expect(terminal.pasted).toEqual([]);
  });

  it('leaves Ctrl+C with no selection untouched as the terminal interrupt', async () => {
    const event = key('c', { ctrlKey: true });
    const { terminal, readTrustedClipboard, terminalInput, write } = await installCopy('');

    expect(terminal.handler?.(event)).toBe(true);
    await afterMicrotasks();

    expect(event.preventDefault).not.toHaveBeenCalled();
    expect(write).not.toHaveBeenCalled();
    expect(readTrustedClipboard).not.toHaveBeenCalled();
    expect(terminalInput.reserve).not.toHaveBeenCalled();
    expect(terminalInput.send).not.toHaveBeenCalled();
    expect(terminal.pasted).toEqual([]);
  });

  it('consumes Ctrl+Shift+C with no selection as an inert copy gesture, never as an interrupt', async () => {
    const event = key('c', { ctrlKey: true, shiftKey: true });
    const { terminal, readTrustedClipboard, terminalInput, write } = await installCopy('');

    expect(terminal.handler?.(event)).toBe(false);
    await afterMicrotasks();

    expect(event.preventDefault).toHaveBeenCalledOnce();
    expect(write).not.toHaveBeenCalled();
    expect(readTrustedClipboard).not.toHaveBeenCalled();
    expect(terminalInput.reserve).not.toHaveBeenCalled();
    expect(terminalInput.send).not.toHaveBeenCalled();
    expect(terminal.pasted).toEqual([]);
  });

  it('consumes repeat but lets keyup through without duplicating the one clipboard write', async () => {
    const selection = 'one selection';
    const { terminal, write } = await installCopy(selection);
    const keydown = key('c', { type: 'keydown', ctrlKey: true });
    const repeated = key('c', { type: 'keydown', ctrlKey: true, repeat: true });
    const keyup = key('c', { type: 'keyup', ctrlKey: true });

    expect(terminal.handler?.(keydown)).toBe(false);
    expect(terminal.handler?.(repeated)).toBe(false);
    expect(terminal.handler?.(keyup)).toBe(true);
    await afterMicrotasks();

    expect(write).toHaveBeenCalledOnce();
    expect(write).toHaveBeenCalledWith(selection);
    expect(keydown.preventDefault).toHaveBeenCalledOnce();
    expect(repeated.preventDefault).toHaveBeenCalledOnce();
    expect(keyup.preventDefault).not.toHaveBeenCalled();
  });

  it.each([
    ['a rejected write', vi.fn(async (): Promise<void> => { throw new Error('clipboard unavailable'); })],
    ['a synchronous write failure', vi.fn((): Promise<void> => { throw new Error('clipboard unavailable'); })],
  ])('keeps %s inert: no crash, paste, read, queue reservation, or terminal injection', async (_label, failedWrite) => {
    const { terminal, readTrustedClipboard, terminalInput } = await installCopy('private selection', 'win32', failedWrite);
    const event = key('c', { ctrlKey: true });

    expect(() => terminal.handler?.(event)).not.toThrow();
    expect(event.preventDefault).toHaveBeenCalledOnce();
    await afterMicrotasks();

    expect(failedWrite).toHaveBeenCalledOnce();
    expect(readTrustedClipboard).not.toHaveBeenCalled();
    expect(terminalInput.reserve).not.toHaveBeenCalled();
    expect(terminalInput.send).not.toHaveBeenCalled();
    expect(terminal.pasted).toEqual([]);
  });
});

describe('clipboard shortcut platform isolation', () => {
  it.each([
    ['Windows Ctrl+C without a selection', 'win32', key('c', { ctrlKey: true }), ''],
    ['Windows Alt+C with a selection', 'win32', key('c', { altKey: true }), 'selected'],
    ['macOS Command+V', 'darwin', key('v', { metaKey: true }), 'selected'],
    ['Linux Ctrl+V', 'linux', key('v', { ctrlKey: true }), 'selected'],
    ['Linux Ctrl+Shift+C with a selection', 'linux', key('c', { ctrlKey: true, shiftKey: true }), 'selected'],
  ])('%s passes through unchanged and never requests clipboard authority', async (_label, platform, event, selection) => {
    const { terminal, readTrustedClipboard, write } = await installCopy(selection, platform);

    expect(terminal.handler?.(event)).toBe(true);
    await afterMicrotasks();

    expect(event.preventDefault).not.toHaveBeenCalled();
    expect(readTrustedClipboard).not.toHaveBeenCalled();
    expect(write).not.toHaveBeenCalled();
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
  const property = object?.properties.find((item) => (ts.isPropertyAssignment(item) || ts.isShorthandPropertyAssignment(item))
    && (ts.isIdentifier(item.name) || ts.isStringLiteral(item.name)) && item.name.text === name);
  if (property && ts.isPropertyAssignment(property)) return property.initializer;
  return property && ts.isShorthandPropertyAssignment(property) ? property.name : undefined;
}

function namedTypeMember(source: ts.SourceFile, typeName: string, memberName: string): ts.TypeElement | undefined {
  const declaration = source.statements.find((statement): statement is ts.TypeAliasDeclaration => ts.isTypeAliasDeclaration(statement)
    && statement.name.text === typeName);
  if (!declaration || !ts.isTypeLiteralNode(declaration.type)) return undefined;
  return declaration.type.members.find((member) => (ts.isPropertySignature(member) || ts.isMethodSignature(member))
    && (ts.isIdentifier(member.name) || ts.isStringLiteral(member.name)) && member.name.text === memberName);
}

function namedVariableObject(source: ts.SourceFile, name: string): ts.ObjectLiteralExpression | undefined {
  for (const statement of source.statements) {
    if (!ts.isVariableStatement(statement)) continue;
    const declaration = statement.declarationList.declarations.find((candidate) => ts.isIdentifier(candidate.name) && candidate.name.text === name);
    if (declaration) return objectLiteral(declaration.initializer);
  }
  return undefined;
}

// Deleting a boundary call silently removes Windows paste/copy despite the seam units being tested.
describe('Windows clipboard process-boundary wiring', () => {
  it('makes the process-owned image writer required in production and leaves no loose-file persistence seam', () => {
    const reader = sourceFile('../src/main/clipboard-paste.ts');
    const writeImage = namedTypeMember(reader, 'ClipboardReadDependencies', 'writeImage');

    expect(writeImage, 'ClipboardReadDependencies must name the owned image writer').toBeDefined();
    expect(writeImage?.questionToken === undefined, 'omitting the production writer must fail TypeScript compilation').toBe(true);
    expect(namedTypeMember(reader, 'ClipboardReadDependencies', 'filesystem'), 'the reader must not retain a shared-temp filesystem fallback').toBeUndefined();
    expect(namedTypeMember(reader, 'ClipboardReadDependencies', 'uniqueId'), 'the reader must not retain a loose-file naming fallback').toBeUndefined();

    const main = sourceFile('../src/main/index.ts');
    const productionDependencies = namedVariableObject(main, 'desktopClipboardDependencies');
    const productionWriter = objectProperty(productionDependencies, 'writeImage');
    expect(productionWriter, 'production dependencies must supply the process-owned writer').toBeDefined();
    expect(productionWriter?.getText(main)).toContain('clipboardImageStorage.writeImage');
    expect(objectProperty(productionDependencies, 'filesystem')).toBeUndefined();
    expect(objectProperty(productionDependencies, 'uniqueId')).toBeUndefined();
  });

  it('delegates shortcuts and real xterm onData to the single product wiring seam before open', () => {
    const source = sourceFile('../src/renderer/index.ts');
    const clipboardImport = source.statements.find((statement): statement is ts.ImportDeclaration => ts.isImportDeclaration(statement)
      && ts.isStringLiteral(statement.moduleSpecifier) && statement.moduleSpecifier.text === './clipboard-shortcuts');
    const importedNames = clipboardImport?.importClause?.namedBindings && ts.isNamedImports(clipboardImport.importClause.namedBindings)
      ? clipboardImport.importClause.namedBindings.elements.map((element) => element.name.text)
      : [];
    expect(importedNames).toContain('wireProductTerminalClipboard');
    expect(importedNames).not.toContain('createOrderedTerminalInputSink');
    expect(importedNames).not.toContain('installWindowsClipboardShortcuts');

    const launch = namedFunction(source, 'launch');
    expect(launch, 'the real renderer launch path exists').toBeDefined();
    const statements = (launch!.body as ts.Block).statements;
    const terminalIndex = statements.findIndex((statement) => ts.isVariableStatement(statement)
      && statement.declarationList.declarations.some((declaration) => ts.isObjectBindingPattern(declaration.name)
        && ts.isIdentifier(declaration.initializer) && declaration.initializer.text === 'created'
        && declaration.name.elements.some((element) => ts.isIdentifier(element.name) && element.name.text === 'terminal')));
    const wiringIndex = statements.findIndex((statement) => Boolean(directCall(statement, 'wireProductTerminalClipboard')));
    const openIndex = statements.findIndex((statement) => ts.isExpressionStatement(statement) && ts.isCallExpression(statement.expression)
      && ts.isPropertyAccessExpression(statement.expression.expression) && ts.isIdentifier(statement.expression.expression.expression)
      && statement.expression.expression.expression.text === 'terminal' && statement.expression.expression.name.text === 'open');
    const directOnData = statements.find((statement) => ts.isExpressionStatement(statement) && ts.isCallExpression(statement.expression)
      && ts.isPropertyAccessExpression(statement.expression.expression) && ts.isIdentifier(statement.expression.expression.expression)
      && statement.expression.expression.expression.text === 'terminal' && statement.expression.expression.name.text === 'onData');
    const wiring = directCall(statements[wiringIndex]!, 'wireProductTerminalClipboard');

    expect(terminalIndex).toBeGreaterThan(-1);
    expect(wiringIndex).toBeGreaterThan(terminalIndex);
    expect(wiringIndex).toBeLessThan(openIndex);
    expect(directOnData, 'launch must not bypass the product seam with its own terminal.onData').toBeUndefined();
    expect(wiring?.arguments).toHaveLength(5);
    expect(ts.isIdentifier(wiring!.arguments[0]) && wiring!.arguments[0].text).toBe('terminal');
    expect(ts.isIdentifier(wiring!.arguments[1]) && wiring!.arguments[1].text).toBe('rendererPlatform');

    const trustedRead = wiring!.arguments[2];
    expect(ts.isArrowFunction(trustedRead)).toBe(true);
    expect(trustedRead.getText(source)).toContain('window.voidTerminal.clipboard.read()');

    const trustedWrite = wiring!.arguments[3];
    expect(ts.isArrowFunction(trustedWrite), 'the seam receives the trusted clipboard writer').toBe(true);
    expect(trustedWrite.getText(source)).toContain('window.voidTerminal.clipboard.write');

    const send = wiring!.arguments[4];
    expect(ts.isArrowFunction(send), 'the seam receives the owned terminal-input sender').toBe(true);
    expect(send.getText(source)).toContain('window.voidTerminal.input');
    expect(send.getText(source)).toContain('sessionId: tab.id');
  });

  it('preload exposes only narrow clipboard read/write IPC calls', () => {
    const source = sourceFile('../src/preload/index.ts');
    const apiStatement = source.statements.find((statement): statement is ts.VariableStatement => ts.isVariableStatement(statement)
      && statement.declarationList.declarations.some((declaration) => ts.isIdentifier(declaration.name) && declaration.name.text === 'api'));
    const apiDeclaration = apiStatement?.declarationList.declarations.find((declaration) => ts.isIdentifier(declaration.name) && declaration.name.text === 'api');
    const api = objectLiteral(apiDeclaration?.initializer);
    const clipboard = objectLiteral(objectProperty(api, 'clipboard'));
    const read = objectProperty(clipboard, 'read');
    const write = objectProperty(clipboard, 'write');

    expect(read, 'preload exposes clipboard.read').toBeDefined();
    expect(write, 'preload exposes clipboard.write').toBeDefined();
    if (!read || !write) return;
    expect(ts.isArrowFunction(read)).toBe(true);
    expect(ts.isArrowFunction(write)).toBe(true);
    if (!ts.isArrowFunction(read) || !ts.isArrowFunction(write)) return;

    expect(ts.isCallExpression(read.body), 'clipboard.read directly invokes IPC').toBe(true);
    const readInvocation = read.body as ts.CallExpression;
    expect(ts.isPropertyAccessExpression(readInvocation.expression) && readInvocation.expression.expression.getText(source)).toBe('ipcRenderer');
    expect(ts.isPropertyAccessExpression(readInvocation.expression) && readInvocation.expression.name.text).toBe('invoke');
    expect(readInvocation.arguments).toHaveLength(1);
    expect(readInvocation.arguments[0].getText(source)).toBe('IPC.clipboardRead');

    expect(write.parameters).toHaveLength(1);
    const text = write.parameters[0].name.getText(source);
    expect(ts.isCallExpression(write.body), 'clipboard.write directly invokes IPC').toBe(true);
    const writeInvocation = write.body as ts.CallExpression;
    expect(ts.isPropertyAccessExpression(writeInvocation.expression) && writeInvocation.expression.expression.getText(source)).toBe('ipcRenderer');
    expect(ts.isPropertyAccessExpression(writeInvocation.expression) && writeInvocation.expression.name.text).toBe('invoke');
    expect(writeInvocation.arguments).toHaveLength(2);
    expect(writeInvocation.arguments[0].getText(source)).toBe('IPC.clipboardWrite');
    expect(writeInvocation.arguments[1].getText(source).replaceAll(' ', '')).toBe(`{${text}}`);
  });

  it('keeps the clipboard bridge context-isolated and gives the renderer no browser-only copy fallback', () => {
    const main = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');
    const preload = readFileSync(new URL('../src/preload/index.ts', import.meta.url), 'utf8');
    const renderer = readFileSync(new URL('../src/renderer/clipboard-shortcuts.ts', import.meta.url), 'utf8');
    const createWindow = main.match(/async function createWindow\(\)[\s\S]*?\n\}/)?.[0] ?? '';
    const browserWindow = createWindow.match(/new BrowserWindow\(\{[\s\S]*?\}\)\)/)?.[0] ?? '';

    expect(browserWindow, 'the owned BrowserWindow construction is absent').not.toBe('');
    expect(browserWindow).toMatch(/contextIsolation\s*:\s*true/);
    expect(browserWindow).toMatch(/nodeIntegration\s*:\s*false/);
    expect(browserWindow).toMatch(/sandbox\s*:\s*true/);
    expect(preload).toMatch(/contextBridge\.exposeInMainWorld\(\s*['"]voidTerminal['"]\s*,\s*api\s*\)/);
    expect(`${preload}\n${renderer}`).not.toMatch(/navigator\.clipboard|document\.execCommand\s*\(|new\s+ClipboardEvent\s*\(/);
  });

  it('delegates both main-process clipboard channels exactly once with owned-renderer authority', () => {
    const source = sourceFile('../src/main/index.ts');
    const register = namedFunction(source, 'registerIpc');
    expect(register?.body, 'the real main-process IPC registration path exists').toBeDefined();

    const calls: ts.CallExpression[] = [];
    const visit = (node: ts.Node): void => {
      if (ts.isCallExpression(node) && ts.isIdentifier(node.expression)
        && node.expression.text === 'registerDesktopClipboardHandlers') calls.push(node);
      ts.forEachChild(node, visit);
    };
    visit(register!.body!);

    expect(calls, 'the executable clipboard registrar must be delegated to exactly once').toHaveLength(1);
    expect(calls[0].arguments).toHaveLength(1);
    const options = objectLiteral(calls[0].arguments[0]);
    expect(options, 'the clipboard registrar receives explicit production dependencies').toBeDefined();
    expect(objectProperty(options, 'ipcMain')?.getText(source)).toBe('ipcMain');
    expect(objectProperty(options, 'channels')?.getText(source)).toBe('IPC');
    expect(objectProperty(options, 'platform')?.getText(source)).toBe('process.platform');
    expect(objectProperty(options, 'authorize')?.getText(source)).toBe('assertRenderer');
    expect(objectProperty(options, 'dependencies')?.getText(source)).toBe('desktopClipboardDependencies');
  });

  it('names a distinct write channel instead of overloading terminal input or clipboard read', () => {
    const preloadContract = readFileSync(new URL('../src/shared/preload-contract.ts', import.meta.url), 'utf8');
    expect(preloadContract).toMatch(/clipboardWrite\s*:\s*['"]clipboard:write['"]/);
  });
});

describe('clipboard write IPC request validation', () => {
  it('returns the exact non-empty text value from the sole owned field', async () => {
    const module = await clipboardContract();
    expect(module.clipboardWriteRequest, 'strict clipboardWriteRequest validator is absent').toBeTypeOf('function');
    const text = '  selected\r\nΔ text\t ';
    expect(module.clipboardWriteRequest!({ text })).toBe(text);
  });

  it.each([
    null,
    [],
    {},
    { text: '' },
    { text: 7 },
    { text: 'selected', path: 'C:\\escape.png' },
    { text: 'selected', command: 'powershell.exe' },
  ])('rejects malformed, empty, non-text, and widened write requests: %j', async (raw) => {
    const module = await clipboardContract();
    expect(module.clipboardWriteRequest, 'strict clipboardWriteRequest validator is absent').toBeTypeOf('function');
    expect(() => module.clipboardWriteRequest!(raw)).toThrow();
  });
});
