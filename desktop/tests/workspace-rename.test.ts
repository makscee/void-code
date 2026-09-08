import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { WorkspaceStore } from '../src/main/workspace-store';

const CHAT = '11111111-1111-4111-8111-111111111111';
const UNKNOWN = '22222222-2222-4222-8222-222222222222';
const roots: string[] = [];

type RenameStore = WorkspaceStore & { rename?: (id: string, title: string) => ReturnType<WorkspaceStore['view']> };

function rename(store: WorkspaceStore, id: string, title: string) {
  const operation = (store as RenameStore).rename;
  if (typeof operation !== 'function') throw new Error('WorkspaceStore.rename(id, title) is missing');
  return operation.call(store, id, title);
}

function fixture() {
  const root = mkdtempSync(path.join(os.tmpdir(), 'void-rename-')); roots.push(root);
  const folder = path.join(root, 'workspace'); mkdirSync(folder);
  const metadata = path.join(root, 'metadata', 'workspace.json');
  const session = path.join(root, `chat_${CHAT}.jsonl`);
  writeFileSync(session, '{"type":"message","text":"session truth"}\n');
  const store = new WorkspaceStore(metadata); store.setFolder(folder); store.newChat(CHAT);
  return { root, folder, metadata, session, store };
}

afterEach(() => {
  for (const root of roots.splice(0)) { chmodSync(root, 0o700); rmSync(root, { recursive: true, force: true }); }
});

describe('durable chat titles', () => {
  it('trims a rename, persists it across restart, and carries it through Recent Chats and resume', () => {
    const { metadata, store } = fixture();

    expect(rename(store, CHAT, '  Quarterly close  ').workspace?.tabs[0].title).toBe('Quarterly close');
    store.close(CHAT);

    const relaunched = new WorkspaceStore(metadata);
    expect(relaunched.view().workspace?.tabs).toEqual([{ id: CHAT, title: 'Quarterly close', location: 'recent' }]);
    expect(relaunched.resume(CHAT).workspace?.tabs).toEqual([{ id: CHAT, title: 'Quarterly close', location: 'active' }]);
  });

  it('does not leak a rename whose metadata save failed into a later successful save', () => {
    const { metadata, session, store } = fixture();
    const originalMetadata = readFileSync(metadata, 'utf8');
    const originalSession = readFileSync(session);
    mkdirSync(`${metadata}.tmp`);

    expect(() => rename(store, CHAT, 'Leaked title')).toThrow();
    const titleAfterFailure = store.view().workspace?.tabs[0].title;
    expect(readFileSync(metadata, 'utf8')).toBe(originalMetadata);
    expect(readFileSync(session)).toEqual(originalSession);

    rmSync(`${metadata}.tmp`, { recursive: true });
    store.close(CHAT);
    const titleAfterReload = new WorkspaceStore(metadata).view().workspace?.tabs[0].title;

    expect({ titleAfterFailure, titleAfterReload }).toEqual({
      titleAfterFailure: 'Chat 1',
      titleAfterReload: 'Chat 1',
    });
    expect(readFileSync(session)).toEqual(originalSession);
  });

  it('rejects invalid changes atomically and never writes the Pi JSONL session', () => {
    const { metadata, session, store } = fixture();
    const originalMetadata = readFileSync(metadata, 'utf8');
    const originalSession = readFileSync(session);
    chmodSync(session, 0o400);

    for (const [id, title] of [[CHAT, ''], [CHAT, '   \t\n'], [CHAT, 'x'.repeat(81)], [UNKNOWN, 'Valid title']]) {
      expect(() => rename(store, id, title)).toThrow();
      expect(store.view().workspace?.tabs[0].title).toBe('Chat 1');
      expect(readFileSync(metadata, 'utf8')).toBe(originalMetadata);
      expect(readFileSync(session)).toEqual(originalSession);
    }
  });
});
