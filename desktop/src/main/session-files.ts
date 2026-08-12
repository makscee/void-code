import { closeSync, fstatSync, lstatSync, openSync, readSync, readdirSync, realpathSync, statSync, type Stats } from 'node:fs';
import path from 'node:path';

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

export interface SessionScanLimits {
  maxDepth: number;
  maxDirectories: number;
  maxEntries: number;
  maxCandidates: number;
  maxHeaderBytes: number;
  maxHeaderLineBytes: number;
}

export const DEFAULT_SESSION_SCAN_LIMITS: Readonly<SessionScanLimits> = Object.freeze({
  maxDepth: 32,
  maxDirectories: 10_000,
  maxEntries: 50_000,
  maxCandidates: 10_000,
  maxHeaderBytes: 1024 * 1024,
  maxHeaderLineBytes: 64 * 1024,
});

export type SessionPathPlatform = 'darwin' | 'win32';
export interface SessionDiscoveryOptions {
  platform?: SessionPathPlatform;
  limits?: Partial<SessionScanLimits>;
  /** Test-only barrier used to exercise replacement between descriptor read and pathname handoff. */
  beforeCandidateRevalidation?: (file: string) => void;
}

export class SessionDiscoveryError extends Error {
  constructor(public readonly code: 'SESSION_MISSING' | 'SESSION_EXISTS' | 'SESSION_AMBIGUOUS' | 'SESSION_SCAN_LIMIT' | 'SESSION_STORE_UNAVAILABLE' | 'SESSION_INVALID_SOURCE') {
    super(`${code}: ${safeGuidance(code)}`);
    this.name = 'SessionDiscoveryError';
  }
}

function safeGuidance(code: SessionDiscoveryError['code']): string {
  if (code === 'SESSION_MISSING') return 'The saved session is unavailable. Start a new chat or close this one.';
  if (code === 'SESSION_EXISTS') return 'This chat already has a saved session.';
  if (code === 'SESSION_AMBIGUOUS') return 'More than one saved session matches this chat.';
  if (code === 'SESSION_SCAN_LIMIT') return 'Saved-session discovery reached its safety limit.';
  if (code === 'SESSION_INVALID_SOURCE') return 'The saved session source is invalid.';
  return 'The saved-session store could not be checked safely.';
}

function within(root: string, candidate: string): boolean {
  const relative = path.relative(root, candidate);
  return relative === '' || (!relative.startsWith(`..${path.sep}`) && relative !== '..' && !path.isAbsolute(relative));
}

function normalizeCwd(raw: string, platform: SessionPathPlatform): string | undefined {
  if (platform === 'win32') {
    if (/^(?:\\\\[?.]\\|\\[?.]\\)/.test(raw)) return undefined;
    const normalizedInput = raw.replaceAll('/', '\\');
    if (!/^(?:[a-zA-Z]:\\|\\\\[^\\]+\\[^\\]+)/.test(normalizedInput)) return undefined;
    return path.win32.normalize(normalizedInput).replace(/[\\]+$/, '').toLowerCase();
  }
  if (!path.posix.isAbsolute(raw) || /^[/]?(?:[a-zA-Z]:[\\/]|\\\\)/.test(raw)) return undefined;
  const lexical = path.posix.normalize(raw);
  try { return realpathSync(lexical); } catch { return lexical; }
}

function sameFile(first: Stats, second: Stats): boolean {
  return first.dev === second.dev && first.ino === second.ino && first.mode === second.mode && first.size === second.size && first.mtimeMs === second.mtimeMs && first.ctimeMs === second.ctimeMs;
}

function readHeader(file: string, canonicalRoot: string, limits: SessionScanLimits, beforeCandidateRevalidation?: (file: string) => void): Record<string, unknown> | undefined {
  let fd: number | undefined;
  try {
    const pathBefore = lstatSync(file);
    if (!pathBefore.isFile() || pathBefore.isSymbolicLink()) return undefined;
    const canonicalBefore = realpathSync(file);
    if (!within(canonicalRoot, canonicalBefore)) return undefined;
    fd = openSync(file, 'r');
    const descriptorBefore = fstatSync(fd);
    if (!descriptorBefore.isFile() || !sameFile(pathBefore, descriptorBefore)) return undefined;
    const capacity = Math.min(limits.maxHeaderBytes, descriptorBefore.size) + 1;
    const buffer = Buffer.alloc(capacity);
    let used = 0;
    while (used < capacity) {
      const count = readSync(fd, buffer, used, capacity - used, used);
      if (count === 0) break;
      used += count;
    }
    const decoder = new TextDecoder('utf-8', { fatal: true });
    const text = decoder.decode(buffer.subarray(0, used));
    let offset = 0;
    for (const physical of text.split('\n')) {
      const bytes = Buffer.byteLength(physical);
      offset += bytes + 1;
      if (bytes > limits.maxHeaderLineBytes || offset > limits.maxHeaderBytes) return undefined;
      const line = physical.endsWith('\r') ? physical.slice(0, -1) : physical;
      if (!line.trim() || line.trim() === '\uFEFF') continue;
      let parsed: unknown;
      try { parsed = JSON.parse(line.replace(/^\uFEFF/, '')); } catch { continue; }
      if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return undefined;
      const descriptorAfter = fstatSync(fd);
      beforeCandidateRevalidation?.(file);
      const pathAfter = lstatSync(file);
      const canonicalAfter = realpathSync(file);
      if (!sameFile(descriptorBefore, descriptorAfter) || !sameFile(descriptorAfter, pathAfter) || canonicalAfter !== canonicalBefore || !within(canonicalRoot, canonicalAfter)) return undefined;
      return parsed as Record<string, unknown>;
    }
    return undefined;
  } catch { return undefined; }
  finally { if (fd !== undefined) closeSync(fd); }
}

