import { mkdirSync, readFileSync, statSync, symlinkSync, writeFileSync } from 'node:fs';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { findSessionFile, sessionLifecycleArgs, SessionDiscoveryError } from '../src/main/session-files';

const temporary: string[] = [];
const id = '123e4567-e89b-42d3-a456-426614174000';
const header = (cwd: string, overrides: Record<string, unknown> = {}) => JSON.stringify({ type: 'session', version: 3, id, timestamp: '2026-07-25T00:00:00.000Z', cwd, ...overrides });
afterEach(async () => { for (const root of temporary.splice(0)) await rm(root, { recursive: true, force: true }); });
async function fixture() { const root = await mkdtemp(path.join(os.tmpdir(), 'vc-session-')); temporary.push(root); const work = path.join(root, 'work'); const store = path.join(root, 'sessions'); mkdirSync(work); mkdirSync(store); return { root, work, store }; }

// A1/A2/A5/A7/A8: header identity, workspace binding, ambiguity, malformed input and stable lifecycle errors.
describe('Pi session resolver', () => {
  it('binds the authoritative v1-v3 header UUID and canonical workspace cwd, independent of filename', async () => {
    const { work, store } = await fixture();
    for (const version of [1, 2, 3]) {
      const file = path.join(store, `unexpected-${version}.jsonl`);
      writeFileSync(file, `${header(work, { version })}\n`);
      expect(findSessionFile(store, id, work)).toBe(file);
      rmSyncFile(file);
    }
  });
  it('selects the requested cwd and rejects deterministic same-key ambiguity', async () => {
    const { root, work, store } = await fixture(); const other = path.join(root, 'other'); mkdirSync(other);
    writeFileSync(path.join(store, 'other.jsonl'), header(other));
    const selected = path.join(store, 'selected.jsonl'); writeFileSync(selected, header(work));
    expect(findSessionFile(store, id, work)).toBe(selected);
    writeFileSync(path.join(store, 'duplicate.jsonl'), header(work));
    expect(() => findSessionFile(store, id, work)).toThrowError(SessionDiscoveryError);
    expect(() => findSessionFile(store, id, work)).toThrow('SESSION_AMBIGUOUS');
  });
  it.each([
    ['', 'empty'], ['{', 'torn JSON'], [JSON.stringify({ type: 'message' }), 'non-header'],
    [header('/relative', { cwd: 'relative' }), 'relative cwd'], [header('/missing', { id: '223e4567-e89b-42d3-a456-426614174000' }), 'other id'],
    [header('/missing', { version: 4 }), 'future version'], [header('/missing', { timestamp: 7 }), 'invalid timestamp'],
  ])('ignores %s candidate safely (%s)', async (contents) => {
    const { work, store } = await fixture(); writeFileSync(path.join(store, `named_${id}.jsonl`), contents);
    expect(findSessionFile(store, id, work)).toBeUndefined();
  });
  it('skips bounded blank/malformed physical lines and accepts a final header without newline', async () => {
    const { work, store } = await fixture(); const file = path.join(store, 'legacy.jsonl');
    writeFileSync(file, `\nnot-json\n${header(work)}`);
    expect(findSessionFile(store, id, work)).toBe(file);
  });
  it('preserves create/resume semantics with privacy-safe codes', async () => {
    const { work, store } = await fixture(); const file = path.join(store, 'saved.jsonl'); writeFileSync(file, header(work));
    expect(sessionLifecycleArgs(store, id, 'resume', work)).toEqual(['--session', file]);
    expect(() => sessionLifecycleArgs(store, id, 'create', work)).toThrow('SESSION_EXISTS');
    rmSyncFile(file);
    expect(sessionLifecycleArgs(store, id, 'create', work)).toEqual(['--session-id', id]);
    expect(() => sessionLifecycleArgs(store, id, 'resume', work)).toThrow('SESSION_MISSING');
  });
  it('uses injected Windows path semantics without host POSIX resolution', async () => {
    const { store } = await fixture(); const file = path.join(store, 'windows.jsonl');
    writeFileSync(file, header('C:\\Users\\Alice\\repo\\.'));
    expect(findSessionFile(store, id, 'c:/users/ALICE/repo', { platform: 'win32' })).toBe(file);
    writeFileSync(file, header('\\\\server\\share\\repo'));
    expect(findSessionFile(store, id, '\\\\SERVER\\SHARE\\repo\\', { platform: 'win32' })).toBe(file);
    writeFileSync(file, header('\\\\?\\C:\\repo'));
    expect(findSessionFile(store, id, 'C:\\repo', { platform: 'win32' })).toBeUndefined();
  });
  it('does not mutate a compatible fixture while discovering it', async () => {
    const { work, store } = await fixture(); const file = path.join(store, 'v1.jsonl'); const bytes = `${header(work, { version: 1 })}\n{"type":"message","role":"user"}\n`; writeFileSync(file, bytes);
    const before = statSync(file); expect(findSessionFile(store, id, work)).toBe(file);
    expect(readFileSync(file, 'utf8')).toBe(bytes); expect(statSync(file).mtimeMs).toBe(before.mtimeMs);
  });
  it('canonicalizes workspace and header symlink spellings', async () => {
    const { root, work, store } = await fixture(); const alias = path.join(root, 'alias'); symlinkSync(work, alias, 'dir'); const file = path.join(store, 'alias.jsonl'); writeFileSync(file, header(alias));
    expect(findSessionFile(store, id, work)).toBe(file);
  });
});
function rmSyncFile(file: string) { rmSync(file); }
import { rmSync } from 'node:fs';
