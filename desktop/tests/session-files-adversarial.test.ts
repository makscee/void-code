import { mkdirSync, symlinkSync, writeFileSync } from 'node:fs';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { findSessionFile } from '../src/main/session-files';
const roots: string[] = []; const id = '123e4567-e89b-42d3-a456-426614174000';
afterEach(async () => { for (const root of roots.splice(0)) await rm(root, { recursive: true, force: true }); });
async function base() { const root = await mkdtemp(path.join(os.tmpdir(), 'vc-adversarial-')); roots.push(root); const store = path.join(root, 'sessions'); const work = path.join(root, 'work'); mkdirSync(store); mkdirSync(work); return { root, store, work }; }
const body = (cwd: string) => JSON.stringify({ type: 'session', version: 3, id, timestamp: '2026-01-01T00:00:00Z', cwd });
// A3/A4/A5/A9: contained directory links are followed once, escapes/files are not, and traversal is finite.
describe('adversarial Pi session discovery', () => {
  it('follows contained directory symlinks cycle-deduped but ignores outside-root escapes and file links', async () => {
    const { root, store, work } = await base(); const target = path.join(store, 'target'); mkdirSync(target); const valid = path.join(target, 'valid.jsonl'); writeFileSync(valid, body(work));
    symlinkSync(target, path.join(store, 'alias'), 'dir'); symlinkSync(store, path.join(target, 'cycle'), 'dir'); symlinkSync(valid, path.join(store, 'file-link.jsonl'));
    const outside = path.join(root, 'outside'); mkdirSync(outside); writeFileSync(path.join(outside, 'outside.jsonl'), body(work)); symlinkSync(outside, path.join(store, 'escape'), 'dir');
    expect(findSessionFile(store, id, work)).toBe(valid);
  });
  it('fails closed at depth, entry and candidate limits', async () => {
    const { store, work } = await base(); let nested = store;
    for (let index = 0; index < 5; index++) { nested = path.join(nested, `d${index}`); mkdirSync(nested); }
    expect(() => findSessionFile(store, id, work, { limits: { maxDepth: 2 } })).toThrow('SESSION_SCAN_LIMIT');
    expect(() => findSessionFile(store, id, work, { limits: { maxEntries: 2 } })).toThrow('SESSION_SCAN_LIMIT');
    const flat = path.join(store, 'flat'); mkdirSync(flat); for (let index = 0; index < 3; index++) writeFileSync(path.join(flat, `${index}.jsonl`), '{}');
    expect(() => findSessionFile(flat, id, work, { limits: { maxCandidates: 2 } })).toThrow('SESSION_SCAN_LIMIT');
  });
  it('rejects oversized physical headers while continuing to a valid candidate', async () => {
    const { store, work } = await base(); writeFileSync(path.join(store, 'oversize.jsonl'), `${'x'.repeat(301)}\n${body(work)}`); const valid = path.join(store, 'valid.jsonl'); writeFileSync(valid, body(work));
    expect(findSessionFile(store, id, work, { limits: { maxHeaderBytes: 1024, maxHeaderLineBytes: 300 } })).toBe(valid);
  });
});
