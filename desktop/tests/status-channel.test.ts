import { existsSync, writeFileSync, renameSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { lifecycleEvent, StatusChannelStore } from '../src/main/status-channel';

const CHAT = '123e4567-e89b-42d3-a456-426614174000';
const OTHER = '223e4567-e89b-42d3-a456-426614174000';
const message = (generation: number, sequence: number, state: 'Working' | 'Ready' = 'Working') => ({
  version: 1 as const, chatId: CHAT, generation, sequence, state, timestamp: '2026-07-25T00:00:00.000Z',
});

describe('private UUID-keyed desktop lifecycle channel', () => {
  it('accepts only the exact value-free v1 schema', () => {
    const valid = message(1, 1);
    expect(lifecycleEvent(valid)).toEqual(valid);
    for (const invalid of [
      { ...valid, version: 2 }, { ...valid, chatId: 'not-a-uuid' }, { ...valid, state: 'Prompting' },
      { ...valid, sequence: 0 }, { ...valid, timestamp: 'not-time' }, { ...valid, prompt: 'forbidden' },
      { ...valid, filename: 'forbidden' }, { ...valid, model: 'forbidden' }, { ...valid, credential: 'forbidden' },
      { ...valid, payload: {} },
    ]) expect(lifecycleEvent(invalid)).toBeNull();
    expect(Object.keys(valid).sort()).toEqual(['chatId', 'generation', 'sequence', 'state', 'timestamp', 'version']);
  });

  it('validates UUID, generation and sequence and rejects replay, stale and post-close writes', () => {
    const delivered: unknown[] = [];
    const store = new StatusChannelStore(path.join(os.tmpdir(), `void-status-${crypto.randomUUID()}`), (...args) => delivered.push(args), () => false);
    const authority = store.create(7, CHAT);
    expect(store.ingest(CHAT, message(authority.generation, 1))).toBe(true);
    expect(store.ingest(CHAT, message(authority.generation, 1, 'Ready'))).toBe(false);
    expect(store.ingest(CHAT, message(authority.generation - 1, 2))).toBe(false);
    expect(store.ingest(CHAT, { ...message(authority.generation, 2), chatId: OTHER })).toBe(false);
    expect(store.status(7, CHAT)).toMatchObject({ state: 'working', unread: false });
    expect(store.ingest(CHAT, message(authority.generation, 2, 'Ready'))).toBe(true);
    expect(store.status(7, CHAT)).toMatchObject({ state: 'ready', unread: true });
    expect(() => store.status(8, CHAT)).toThrow('unknown status channel');
    store.close(7, CHAT);
    expect(existsSync(path.dirname(authority.path))).toBe(false);
    expect(store.ingest(CHAT, message(authority.generation, 3))).toBe(false);
    expect(delivered).toHaveLength(2);
    store.closeAll();
  });

  it('reads atomic writer fixtures and clearing unread does not emit or alter lifecycle ordering', async () => {
    const delivered: unknown[] = [];
    const root = path.join(os.tmpdir(), `void-status-${crypto.randomUUID()}`);
    const store = new StatusChannelStore(root, (...args) => delivered.push(args), () => false);
    const authority = store.create(4, CHAT);
    const temporary = `${authority.path}.tmp-fixture`;
    writeFileSync(temporary, JSON.stringify(message(authority.generation, 1, 'Ready')));
    renameSync(temporary, authority.path);
    const deadline = Date.now() + 1000;
    while (store.status(4, CHAT).state !== 'ready' && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 10));
    expect(store.status(4, CHAT)).toMatchObject({ state: 'ready', unread: true });
    expect(store.clearUnread(4, CHAT)).toMatchObject({ state: 'ready', unread: false });
    expect(delivered).toHaveLength(1);
    store.closeAll();
    expect(existsSync(root)).toBe(false);
  });
});
