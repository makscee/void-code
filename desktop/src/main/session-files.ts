import { existsSync, readdirSync } from 'node:fs';
import path from 'node:path';

export function findSessionFile(root: string, sessionId: string): string | undefined {
  if (!existsSync(root)) return undefined;
  const suffix = `_${sessionId}.jsonl`;
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    const candidate = path.join(root, entry.name);
    if (entry.isDirectory()) { const found = findSessionFile(candidate, sessionId); if (found) return found; }
    else if (entry.isFile() && entry.name.endsWith(suffix)) return candidate;
  }
  return undefined;
}

export function sessionLifecycleArgs(root: string, sessionId: string, mode: 'create' | 'resume'): string[] {
  const persisted = findSessionFile(root, sessionId);
  if (mode === 'resume' && !persisted) throw new Error('SESSION_MISSING: The saved Pi session is unavailable. Start a new chat or close this one.');
  if (mode === 'create' && persisted) throw new Error('session UUID already exists');
  return mode === 'create' ? ['--session-id', sessionId] : ['--session', persisted!];
}
