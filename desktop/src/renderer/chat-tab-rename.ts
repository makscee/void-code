export interface ChatTabRenameEditing {
  sessionId: string;
  originalTitle: string;
  draft: string;
  selectionStart: number;
  selectionEnd: number;
}

export interface ChatTabRenameState { editing: ChatTabRenameEditing | null }

export type ChatTabRenameEvent =
  | { type: 'title-click'; sessionId: string; title: string; selectedId: string | null }
  | { type: 'input'; value: string }
  | { type: 'commit'; trigger: 'enter' | 'blur' }
  | { type: 'escape' };

export type ChatTabRenameEffect = { type: 'select'; sessionId: string } | { type: 'rename'; sessionId: string; title: string };
export interface ChatTabRenameResult { state: ChatTabRenameState; effects: ChatTabRenameEffect[] }

function validTitle(value: string): string | undefined {
  const title = value.trim();
  return title.length >= 1 && title.length <= 80 ? title : undefined;
}

export function reduceChatTabRename(state: ChatTabRenameState, event: ChatTabRenameEvent): ChatTabRenameResult {
  if (event.type === 'title-click') {
    if (event.sessionId !== event.selectedId) return { state: { editing: null }, effects: [{ type: 'select', sessionId: event.sessionId }] };
    return {
      state: {
        editing: {
          sessionId: event.sessionId, originalTitle: event.title, draft: event.title,
          selectionStart: 0, selectionEnd: event.title.length,
        },
      },
      effects: [],
    };
  }
  if (event.type === 'input' && state.editing) return { state: { editing: { ...state.editing, draft: event.value } }, effects: [] };
  if (event.type === 'escape') return { state: { editing: null }, effects: [] };
  if (event.type === 'commit' && state.editing) {
    const title = validTitle(state.editing.draft);
    return { state: { editing: null }, effects: title ? [{ type: 'rename', sessionId: state.editing.sessionId, title }] : [] };
  }
  return { state, effects: [] };
}
