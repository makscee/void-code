import { mkdirSync, writeFileSync } from 'node:fs';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { findSessionFile, sessionLifecycleArgs } from '../src/main/session-files';

const temporary: string[] = [];
afterEach(async () => { for (const root of temporary.splice(0)) await rm(root, { recursive: true, force: true }); });
describe('Pi session UUID lookup', () => {
  it('finds the exact persisted JSONL through project directories', async () => {
    const root = await mkdtemp(path.join(os.tmpdir(), 'vc-session-')); temporary.push(root);
    const nested = path.join(root, 'project-hash'); mkdirSync(nested);
    const id = '123e4567-e89b-42d3-a456-426614174000';
    const file = path.join(nested, `2026-07-25T00-00-00_${id}.jsonl`); writeFileSync(file, '');
    expect(findSessionFile(root, id)).toBe(file);
    expect(sessionLifecycleArgs(root, id, 'resume')).toEqual(['--session', file]);
    expect(() => sessionLifecycleArgs(root, id, 'create')).toThrow('already exists');
    expect(findSessionFile(root, '123e4567-e89b-42d3-a456-426614174001')).toBeUndefined();
  });
  it('reports an absent session store as missing without silently creating a replacement', () => {
    const root = '/path/that/does/not/exist'; const id = '123e4567-e89b-42d3-a456-426614174000';
    expect(findSessionFile(root, id)).toBeUndefined();
    expect(() => sessionLifecycleArgs(root, id, 'resume')).toThrow('SESSION_MISSING');
    expect(sessionLifecycleArgs(root, id, 'create')).toEqual(['--session-id', id]);
  });
});
