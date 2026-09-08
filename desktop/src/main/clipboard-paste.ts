import { createHash } from 'node:crypto';
import { chmodSync, lstatSync, mkdirSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { clipboardWriteRequest, type ClipboardReadResult } from '../shared/contract';

const CLIPBOARD_DIRECTORY_PREFIX = 'void-code-clipboard-';
const CLIPBOARD_STORAGE_ROOT_PREFIX = 'void-code-clipboard-storage-';
const CLIPBOARD_DIRECTORY_NAME = /^void-code-clipboard-([1-9]\d*)-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$(?![\s\S])/;
const CLIPBOARD_RETENTION_MS = 7 * 24 * 60 * 60 * 1000;

// Electron userData scopes the single-instance lock, so its stable digest also scopes pruning.
// The namespace is a direct child of temp: Windows gives that new child the per-user temp DACL.
export function clipboardStorageRoot(temporaryDirectory: string, userData: string): string {
  const normalizedTemporaryDirectory = path.resolve(temporaryDirectory);
  const normalizedUserData = path.resolve(userData);
  const namespace = createHash('sha256').update(normalizedUserData).digest('hex');
  return path.join(normalizedTemporaryDirectory, `${CLIPBOARD_STORAGE_ROOT_PREFIX}${namespace}`);
}

export type ClipboardReadDependencies = {
  clipboard: {
    readImage(): { isEmpty(): boolean; toPNG(): Buffer };
    readText(): string;
  };
  writeImage(png: Buffer): string;
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
  const matched = CLIPBOARD_DIRECTORY_NAME.exec(name);
  return matched !== null && Number.isSafeInteger(Number(matched[1]));
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

export function readDesktopClipboard(dependencies: ClipboardReadDependencies): ClipboardReadResult {
  let image: ReturnType<ClipboardReadDependencies['clipboard']['readImage']>;
  try {
    image = dependencies.clipboard.readImage();
  } catch {
    return { kind: 'empty' };
  }

  try {
    if (!image.isEmpty()) {
      // IPC inputs are runtime data despite the required TypeScript contract. A preferred image
      // must have the process-owned persistence path or fail closed; it must never become text.
      if (typeof dependencies.writeImage !== 'function') return { kind: 'empty' };
      return { kind: 'image-path', path: dependencies.writeImage(image.toPNG()) };
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