function validHeader(header: Record<string, unknown>, requestedId: string, requestedCwd: string, platform: SessionPathPlatform): boolean {
  if (header.type !== 'session' || !Number.isInteger(header.version) || ![1, 2, 3].includes(header.version as number)) return false;
  if (typeof header.id !== 'string' || !UUID.test(header.id) || header.id !== requestedId) return false;
  if (typeof header.cwd !== 'string' || typeof header.timestamp !== 'string' || header.timestamp.length === 0 || !Number.isFinite(Date.parse(header.timestamp))) return false;
  return normalizeCwd(header.cwd, platform) === requestedCwd;
}

export function findSessionFiles(root: string, sessionId: string, workspaceCwd: string, options: SessionDiscoveryOptions = {}): string[] {
  if (!UUID.test(sessionId)) throw new SessionDiscoveryError('SESSION_INVALID_SOURCE');
  const platform = options.platform ?? (process.platform === 'win32' ? 'win32' : 'darwin');
  const limits = { ...DEFAULT_SESSION_SCAN_LIMITS, ...options.limits };
  const requestedCwd = normalizeCwd(workspaceCwd, platform);
  if (!requestedCwd) throw new SessionDiscoveryError('SESSION_INVALID_SOURCE');
  let canonicalRoot: string;
  const lexicalRoot = path.resolve(root);
  try { canonicalRoot = realpathSync(lexicalRoot); if (!statSync(canonicalRoot).isDirectory()) throw new Error(); }
  catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') return [];
    throw new SessionDiscoveryError('SESSION_STORE_UNAVAILABLE');
  }
  const queue: Array<{ directory: string; depth: number }> = [{ directory: lexicalRoot, depth: 0 }];
  const visited = new Set<string>();
  const matches: string[] = [];
  let entriesSeen = 0; let candidatesSeen = 0;
  while (queue.length > 0) {
    const next = queue.shift()!;
    let directory: string;
    try { directory = realpathSync(next.directory); } catch { continue; }
    if (!within(canonicalRoot, directory) || visited.has(directory)) continue;
    if (next.depth > limits.maxDepth || visited.size >= limits.maxDirectories) throw new SessionDiscoveryError('SESSION_SCAN_LIMIT');
    visited.add(directory);
    let entries;
    try { entries = readdirSync(next.directory, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name, 'en')); }
    catch { throw new SessionDiscoveryError('SESSION_STORE_UNAVAILABLE'); }
    entriesSeen += entries.length;
    if (entriesSeen > limits.maxEntries) throw new SessionDiscoveryError('SESSION_SCAN_LIMIT');
    // Visit real directories before aliases so an alias never changes the returned stable path.
    for (const entry of entries.filter((item) => item.isDirectory())) queue.push({ directory: path.join(next.directory, entry.name), depth: next.depth + 1 });
    for (const entry of entries.filter((item) => !item.isDirectory())) {
      const candidate = path.join(next.directory, entry.name);
      let stat;
      try { stat = lstatSync(candidate); } catch { throw new SessionDiscoveryError('SESSION_STORE_UNAVAILABLE'); }
      if (stat.isSymbolicLink()) {
        let target: string;
        try { target = realpathSync(candidate); } catch { continue; }
        if (!within(canonicalRoot, target)) continue;
        try { if (statSync(target).isDirectory()) queue.push({ directory: candidate, depth: next.depth + 1 }); } catch { /* raced/broken link: ignore */ }
      } else if (stat.isFile() && entry.name.endsWith('.jsonl')) {
        candidatesSeen++;
        if (candidatesSeen > limits.maxCandidates) throw new SessionDiscoveryError('SESSION_SCAN_LIMIT');
        const header = readHeader(candidate, canonicalRoot, limits, options.beforeCandidateRevalidation);
        if (header && validHeader(header, sessionId, requestedCwd, platform)) matches.push(candidate);
      }
    }
  }
  return matches.sort();
}

export function findSessionFile(root: string, sessionId: string, workspaceCwd: string, options?: SessionDiscoveryOptions): string | undefined {
  const matches = findSessionFiles(root, sessionId, workspaceCwd, options);
  if (matches.length > 1) throw new SessionDiscoveryError('SESSION_AMBIGUOUS');
  return matches[0];
}

export function sessionLifecycleArgs(root: string, sessionId: string, mode: 'create' | 'resume', workspaceCwd: string, options?: SessionDiscoveryOptions): string[] {
  const matches = findSessionFiles(root, sessionId, workspaceCwd, options);
  if (matches.length > 1) throw new SessionDiscoveryError('SESSION_AMBIGUOUS');
  const persisted = matches[0];
  if (mode === 'resume' && !persisted) throw new SessionDiscoveryError('SESSION_MISSING');
  if (mode === 'create' && persisted) throw new SessionDiscoveryError('SESSION_EXISTS');
  return mode === 'create' ? ['--session-id', sessionId] : ['--session', persisted];
}
