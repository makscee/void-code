import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { sessionLifecycleArgs } from '../src/main/session-files';

const roots: string[] = [];
afterEach(async () => { for (const root of roots.splice(0)) await rm(root, { recursive: true, force: true }); });

// Captured from real @earendil-works/pi-coding-agent 0.82.1 output, with only
// timestamps and the disposable cwd normalized. The pinned 0.84.1 desktop
// resolver must pass its bytes through unchanged for Pi itself to resume.
describe('real Pi 0.82.1 fixture to pinned 0.84.1 compatibility', () => {
  it('resolves the durable fixture without rewriting it', async () => {
    const root = await mkdtemp(path.join(os.tmpdir(), 'vc-pi-082-fixture-')); roots.push(root);
    const store = path.join(root, 'sessions'); const work = path.join(root, 'work'); mkdirSync(store); mkdirSync(work);
    const captured = readFileSync(path.join(__dirname, 'fixtures-pi-082.jsonl'), 'utf8').replace('WORKSPACE_CWD', work);
    const file = path.join(store, 'captured-by-pi-0.82.1.jsonl'); writeFileSync(file, captured);
    expect(sessionLifecycleArgs(store, '523e4567-e89b-42d3-a456-426614174000', 'resume', work)).toEqual(['--session', file]);
    expect(readFileSync(file, 'utf8')).toBe(captured);
  });
});
