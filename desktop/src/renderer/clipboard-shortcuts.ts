import type { ClipboardReadResult } from '../shared/contract';

export type TerminalClipboardTarget = {
  attachCustomKeyEventHandler(handler: (event: KeyboardEvent) => boolean): void;
  getSelection(): string;
  paste(value: string): void;
};

type TerminalClipboardDataTarget = TerminalClipboardTarget & {
  onData(listener: (data: string) => void): unknown;
};

export type TerminalInputReservation = {
  emit(action: () => void): void;
  discard(): void;
};

export type OrderedTerminalInputSink = {
  send(data: string): void;
  reserve(): TerminalInputReservation;
};

type TerminalInputEntry = { ready: boolean; data: string[] };

// A clipboard read crosses Electron's process boundary, so it can finish after later xterm
// input. A reservation occupies the input order at keydown; emit() captures synchronous xterm
// onData from paste() in that reserved position before releasing the following input.
export function createOrderedTerminalInputSink(send: (data: string) => void): OrderedTerminalInputSink {
  const entries: TerminalInputEntry[] = [];
  let active: TerminalInputEntry | undefined;

  const flush = (): void => {
    while (entries[0]?.ready) {
      const entry = entries.shift()!;
      for (const data of entry.data) send(data);
    }
  };

  return {
    send: (data) => {
      if (active) active.data.push(data);
      else {
        entries.push({ ready: true, data: [data] });
        flush();
      }
    },
    reserve: () => {
      const entry: TerminalInputEntry = { ready: false, data: [] };
      entries.push(entry);
      return {
        emit: (action) => {
          if (entry.ready) return;
          active = entry;
          try {
            action();
          } finally {
            active = undefined;
            entry.ready = true;
            flush();
          }
        },
        discard: () => {
          if (entry.ready) return;
          entry.ready = true;
          flush();
        },
      };
    },
  };
}

function isWindowsPasteShortcut(event: KeyboardEvent): boolean {
  if (event.code !== 'KeyV' || event.metaKey) return false;
  return (event.ctrlKey && !event.altKey) || (event.altKey && !event.ctrlKey && !event.shiftKey);
}

function isWindowsCopyShortcut(event: KeyboardEvent): boolean {
  return event.code === 'KeyC' && event.ctrlKey && !event.altKey && !event.metaKey;
}

function pasteTrustedClipboard(target: TerminalClipboardTarget, result: ClipboardReadResult): void {
  if (result.kind === 'text') target.paste(result.text);
  else if (result.kind === 'image-path') target.paste(result.path);
}

export type ClipboardTransactionClock = () => number;

// Date.now is sufficiently monotonic for a five-second renderer transaction and remains controllable
// in tests; callers that need a stricter clock can provide one.
const defaultClipboardTransactionClock: ClipboardTransactionClock = () => Date.now();

export function installWindowsClipboardShortcuts(
  target: TerminalClipboardTarget,
  platform: string,
  readTrustedClipboard: () => Promise<ClipboardReadResult>,
  terminalInput: OrderedTerminalInputSink,
  writeTrustedClipboard?: (text: string) => Promise<void>,
  now: ClipboardTransactionClock = defaultClipboardTransactionClock,
): void {
  target.attachCustomKeyEventHandler((event) => {
    if (platform !== 'win32' || event.type !== 'keydown') return true;

    if (isWindowsCopyShortcut(event)) {
      const selection = target.getSelection();
      // Ctrl+C without a selection is the terminal's interrupt. Ctrl+Shift+C is always an
      // explicit copy gesture, including an empty one, so it must not become an interrupt.
      if (selection === '' && !event.shiftKey) return true;
      event.preventDefault();
      if (!event.repeat && selection !== '' && writeTrustedClipboard) {
        try {
          void writeTrustedClipboard(selection).catch(() => undefined);
        } catch {
          // A broken trusted bridge must not inject, paste, or revive the browser default.
        }
      }
      return false;
    }

    if (!isWindowsPasteShortcut(event)) return true;
    event.preventDefault();
    if (event.repeat) return false;

    const reservation = terminalInput.reserve();
    const deadline = now() + 5_000;
    let settled = false;
    const discard = (): void => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      reservation.discard();
    };
    const timeout = setTimeout(discard, 5_000);
    void (async () => {
      try {
        const result = await readTrustedClipboard();
        if (settled || now() >= deadline) {
          discard();
          return;
        }
        settled = true;
        clearTimeout(timeout);
        if (result.kind === 'empty') reservation.discard();
        else reservation.emit(() => { pasteTrustedClipboard(target, result); });
      } catch {
        discard();
      }
    })();
    return false;
  });
}

// Keep the async clipboard transaction and xterm's synchronous onData on one ordered sink. This
// is the sole composition used by the renderer and by the real-xterm fixture.
export function wireProductTerminalClipboard(
  terminal: TerminalClipboardDataTarget,
  platform: string,
  readTrustedClipboard: () => Promise<ClipboardReadResult>,
  writeTrustedClipboard: (text: string) => Promise<void>,
  sendTerminalInput: (data: string) => void,
): void {
  const terminalInput = createOrderedTerminalInputSink(sendTerminalInput);
  installWindowsClipboardShortcuts(terminal, platform, readTrustedClipboard, terminalInput, writeTrustedClipboard);
  terminal.onData((data) => { terminalInput.send(data); });
}
