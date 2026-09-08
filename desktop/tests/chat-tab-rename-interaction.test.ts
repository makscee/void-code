import { describe, expect, it } from 'vitest';

const ONE = '11111111-1111-4111-8111-111111111111';
const TWO = '22222222-2222-4222-8222-222222222222';
const TITLE = 'Quarterly close';
const MODULE = '../src/renderer/chat-tab-rename';

type Editing = { sessionId: string; originalTitle: string; draft: string; selectionStart: number; selectionEnd: number };
type State = { editing: Editing | null };
type Event =
  | { type: 'title-click'; sessionId: string; title: string; selectedId: string | null }
  | { type: 'input'; value: string }
  | { type: 'commit'; trigger: 'enter' | 'blur' }
  | { type: 'escape' };
type Effect = { type: 'select'; sessionId: string } | { type: 'rename'; sessionId: string; title: string };
type Result = { state: State; effects: Effect[] };
type Reducer = (state: State, event: Event) => Result;

async function reducer(): Promise<Reducer> {
  try {
    const module = await import(MODULE) as { reduceChatTabRename?: Reducer };
    if (typeof module.reduceChatTabRename === 'function') return module.reduceChatTabRename;
  } catch { /* report the desired interaction seam below */ }
  throw new Error('src/renderer/chat-tab-rename.ts must export reduceChatTabRename(state, event)');
}

const idle = (): State => ({ editing: null });
function editing(draft = TITLE): State {
  return { editing: { sessionId: ONE, originalTitle: TITLE, draft, selectionStart: 0, selectionEnd: TITLE.length } };
}

describe('inline tab-title rename interaction', () => {
  it('clicking the selected title enters edit mode with the whole current title selected', async () => {
    const reduce = await reducer();
    expect(reduce(idle(), { type: 'title-click', sessionId: ONE, title: TITLE, selectedId: ONE })).toEqual({
      state: { editing: { sessionId: ONE, originalTitle: TITLE, draft: TITLE, selectionStart: 0, selectionEnd: TITLE.length } },
      effects: [],
    });
  });

  it('an inactive title click selects only; a later selected-title click may edit', async () => {
    const reduce = await reducer();
    const first = reduce(idle(), { type: 'title-click', sessionId: ONE, title: TITLE, selectedId: TWO });
    expect(first).toEqual({ state: idle(), effects: [{ type: 'select', sessionId: ONE }] });
    expect(reduce(first.state, { type: 'title-click', sessionId: ONE, title: TITLE, selectedId: ONE }).state.editing?.draft).toBe(TITLE);
  });

  it.each(['', '   ', '\t\n'])('does not save blank draft %j', async (draft) => {
    const reduce = await reducer();
    expect(reduce(editing(draft), { type: 'commit', trigger: 'enter' }).effects).toEqual([]);
  });

  it('does not save 81 characters', async () => {
    const reduce = await reducer();
    expect(reduce(editing('x'.repeat(81)), { type: 'commit', trigger: 'enter' }).effects).toEqual([]);
  });

  it('Escape cancels without persisting', async () => {
    const reduce = await reducer();
    expect(reduce(editing('Replacement'), { type: 'escape' })).toEqual({ state: idle(), effects: [] });
  });

  it('an Enter followed by the resulting blur persists exactly once', async () => {
    const reduce = await reducer();
    const entered = reduce(editing('  Replacement  '), { type: 'commit', trigger: 'enter' });
    expect(entered.effects).toEqual([{ type: 'rename', sessionId: ONE, title: 'Replacement' }]);
    expect(reduce(entered.state, { type: 'commit', trigger: 'blur' }).effects).toEqual([]);
  });

  it('blur saves a trimmed valid draft', async () => {
    const reduce = await reducer();
    expect(reduce(editing('  Replacement  '), { type: 'commit', trigger: 'blur' }).effects).toEqual([
      { type: 'rename', sessionId: ONE, title: 'Replacement' },
    ]);
  });

  it('saves exactly 80 characters', async () => {
    const reduce = await reducer(); const title = 'x'.repeat(80);
    expect(reduce(editing(title), { type: 'commit', trigger: 'enter' }).effects).toEqual([{ type: 'rename', sessionId: ONE, title }]);
  });
});
