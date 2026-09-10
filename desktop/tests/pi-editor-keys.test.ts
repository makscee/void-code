import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { attach, editorRig, keys as k, keysModule, startDefault, type EditorRig } from './fixtures/pi-editor-keys';
import { flush, install, oscCopies } from './fixtures/pi-fullscreen-clipboard';

// K1–K7 plan: native controls paired with managed RED; only Pi owns undo/history.
// Negative/boundary groups dominate; no submit/inference/files/native clipboard.
// Source-hook integration is NOT interactive CLI/PTY or GUI acceptance.
const live: EditorRig[] = [];
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(10_000); vi.stubGlobal('fetch', vi.fn(() => { throw new Error('offline only'); })); });
afterEach(() => { live.reverse().forEach(r => r.close()); live.length = 0; vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers(); });
async function make(feature = false) {
  const r = await editorRig(); live.push(r);
  if (feature) attach(await keysModule(), r);
  return r;
}
function seed(r: EditorRig) {
  r.editor.addToHistory('USER oldest 世界'); r.editor.addToHistory('USER latest\nsecond line');
  r.draft('draft 😀\nmiddle cursor\nlast');
  // Position with native keys before installation where needed.
  r.editor.handleInput(k.up); r.editor.handleInput(k.left);
}

it('native control: fresh multiline Esc does not clear; middle Up moves cursor, not history', async () => {
  const r = await make(); seed(r); const draft = r.editor.getText(); const cursor = r.editor.getCursor();
  r.input(k.esc); expect(r.editor.getText()).toBe(draft);
  r.input(k.up); expect(r.editor.getText()).toBe(draft); expect(r.editor.getCursor()).not.toEqual(cursor);
  expect(r.submit).not.toHaveBeenCalled(); expect(r.receiver.handleCtrlC).not.toHaveBeenCalled();
});

it('native control: Pi navigateHistory owns draft/cursor and setText is undoable', async () => {
  const r = await make(); seed(r); const text = r.editor.getText(); const cursor = r.editor.getCursor();
  r.editor.navigateHistory(-1); expect(r.editor.getText()).toBe('USER latest\nsecond line');
  r.editor.navigateHistory(1); expect(r.editor.getText()).toBe(text); expect(r.editor.getCursor()).toEqual(cursor);
  r.editor.setText(''); r.input(k.undo); expect(r.editor.getText()).toBe(text); expect(r.editor.getCursor()).toEqual(cursor);
});
it.each(['Привет 世界 😀\nе́ second\nlast', '   \n  ', 'fresh draft'])('K1 feature: Esc clears complete %j and native undo restores', async text => {
  const r = await make(); r.draft(text); const cursor = r.editor.getCursor();
  attach(await keysModule(), r);
  expect(r.editor.getText()).toBe(text); // install must never clear
  r.input(k.esc); expect(r.editor.getText()).toBe('');
  r.input(k.undo); expect(r.editor.getText()).toBe(text); expect(r.editor.getCursor()).toEqual(cursor);
  expect(r.submit).not.toHaveBeenCalled(); expect(r.receiver.handleCtrlC).not.toHaveBeenCalled(); expect(r.receiver.handleCtrlD).not.toHaveBeenCalled();
});
it('K1 boundary: expanded paste survives Esc/undo, including Pi marker storage', async () => {
  const r = await make(); const text = '世界 😀\n'.repeat(30); r.draft(text);
  expect(r.editor.getText()).not.toBe(text); expect(r.editor.getExpandedText()).toBe(text);
  attach(await keysModule(), r); r.input(k.esc); expect(r.editor.getText()).toBe('');
  r.input(k.undo); expect(r.editor.getExpandedText()).toBe(text);
});
it('K3 feature: middle-cursor history uses the SAME Pi objects and restores exact draft/cursor', async () => {
  const r = await make(); seed(r); const draft = r.editor.getText(); const cursor = r.editor.getCursor(); const history = r.editor.history;
  attach(await keysModule(), r); const navigate = vi.spyOn(r.editor, 'navigateHistory');
  r.input(k.up); expect(r.editor.getText()).toBe('USER latest\nsecond line');
  r.input(k.up); expect(r.editor.getText()).toBe('USER oldest 世界');
  r.input(k.down); expect(r.editor.getText()).toBe('USER latest\nsecond line');
  r.input(k.down); expect(r.editor.getText()).toBe(draft); expect(r.editor.getCursor()).toEqual(cursor);
  expect(navigate.mock.calls).toEqual([[-1], [-1], [1], [1]]); expect(r.editor.history).toBe(history); expect(r.tui.focusedComponent).toBe(r.editor);
});
it.each(['empty history', 'oldest boundary', 'newest boundary'])('K3 boundary: %s cannot erase or wrap', async boundary => {
  const r = await make(); if (boundary !== 'empty history') seed(r); else r.draft('draft\nmiddle\nlast');
  attach(await keysModule(), r);
  if (boundary === 'oldest boundary') { r.input(k.up); r.input(k.up); }
  const text = r.editor.getText(); const cursor = r.editor.getCursor();
  r.input(boundary === 'newest boundary' ? k.down : k.up);
  expect(r.editor.getText()).toBe(text); expect(r.editor.getCursor()).toEqual(cursor);
});

