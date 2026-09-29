import { readFileSync, renameSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it, vi } from 'vitest';
import { codexSessionEvent, StatusChannelStore } from '../src/main/status-channel';

// Spec 2026-09-29-desktop-codex-chats-design, item 9: `vc codex-hook` writes
// session.json beside status.json when Codex starts a session; main watches it
// with the same watcher as status.json and stores the Codex session id on the
// chat, so the chat reopens the same conversation after the app restarts.
//
// Contract pinned here:
//   codexSessionEvent(value): { version: 1, chatId, runtime: 'codex', sessionId } | null
//     exact keys, UUID chatId and sessionId (Codex issues UUIDv7);
//   new StatusChannelStore(root, listener, isActive, onCodexSession?)
//     onCodexSession(chatId, sessionId) is called when a valid session.json for
//     that chat appears in its channel directory;
//   main/index.ts hands it to WorkspaceStore.bindCodexSession.

const CHAT = '123e4567-e89b-42d3-a456-426614174000';
const OTHER = '223e4567-e89b-42d3-a456-426614174000';
const CODEX_SESSION = '01a0ec63-35c1-7d02-bc6a-c06464a61565';
const LATER_SESSION = '01a0ec70-0000-7d02-bc6a-c06464a61565';
const session = (sessionId = CODEX_SESSION, chatId = CHAT) => ({ version: 1 as const, chatId, runtime: 'codex' as const, sessionId });

function atomicWrite(file: string, value: unknown): void {
  const temporary = `${file}.tmp-test`;
  writeFileSync(temporary, `${JSON.stringify(value)}\n`);
  renameSync(temporary, file);
}

describe('the session.json schema', () => {
  it('accepts exactly what vc codex-hook writes', () => {
    expect(codexSessionEvent(session())).toEqual(session());
  });

  it.each([
    ['another version', { ...session(), version: 2 }],
    ['another runtime', { ...session(), runtime: 'pi' }],
    ['a chat id that is not a UUID', { ...session(), chatId: 'chat-1' }],
    ['a session id that is not a UUID', { ...session(), sessionId: '../../etc/passwd' }],
    ['an extra key', { ...session(), transcript_path: '/tmp/x.jsonl' }],
    ['a missing key', { version: 1, chatId: CHAT, runtime: 'codex' }],
    ['an array', [session()]],
    ['null', null],
  ])('rejects %s', (_name, value) => {
    expect(codexSessionEvent(value)).toBeNull();
  });
});

describe('the channel reports the Codex session of its chat', () => {
  it('calls back with the session id once session.json appears', async () => {
    const onSession = vi.fn();
    const store = new StatusChannelStore(path.join(os.tmpdir(), `void-codex-session-${crypto.randomUUID()}`), () => undefined, () => false, onSession);
    try {
      const authority = store.create(3, CHAT);
      atomicWrite(path.join(path.dirname(authority.path), 'session.json'), session());
      await vi.waitFor(() => expect(onSession).toHaveBeenCalledWith(CHAT, CODEX_SESSION), { timeout: 2000, interval: 20 });
      // The status side is untouched by a session record.
      expect(store.status(3, CHAT)).toMatchObject({ state: 'running' });
      expect(store.status(3, CHAT).diagnostic ?? '').not.toMatch(/rejected|unreadable/);
    } finally { store.closeAll(); }
  });

  it('reports a later session when Codex starts another one in the same chat', async () => {
    const onSession = vi.fn();
    const store = new StatusChannelStore(path.join(os.tmpdir(), `void-codex-session-${crypto.randomUUID()}`), () => undefined, () => false, onSession);
    try {
      const authority = store.create(3, CHAT);
      const file = path.join(path.dirname(authority.path), 'session.json');
      atomicWrite(file, session());
      await vi.waitFor(() => expect(onSession).toHaveBeenCalledWith(CHAT, CODEX_SESSION), { timeout: 2000, interval: 20 });
      atomicWrite(file, session(LATER_SESSION));
      await vi.waitFor(() => expect(onSession).toHaveBeenLastCalledWith(CHAT, LATER_SESSION), { timeout: 2000, interval: 20 });
    } finally { store.closeAll(); }
  });

  it('ignores a record for another chat or a malformed one', async () => {
    const onSession = vi.fn();
    const delivered: unknown[] = [];
    const store = new StatusChannelStore(path.join(os.tmpdir(), `void-codex-session-${crypto.randomUUID()}`), (...args) => delivered.push(args), () => false, onSession);
    try {
      const authority = store.create(3, CHAT);
      const dir = path.dirname(authority.path);
      atomicWrite(path.join(dir, 'session.json'), session(CODEX_SESSION, OTHER));
      await new Promise((resolve) => setTimeout(resolve, 250));
      writeFileSync(path.join(dir, 'session.json'), '{ not json');
      await new Promise((resolve) => setTimeout(resolve, 250));
      expect(onSession).not.toHaveBeenCalled();
      // A bad session record is not a broken status channel.
      expect(delivered).toHaveLength(0);
    } finally { store.closeAll(); }
  });

  it('keeps working without a callback, as every existing caller constructs it', async () => {
    const store = new StatusChannelStore(path.join(os.tmpdir(), `void-codex-session-${crypto.randomUUID()}`), () => undefined, () => false);
    try {
      const authority = store.create(3, CHAT);
      atomicWrite(path.join(path.dirname(authority.path), 'session.json'), session());
      await new Promise((resolve) => setTimeout(resolve, 200));
      expect(store.status(3, CHAT)).toMatchObject({ state: 'running' });
    } finally { store.closeAll(); }
  });
});

describe('main stores what the channel reports', () => {
  const main = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');
  it('binds the reported session to the chat in the workspace', () => {
    // Honest limit: source text. index.ts cannot be imported without Electron;
    // the behaviour on both sides of this line is tested above and in
    // workspace-codex-chats.test.ts.
    expect(main).toMatch(/new StatusChannelStore\([\s\S]{0,600}?bindCodexSession\(/);
  });
});
