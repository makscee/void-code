import type { ChatRuntime } from '../shared/contract';

/**
 * The label a chat carries in its tab and in Recent: "Codex" for a Codex chat, none for Pi. Pi is
 * the default, and every chat made before Codex chats existed is Pi.
 */
export function chatRuntimeLabel(tab: { runtime?: ChatRuntime }): string | null {
  return tab.runtime === 'codex' ? 'Codex' : null;
}