for (const feature of [false, true]) describe(feature ? 'managed guard' : 'native control', () => {
  it('K2: !bash cancellation runs even though ctx.isIdle is true', async () => {
    const r = await make(feature); r.draft('draft'); r.session.isBashRunning = true;
    r.session.abortBash.mockImplementation(() => { expect(r.editor.getText(), 'native Esc must run BEFORE any clear').toBe('draft'); });
    expect(r.ctx.isIdle()).toBe(true); r.input(k.esc);
    expect(r.session.abortBash).toHaveBeenCalledTimes(1);
    // An optional clear AFTER native bash cancellation is allowed by ordinary-idle check.
    expect(r.receiver.handleCtrlC).not.toHaveBeenCalled();
  });
  it('K2: streaming Esc restores queued text via native callback; no extra clear', async () => {
    const r = await make(feature); r.draft('draft'); r.activity.idle = false; r.session.isStreaming = true;
    r.receiver.restoreQueuedMessagesToEditor.mockImplementation(() => r.editor.setText('queued USER\nrestore'));
    r.input(k.esc); expect(r.receiver.restoreQueuedMessagesToEditor).toHaveBeenCalledExactlyOnceWith({ abort: true });
    expect(r.editor.getText()).toBe('queued USER\nrestore');
  });
  it.each(['retry', 'compaction', 'queued continuation'])('K2: busy %s remains native even without streaming', async () => {
    const r = await make(feature); r.draft('retained'); r.activity.idle = false; r.activity.pending = true;
    r.input(k.esc); expect(r.editor.getText()).toBe('retained'); expect(r.receiver.handleCtrlC).not.toHaveBeenCalled();
  });
  it.each([false, true])('K2: temporary onEscape override stays authoritative (changes text=%s)', async changes => {
    const r = await make(feature); r.draft('retained');
    const temporary = vi.fn(() => { if (changes) r.editor.setText('native restored'); }); r.editor.onEscape = temporary;
    r.input(k.esc); expect(temporary).toHaveBeenCalledTimes(1); expect(r.editor.onEscape).toBe(temporary);
    expect(r.editor.getText()).toBe(changes ? 'native restored' : 'retained');
  });
  it('K2: empty/repeated Esc never becomes Ctrl+C, exit or submit', async () => {
    const r = await make(feature); const setText = vi.spyOn(r.editor, 'setText');
    for (let i = 0; i < 4; i++) r.input(k.esc);
    expect(setText).not.toHaveBeenCalled(); expect(r.editor.getText()).toBe(''); expect(r.receiver.handleCtrlC).not.toHaveBeenCalled(); expect(r.receiver.handleCtrlD).not.toHaveBeenCalled(); expect(r.submit).not.toHaveBeenCalled();
  });
  it.each([k.esc, k.up, k.down])('K2/K4: focused selector owns %j', async key => {
    const r = await make(feature); r.draft('main draft');
    const menu = { render: () => ['menu'], invalidate() {}, handleInput: vi.fn() };
    const overlay = r.tui.showOverlay(menu); r.input(key);
    expect(menu.handleInput).toHaveBeenCalledExactlyOnceWith(key); expect(r.editor.getText()).toBe('main draft'); overlay.hide();
  });
  it.each(['\x1b[1;2A', '\x1b[1;5B', '\x1b[27;2u', '\x1b[1;3A', k.left, k.right])('K4: modifier/other key %j stays native', async key => {
    const r = await make(feature); r.draft('abc\ndef'); const navigation = vi.spyOn(r.editor, 'navigateHistory');
    r.input(key); expect(navigation).not.toHaveBeenCalled(); expect(r.editor.getText()).toBe('abc\ndef');
    expect(r.receiver.handleCtrlC).not.toHaveBeenCalled();
  });
  it.each(['\x1b[27;1:3u', '\x1b[1;1:3A', '\x1b[1;1:3B'])('K4: Kitty release %j has no effect', async key => {
    const r = await make(feature); r.draft('abc\ndef'); const cursor = r.editor.getCursor(); const navigation = vi.spyOn(r.editor, 'navigateHistory');
    r.input(key); expect(r.editor.getText()).toBe('abc\ndef'); expect(r.editor.getCursor()).toEqual(cursor); expect(navigation).not.toHaveBeenCalled();
  });
  it('K4: split paste buffers arrows instead of navigating history', async () => {
    const r = await make(feature); r.editor.addToHistory('old USER'); const navigate = vi.spyOn(r.editor, 'navigateHistory');
    r.input('\x1b[200~first'); expect(r.editor.isInPaste).toBe(true);
    r.input(k.up); r.input(k.down); expect(navigate).not.toHaveBeenCalled(); expect(r.editor.getText()).toBe('');
    r.input('last\x1b[201~'); expect(r.editor.isInPaste).toBe(false); expect(r.editor.getText()).toContain('first'); expect(r.editor.getText()).toContain('last');
  });
  it('K4: character jump then arrow follows native movement, not history', async () => {
    const r = await make(feature); r.draft('one\ntwo\nthree'); r.editor.addToHistory('old USER');
    r.input('\x1d'); expect(r.editor.jumpMode).toBe('forward'); const navigate = vi.spyOn(r.editor, 'navigateHistory');
    r.input(k.up); expect(r.editor.jumpMode).toBe(null); expect(navigate).not.toHaveBeenCalled(); expect(r.editor.getText()).toBe('one\ntwo\nthree');
  });
  it.each([k.esc, k.up, k.down])('K4: actual autocomplete owns %j', async key => {
    const r = await make(feature); r.editor.addToHistory('old USER');
    r.editor.setAutocompleteProvider({
      getSuggestions: async () => ({ prefix: '@', items: [{ value: '@one', label: 'one' }, { value: '@two', label: 'two' }] }),
      applyCompletion: () => { throw new Error('Esc/arrows must not apply completion'); },
    });
    r.draft('@'); r.input('\t'); await flush(); await r.editor.autocompleteRequestTask;
    expect(r.editor.isShowingAutocomplete()).toBe(true);
    const navigate = vi.spyOn(r.editor, 'navigateHistory'); r.input(key);
    expect(navigate).not.toHaveBeenCalled(); expect(r.editor.getText()).toBe('@');
    expect(r.editor.isShowingAutocomplete()).toBe(key !== k.esc);
    if (key === k.down) expect(r.editor.autocompleteList?.getSelectedItem().value).toBe('@two');
  });
  it('K4: Esc inside split paste must not clear buffered draft', async () => {
    const r = await make(feature); r.draft('prefix'); r.input('\x1b[200~chunk'); r.input(k.esc);
    expect(r.editor.getText()).toBe('prefix'); expect(r.editor.isInPaste).toBe(true);
    r.input('tail\x1b[201~'); expect(r.editor.getText()).toBe('prefixchunktail');
  });
  it('K4: extension shortcut claims input before editor actions', async () => {
    const r = await make(feature); r.draft('retained'); r.editor.onExtensionShortcut = vi.fn(() => true);
    r.input(k.esc); r.input(k.up); expect(r.editor.getText()).toBe('retained'); expect(r.editor.onExtensionShortcut).toHaveBeenCalledTimes(2);
  });
});

