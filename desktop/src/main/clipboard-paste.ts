import path from 'node:path';
import type { ClipboardReadResult } from '../shared/contract';

export type ClipboardReadDependencies = {
  clipboard: {
    readImage(): { isEmpty(): boolean; toPNG(): Buffer };
    readText(): string;
  };
  filesystem: {
    temporaryDirectory(): string;
    writeFile(path: string, png: Buffer): void;
  };
  uniqueId(): string;
};

// This reader is only exposed by the Windows IPC handler. win32.join keeps the path suitable for
// Pi's Windows input even when its deterministic seam is exercised from another host platform.
function clipboardImagePath(temporaryDirectory: string, uniqueId: string): string {
  return path.win32.join(temporaryDirectory, `void-code-clipboard-${uniqueId}.png`);
}

export function readDesktopClipboard(dependencies: ClipboardReadDependencies): ClipboardReadResult {
  let image: ReturnType<ClipboardReadDependencies['clipboard']['readImage']>;
  try {
    image = dependencies.clipboard.readImage();
  } catch {
    return { kind: 'empty' };
  }

  try {
    if (!image.isEmpty()) {
      const destination = clipboardImagePath(dependencies.filesystem.temporaryDirectory(), dependencies.uniqueId());
      dependencies.filesystem.writeFile(destination, image.toPNG());
      return { kind: 'image-path', path: destination };
    }
  } catch {
    return { kind: 'empty' };
  }

  try {
    const text = dependencies.clipboard.readText();
    return text === '' ? { kind: 'empty' } : { kind: 'text', text };
  } catch {
    return { kind: 'empty' };
  }
}

export function createTrustedClipboardReadHandler<Event>(
  authorize: (event: Event) => void,
  dependencies: ClipboardReadDependencies,
): (event: Event) => Promise<ClipboardReadResult> {
  return async (event) => {
    authorize(event);
    return readDesktopClipboard(dependencies);
  };
}
