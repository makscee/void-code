import type { ClipboardReadResult } from '../shared/contract';

export type TerminalClipboardTarget = {
  attachCustomKeyEventHandler(handler: (event: KeyboardEvent) => boolean): void;
  paste(value: string): void;
};

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
): void {
  target.attachCustomKeyEventHandler((event) => {
    if (platform !== 'win32' || event.type !== 'keydown' || !isWindowsPasteShortcut(event)) return true;
    void readTrustedClipboard().then((result) => { pasteTrustedClipboard(target, result); }).catch(() => undefined);
    return false;
  });
}