it.each(['\x1b[1;1:1A', '\x1b[1;1:2A'])('K4: Kitty press/repeat %j navigates exactly once', async key => {
  const r = await make(); seed(r); attach(await keysModule(), r);
  const navigate = vi.spyOn(r.editor, 'navigateHistory'); r.input(key);
  expect(navigate).toHaveBeenCalledExactlyOnceWith(-1); expect(r.editor.getText()).toBe('USER latest\nsecond line');
});
it('K3 boundary: editing recalled history and undo matches Pi, never mutates stored USER entries', async () => {
  const control = await make(); const feature = await make(); seed(control); seed(feature);
  const history = [...feature.editor.history]; attach(await keysModule(), feature);
  control.editor.navigateHistory(-1); feature.input(k.up);
  for (const key of ['X', k.undo, k.undo]) {
    control.input(key); feature.input(key);
    expect(feature.editor.getText()).toBe(control.editor.getText()); expect(feature.editor.getCursor()).toEqual(control.editor.getCursor());
  }
  expect(feature.editor.history).toEqual(history);
});
it.each(['cli', 'desktop'])('K6 RED: ordinary default factory reaches actual reference/editor in %s', async mode => {
  const r = await make(); seed(r);
  const env = { VC_BOOTSTRAP_EXECUTABLE: '/isolated/vc', SystemRoot: 'C:\\Windows', ...(mode === 'desktop' ? { VC_DESKTOP_SESSION: '1', VC_DESKTOP_CHAT_ID: 'owned' } : {}) };
  await startDefault(await keysModule(env), r);
  expect(r.ctx.ui.setEditorComponent).not.toHaveBeenCalled();
  r.input(k.esc); expect(r.editor.getText(), 'default Go delivery must clear, not just export a seam').toBe('');
  r.draft('new\nmiddle\nlast'); r.input(k.up); expect(r.editor.getText()).toBe('USER latest\nsecond line');
});
it('K5/K6: dispose restores only owned hook; foreign later hook survives', async () => {
  const r = await make(); const original = r.editor.handleInput; const module = await keysModule();
  const dispose = attach(module, r); dispose(); expect(r.editor.handleInput).toBe(original);
  const again = attach(module, r); const foreign = vi.fn(); r.editor.handleInput = foreign; again(); again();
  expect(r.editor.handleInput).toBe(foreign); r.input(k.up); expect(foreign).toHaveBeenCalledTimes(1);
});
it('K6: duplicate ownership cannot double-navigate; stale disposer cannot kill owner', async () => {
  const r = await make(); seed(r); const module = await keysModule(); attach(module, r); const duplicate = attach(module, r); duplicate();
  const navigation = vi.spyOn(r.editor, 'navigateHistory'); r.input(k.up);
  expect(navigation).toHaveBeenCalledExactlyOnceWith(-1); expect(r.editor.getText()).toBe('USER latest\nsecond line');
});
it('K2 boundary: a temporary callback already active at installation is not ordinary idle', async () => {
  const r = await make(); r.draft('retained'); const temporary = vi.fn(); r.editor.onEscape = temporary;
  attach(await keysModule(), r); r.input(k.esc);
  expect(temporary).toHaveBeenCalledTimes(1); expect(r.editor.getText()).toBe('retained');
});
it.each(['version', 'non-VC', 'rpc', 'print', 'json', 'no UI', 'custom factory', 'throwing factory getter', 'unknown shape'])('K5: fail passive for %s', async kind => {
  const r = await make(); r.draft('retained'); const original = r.editor.handleInput;
  if (['rpc', 'print', 'json'].includes(kind)) r.ctx.mode = kind;
  if (kind === 'no UI') r.ctx.hasUI = false;
  if (kind === 'throwing factory getter') vi.mocked(r.ctx.ui.getEditorComponent).mockImplementation(() => { throw new Error('foreign UI getter'); });
  if (kind === 'custom factory') vi.mocked(r.ctx.ui.getEditorComponent).mockReturnValue(() => r.editor);
  if (kind === 'unknown shape') Object.defineProperty(r.editor, 'navigateHistory', { value: undefined, configurable: true });
  attach(await keysModule(), r, kind === 'version' ? { piVersion: '0.85.0' } : kind === 'non-VC' ? { env: {} } : {});
  expect(r.editor.handleInput).toBe(original); r.input(k.esc); expect(r.editor.getText()).toBe('retained');
});
it.each([k.esc, k.up, '\x1b[200~paste\x1b[201~'])('K6: old clipboard selection cannot steal Ctrl+C after %j', async key => {
  const r = await make(); seed(r); const module = await keysModule(); install(module, { ...r, tui: r.reference }); attach(module, r);
  r.drag(); await flush(); expect(r.write).toHaveBeenCalledTimes(1); r.write.mockClear();
  r.input(key); r.input('\x03'); await flush();
  expect(r.write).not.toHaveBeenCalled(); expect(oscCopies(r.terminal)).toEqual([]); expect(r.receiver.handleCtrlC).toHaveBeenCalledTimes(1);
});
