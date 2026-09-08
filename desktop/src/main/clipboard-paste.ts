import { chmodSync, lstatSync, mkdirSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { clipboardWriteRequest, type ClipboardReadResult } from '../shared/contract';

const CLIPBOARD_DIRECTORY_PREFIX = 'void-code-clipboard-';
const CLIPBOARD_RETENTION_MS = 7 * 24 * 60 * 60 * 1000;

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
  writeImage?(png: Buffer): string;
};

export type ClipboardImageStorageOptions = {
  temporaryDirectory(): string;
  uniqueId(): string;
  processId: number;
  now(): number;
};

export type ClipboardImageStorage = {
  directory: string;
  writeImage(png: Buffer): string;
  cleanup(): void;
};

const unavailableClipboardImageStorage: ClipboardImageStorage = {
  directory: '',
  writeImage: () => { throw new Error('clipboard image storage unavailable'); },
  cleanup: () => undefined,
};

// Image persistence is a Windows-only enhancement. It must never make the text clipboard path
// unavailable when the temporary filesystem is not usable during startup.
export function createSafeClipboardImageStorage(platform: string, create: () => ClipboardImageStorage): ClipboardImageStorage {
  if (platform !== 'win32') return unavailableClipboardImageStorage;
  try {
    return create();
  } catch {
    return unavailableClipboardImageStorage;
  }
}

function isOwnedClipboardDirectory(name: string): boolean {
  const matched = new RegExp(`^${CLIPBOARD_DIRECTORY_PREFIX}(\\d+)-`).exec(name);
  if (!matched) return false;
  const processId = Number(matched[1]);
  return Number.isSafeInteger(processId) && processId > 0;
}

function pruneAbandonedClipboardDirectories(root: string, options: ClipboardImageStorageOptions): void {
  let entries: string[];
  try {
    entries = readdirSync(root);
  } catch {
    return;
  }
  const oldestRetained = options.now() - CLIPBOARD_RETENTION_MS;
  for (const entry of entries) {
    if (!isOwnedClipboardDirectory(entry)) continue;
    const candidate = path.join(root, entry);
    try {
      // lstat makes a prefix-matching symlink inert instead of traversing it during retention.
      const status = lstatSync(candidate);
      if (!status.isDirectory() || status.isSymbolicLink() || status.mtimeMs >= oldestRetained) continue;
      rmSync(candidate, { recursive: true, force: true });
    } catch {
      // Retention is best effort. A concurrent process or inaccessible temporary entry is retained.
    }
  }
}

export function createClipboardImageStorage(options: ClipboardImageStorageOptions): ClipboardImageStorage {
  const root = path.resolve(options.temporaryDirectory());
  try { mkdirSync(root, { recursive: true, mode: 0o700 }); } catch { /* tmpdir already exists or is unavailable */ }
  pruneAbandonedClipboardDirectories(root, options);
  const directory = path.join(root, `${CLIPBOARD_DIRECTORY_PREFIX}${options.processId}-${options.uniqueId()}`);
  mkdirSync(directory, { mode: 0o700 });
  chmodSync(directory, 0o700);
  let sequence = 0;

  return {
    directory,
    writeImage: (png) => {
      // The sequence remains unique even when an injected ID seam intentionally returns a constant.
      const image = path.join(directory, `${CLIPBOARD_DIRECTORY_PREFIX}${options.uniqueId()}-${sequence++}.png`);
      writeFileSync(image, png, { mode: 0o600, flag: 'wx' });
      chmodSync(image, 0o600);
      return image;
    },
    cleanup: () => { rmSync(directory, { recursive: true, force: true }); },
  };
}

// This reader is only exposed by the Windows IPC handler. win32.join keeps the legacy deterministic
// seam suitable for Pi's Windows input even when it is exercised from another host platform.
function clipboardImagePath(temporaryDirectory: string, uniqueId: string): string {
  return path.win32.join(path.win32.resolve(temporaryDirectory), `void-code-clipboard-${uniqueId}.png`);
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
      const png = image.toPNG();
      if (dependencies.writeImage) return { kind: 'image-path', path: dependencies.writeImage(png) };
      const destination = clipboardImagePath(dependencies.filesystem.temporaryDirectory(), dependencies.uniqueId());
      dependencies.filesystem.writeFile(destination, png);
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

export type DesktopClipboardHandlerOptions<Event> = {
  ipcMain: { handle(channel: string, handler: (event: Event, raw?: unknown) => unknown): void };
  channels: { clipboardRead: string; clipboardWrite: string };
  platform: string;
  authorize(event: Event): void;
  dependencies: ClipboardReadDependencies & { clipboard: ClipboardReadDependencies['clipboard'] & { writeText(text: string): void } };
};

export function registerDesktopClipboardHandlers<Event>(options: DesktopClipboardHandlerOptions<Event>): void {
  options.ipcMain.handle(options.channels.clipboardRead, (event) => {
    // Authority is intentionally first: even a non-Windows no-op must not reveal handler reachability.
    options.authorize(event);
    return options.platform === 'win32' ? readDesktopClipboard(options.dependencies) : { kind: 'empty' };
  });
  options.ipcMain.handle(options.channels.clipboardWrite, (event, raw) => {
    options.authorize(event);
    options.dependencies.clipboard.writeText(clipboardWriteRequest(raw));
  });
}
