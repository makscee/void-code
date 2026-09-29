import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { newChatRequest } from '../src/shared/contract';
import { chatRuntimeLabel } from '../src/renderer/chat-runtime-label';

// Spec 2026-09-29-desktop-codex-chats-design, items 6 and 7: New Chat offers Pi
// or Codex, a Codex chat carries a "Codex" label in its tab and in Recent, and
// a Pi chat carries none (Pi is the default, and every chat before this change
// is Pi).
//
// index.ts cannot be imported here (no DOM environment, side effects on
// import — see renderer-login.test.ts), so the decisions live in importable
// modules tested for real, and the wiring is pinned as text.
//
// Contract pinned here:
//   newChatRequest(raw) in src/shared/contract.ts validates the
//     `workspace:new-chat` payload: { runtime?: 'pi' | 'codex' }, nothing else;
//   preload: workspace.newChat(runtime) invokes workspace:new-chat with { runtime };
//   main: the handler validates with newChatRequest and calls
//     workspace.newChat(randomUUID(), <request>.runtime);
//   chatRuntimeLabel(tab) in src/renderer/chat-runtime-label.ts: 'Codex' for a
//     Codex chat, null for a Pi chat (runtime absent or 'pi');
//   index.html: #new-chat-menu with one button per runtime, data-runtime="pi"
//     labelled Pi and data-runtime="codex" labelled Codex.

const html = readFileSync(new URL('../src/renderer/index.html', import.meta.url), 'utf8');
const renderer = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');
const preload = readFileSync(new URL('../src/preload/index.ts', import.meta.url), 'utf8');
const main = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');

describe('the workspace:new-chat payload', () => {
  it('carries the chosen runtime', () => {
    expect(newChatRequest({ runtime: 'codex' })).toEqual({ runtime: 'codex' });
    expect(newChatRequest({ runtime: 'pi' })).toEqual({ runtime: 'pi' });
  });

  it('may leave the runtime to the last choice', () => {
    expect(newChatRequest({})).toEqual({ runtime: undefined });
  });

  it.each([
    ['an unknown runtime', { runtime: 'claude' }],
    ['a runtime that is not a string', { runtime: 1 }],
    ['an extra field', { runtime: 'pi', id: '11111111-1111-4111-8111-111111111111' }],
    ['not an object', 'codex'],
    ['null', null],
  ])('refuses %s', (_name, raw) => {
    expect(() => newChatRequest(raw)).toThrow();
  });
});

describe('the label on a chat', () => {
  it('names Codex chats and leaves Pi chats unlabelled', () => {
    expect(chatRuntimeLabel({ runtime: 'codex' })).toBe('Codex');
    expect(chatRuntimeLabel({ runtime: 'pi' })).toBeNull();
    expect(chatRuntimeLabel({})).toBeNull();
  });
});

describe('wiring', () => {
  it('offers Pi and Codex from New Chat', () => {
    const menu = html.match(/<[a-z]+[^>]*\bid="new-chat-menu"[^>]*>([\s\S]*?)<\/(?:div|menu|ul|nav|section)>/);
    expect(menu, 'index.html has no #new-chat-menu').not.toBeNull();
    const body = menu![1];
    expect(body).toMatch(/<button[^>]*\bdata-runtime="pi"[^>]*>\s*Pi\s*<\/button>/);
    expect(body).toMatch(/<button[^>]*\bdata-runtime="codex"[^>]*>\s*Codex\s*<\/button>/);
  });

  it('asks for the chosen runtime and focuses the last one', () => {
    expect(renderer, 'the renderer never passes a runtime to New Chat').toMatch(/workspace\.newChat\(\s*[A-Za-z'"]/);
    expect(renderer, 'the renderer does not read lastRuntime to put focus on the last choice').toMatch(/lastRuntime/);
    expect(renderer).toMatch(/new-chat-menu/);
  });

  it('labels Codex chats in the tabs and in Recent', () => {
    expect(renderer).toMatch(/from '\.\/chat-runtime-label'/);
    expect((renderer.match(/chatRuntimeLabel\(/g) ?? []).length, 'chatRuntimeLabel is not used for both the tab strip and Recent').toBeGreaterThanOrEqual(2);
  });

  it('passes the runtime from preload to main and into the workspace', () => {
    expect(preload).toMatch(/newChat:\s*\(\s*runtime[^)]*\)\s*=>\s*ipcRenderer\.invoke\(\s*IPC\.workspaceNewChat\s*,\s*\{\s*runtime\s*\}\s*\)/);
    const handler = main.slice(main.indexOf('ipcMain.handle(IPC.workspaceNewChat'));
    const body = handler.slice(0, handler.indexOf('ipcMain.handle(', 1));
    expect(body).toMatch(/newChatRequest\(/);
    expect(body).toMatch(/workspace\.newChat\(\s*randomUUID\(\)\s*,\s*[\w.]*runtime\s*\)/);
  });

  it('launches a chat with the runtime and Codex session the workspace holds for it', () => {
    // The renderer's start request stays { sessionId, cwd, mode }: which runtime
    // a chat runs is the workspace's record, not something the page asserts.
    const handler = main.slice(main.indexOf('ipcMain.handle(IPC.start'));
    const body = handler.slice(0, handler.indexOf('ipcMain.handle(', 1));
    expect(body).toMatch(/workspace\.assertLaunch\(/);
    expect(body).toMatch(/runtime/);
    expect(body).toMatch(/codexSessionId/);
  });
});
