import { existsSync } from 'node:fs';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { agentDir, interactiveFile, realPi } from './fixtures/pi-fullscreen-clipboard';
import { editorRig, keys, keysModule, type EditorRig, type EditorView } from './fixtures/pi-editor-keys';
import { consumerHooks } from './fixtures/pi-interactive-consumer';

// Esc clears the prompt, Up/Down recall prompt history (void-works#77), in the managed Pi extension
// (the TypeScript embedded in cmd/vc/pi_extension.go), with Pi 0.87.1's public API only.
//
// Everything here is the pinned Pi: its CustomEditor, its KeybindingsManager and app keybindings,
// the Esc handlers of its InteractiveMode.setupKeyHandlers, and its own
// InteractiveMode.setCustomEditorComponent, which is what ctx.ui.setEditorComponent runs. Keys go in
// through the real TUI input path. What this cannot prove: a person's terminal delivering the keys;
// the PR's How to try drives the real CLI in a PTY for that.

type Factory = (tui: unknown, theme: unknown, keybindings: unknown) => EditorView;
interface Keybindings {
  getUserBindings(): Record<string, unknown>;
  setUserBindings(bindings: Record<string, unknown>): void;
}

const rigs: EditorRig[] = [];
afterEach(() => { while (rigs.length) rigs.pop()!.close(); });

// The editor rig with the extension started on it: session_start's setEditorComponent runs Pi's
// real setCustomEditorComponent on the same receiver whose Esc handlers Pi set up.
async function started() {
  const r = await editorRig(); rigs.push(r);
  const tuiModule = await realPi() as unknown as { setKeybindings(kb: unknown): void };
  const kb = (r.editor as unknown as { keybindings: Keybindings }).keybindings;
  tuiModule.setKeybindings(kb); // As InteractiveMode's constructor does: one manager for the editor and its base.
  const hooks = consumerHooks(interactiveFile, undefined, ['setCustomEditorComponent']);
  const setCustomEditorComponent = new Function('getEditorTheme', `return class { ${hooks.methods.get('setCustomEditorComponent')} }`)(
    () => r.editor['theme' as keyof EditorView]).prototype.setCustomEditorComponent;
  const receiver = r.receiver as Record<string, unknown>;
  Object.assign(receiver, {
    keybindings: kb,
    editorContainer: { clear: vi.fn(), addChild: vi.fn() },
    disposeActiveSelector: vi.fn(),
  });
  let factory: Factory | undefined;
  const ctx = { ...r.ctx, ui: { ...r.ctx.ui, setEditorComponent: (f: Factory | undefined) => { factory = f; setCustomEditorComponent.call(receiver, f); } } };
  const module = await keysModule();
  const handlers = new Map<string, Array<(event: { reason: string }, c: typeof ctx) => unknown>>();
  await module.default({ on(name: string, handler: never) { handlers.set(name, [...(handlers.get(name) ?? []), handler]); }, registerProvider: vi.fn() } as never);
  for (const handler of handlers.get('session_start') ?? []) await handler({ reason: 'startup' }, ctx);
  expect(factory, 'the extension installs its editor through ctx.ui.setEditorComponent').toBeTypeOf('function');
  const editor = receiver.editor as EditorView;
  expect(editor).not.toBe(r.editor);
  expect(r.reference.focusedComponent).toBe(editor);
  return { ...r, editor, kb, restoreQueued: r.receiver.restoreQueuedMessagesToEditor as ReturnType<typeof vi.fn> };
}

describe.skipIf(!existsSync(agentDir))('Esc and Up/Down in the vc prompt (Pi 0.87.1)', () => {
  it('Esc clears an idle draft, multi-line included, and Pi undo brings it back', async () => {
    const r = await started();
    r.draft('first line\nsecond line');
    expect(r.editor.getText()).toBe('first line\nsecond line');
    r.input(keys.esc);
    expect(r.editor.getText()).toBe('');
    r.input(keys.undo);
    expect(r.editor.getText()).toBe('first line\nsecond line');
  });

  it('Esc during a turn runs Pi\'s abort and keeps the draft', async () => {
    const r = await started();
    r.draft('next question');
    r.session.isStreaming = true; r.activity.idle = false;
    r.input(keys.esc);
    expect(r.restoreQueued).toHaveBeenCalledWith({ abort: true });
    expect(r.editor.getText()).toBe('next question');
  });

  it('Esc with a queued message keeps the draft', async () => {
    const r = await started();
    r.draft('draft');
    r.activity.pending = true;
    r.input(keys.esc);
    expect(r.editor.getText()).toBe('draft');
  });

  it('Esc in bash mode is Pi\'s own: it leaves bash mode and clears once', async () => {
    const r = await started();
    r.draft('!ls');
    (r.receiver as Record<string, unknown>).isBashMode = true;
    r.input(keys.esc);
    expect((r.receiver as Record<string, unknown>).isBashMode).toBe(false);
    expect(r.editor.getText()).toBe('');
    r.input(keys.undo);
    expect(r.editor.getText()).toBe('!ls');
  });

  it('Up/Down browse prompt history from the middle of a multi-line draft and restore it', async () => {
    const r = await started();
    r.editor.addToHistory('first');
    r.editor.addToHistory('second');
    r.draft('line one\nline two\nline three');
    r.input(keys.left); r.input(keys.left);
    r.input(keys.up);
    expect(r.editor.getText()).toBe('second');
    r.input(keys.up);
    expect(r.editor.getText()).toBe('first');
    r.input(keys.up);
    expect(r.editor.getText(), 'the oldest entry stays; no wrap-around').toBe('first');
    r.input(keys.down);
    expect(r.editor.getText()).toBe('second');
    r.input(keys.down);
    expect(r.editor.getText()).toBe('line one\nline two\nline three');
    expect(r.editor.getCursor()).toEqual({ line: 2, col: 8 });
    r.input(keys.down);
    expect(r.editor.getText(), 'Down past the newest keeps the draft').toBe('line one\nline two\nline three');
  });

  it('Up with no history keeps the draft', async () => {
    const r = await started();
    r.draft('only draft');
    r.input(keys.up);
    expect(r.editor.getText()).toBe('only draft');
  });

  it('the arrows are history only for that keystroke: the person\'s bindings and other editors are untouched', async () => {
    const r = await started();
    r.kb.setUserBindings({ 'app.model.select': 'ctrl+o' });
    r.editor.addToHistory('earlier');
    r.draft('now');
    r.input(keys.up);
    expect(r.editor.getText()).toBe('earlier');
    expect(r.kb.getUserBindings()).toEqual({ 'app.model.select': 'ctrl+o' });
    // Pi's default editor, now unused, stands for any other editor: its Up still moves the cursor.
    const other = r.receiver.defaultEditor as EditorView;
    other.addToHistory('not for dialogs');
    other.setText('top\nbottom');
    other.handleInput(keys.up);
    expect(other.getText()).toBe('top\nbottom');
    expect(other.getCursor().line).toBe(0);
  });
});
