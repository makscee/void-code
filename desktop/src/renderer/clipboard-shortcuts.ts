import type { ClipboardReadResult } from '../shared/contract';

export type TerminalClipboardTarget = {
  attachCustomKeyEventHandler(handler: (event: KeyboardEvent) => boolean): void;
  paste(value: string): void;
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

function pasteTrustedClipboard(target: TerminalClipboardTarget, result: ClipboardReadResult): void {
  if (result.kind === 'text') target.paste(result.text);
  else if (result.kind === 'image-path') target.paste(result.path);
}

export function installWindowsClipboardShortcuts(
  target: TerminalClipboardTarget,
  platform: string,
  readTrustedClipboard: () => Promise<ClipboardReadResult>,
  terminalInput: OrderedTerminalInputSink,
): void {
  target.attachCustomKeyEventHandler((event) => {
    if (platform !== 'win32' || event.type !== 'keydown' || !isWindowsPasteShortcut(event)) return true;
    event.preventDefault();
    if (event.repeat) return false;

    const reservation = terminalInput.reserve();
    void (async () => {
      try {
        const result = await readTrustedClipboard();
        if (result.kind === 'empty') {
          reservation.discard();
          return;
        }
        reservation.emit(() => { pasteTrustedClipboard(target, result); });
      } catch {
        reservation.discard();
      }
    })();
    return false;
  });
}
