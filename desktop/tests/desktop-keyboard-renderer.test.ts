import { afterEach, expect, it, vi } from 'vitest';
import { deferred, dispatch, renderer, settle, snapshot, type View } from './fixtures/desktop-keyboard-renderer';

let current: Awaited<ReturnType<typeof renderer>> | undefined;
async function setup(view = snapshot(), platform?: string) { current = await renderer(view, platform); return current; }
afterEach(() => { current?.dispose(); current = undefined; vi.restoreAllMocks(); vi.unstubAllGlobals(); });

// Disconnected helpers, bubble-only listeners, duplicate dispatch and Recent inclusion all fail here.
it.each(['Windows', 'MacIntel', 'Linux'])('K2/K3/K5: actual entry cycles active tabs, captures textarea keys and preserves draft on %s', async platform => {
  const s = await setup(snapshot(), platform);
  const old = s.terminals[0]; old.textarea.value = 'synthetic unsent draft';
  const down = s.key(); const up = s.key({}, 'keyup'); await settle();
  expect(s.select.mock.calls).toEqual([['b']]);
  expect(down.defaultPrevented).toBe(true); expect(up.defaultPrevented).toBe(true);
  expect(s.input).not.toHaveBeenCalled();
  expect(s.start.mock.calls).toEqual([[{ sessionId: 'a', cwd: '/synthetic-no-files', mode: 'resume' }], [{ sessionId: 'b', cwd: '/synthetic-no-files', mode: 'resume' }]]);
  expect((document as unknown as { activeElement: unknown }).activeElement).toBe(s.terminals[1].textarea);
  s.key({ shiftKey: true }); await settle(); // inverse
  s.key({ shiftKey: true }); await settle(); // backward wrap
  s.key(); await settle(); // forward wrap
  expect(s.select.mock.calls).toEqual([['b'], ['a'], ['c'], ['a']]);
  expect(old.textarea.value).toBe('synthetic unsent draft');
  expect(s.resume).not.toHaveBeenCalled(); expect(s.newChat).not.toHaveBeenCalled();
});

// This fails independently of selection, so a disconnected capture path reports the actual input leak.
it('K3: CtrlTab is reserved before the textarea target can send Tab into terminal input', async () => {
  const s = await setup();
  const down = s.key(); const up = s.key({}, 'keyup'); await settle();
  expect.soft(s.input.mock.calls, 'CtrlTab leaked through mocked key routing into real Terminal.input/onData').toEqual([]);
  expect.soft(down.defaultPrevented).toBe(true); expect(up.defaultPrevented).toBe(true);
});

// Ctrl may be released before Tab; the matching release is still owned.
it('K3: consumes matching Tab keyup after Control was released, but not unrelated keyup', async () => {
  const s = await setup(); s.key();
  expect(s.key({ ctrlKey: false }, 'keyup').defaultPrevented).toBe(true);
  expect(s.key({ ctrlKey: false }, 'keyup').defaultPrevented).toBe(false);
  await settle(); expect(s.select.mock.calls).toEqual([['b']]);
});

// Browser auto-repeat produces multiple navigation keydowns but only one physical release.
it('K3 regression: repeated CtrlTab owns one release, not the following ordinary Tab release', async () => {
  const s = await setup();
  expect(s.key().defaultPrevented).toBe(true);
  for (let i = 0; i < 2; i++) expect(s.key({ repeat: true }).defaultPrevented).toBe(true);
  expect(s.key({ ctrlKey: false }, 'keyup').defaultPrevented).toBe(true);
  await settle();
  expect(s.select.mock.calls).toEqual([['b'], ['c'], ['a']]);
  expect(s.input).not.toHaveBeenCalled();
  expect(s.key({ ctrlKey: false }).defaultPrevented).toBe(false);
  expect(s.key({ ctrlKey: false }, 'keyup').defaultPrevented).toBe(false);
  await settle(); expect(s.select.mock.calls).toEqual([['b'], ['c'], ['a']]);
});

// Losing window focus can lose the matching keyup altogether; no OS injection is needed.
it('K3 regression: window blur clears a lost owned release without disabling normal release ownership', async () => {
  const s = await setup();
  expect(s.key().defaultPrevented).toBe(true);
  expect(s.key({ ctrlKey: false }, 'keyup').defaultPrevented).toBe(true);
  await settle();
  expect(s.key().defaultPrevented).toBe(true);
  dispatch(s.doc.parent!, 'blur', {});
  await settle();
  expect(s.select.mock.calls).toEqual([['b'], ['c']]);
  expect(s.key({ ctrlKey: false }).defaultPrevented).toBe(false);
  expect.soft(s.key({ ctrlKey: false }, 'keyup').defaultPrevented).toBe(false);
  expect(s.key().defaultPrevented).toBe(true);
  expect(s.key({ ctrlKey: false }, 'keyup').defaultPrevented).toBe(true);
  await settle(); expect(s.select.mock.calls).toEqual([['b'], ['c'], ['a']]);
});

// These are negative ownership controls, not a replacement for native keyboard routing.
it('K3: ordinary Tab, ShiftTab, CmdTab, Alt/AltGr, composition and Pi keys retain ownership', async () => {
  const s = await setup();
  for (const init of [
    { ctrlKey: false }, { ctrlKey: false, shiftKey: true }, { ctrlKey: false, metaKey: true },
    { altKey: true }, { metaKey: true }, { isComposing: true },
    { key: 'Alt', code: 'AltLeft', ctrlKey: false, altKey: true },
    { key: 'x', code: 'KeyX', ctrlKey: false, altKey: true },
    { key: 'Escape', code: 'Escape', ctrlKey: false }, { key: 'ArrowUp', code: 'ArrowUp', ctrlKey: false },
  ]) expect(s.key(init).defaultPrevented).toBe(false);
  await settle(); expect(s.select).not.toHaveBeenCalled(); expect(s.newChat).not.toHaveBeenCalled();
});

it('K3: explicit AltGraph with Control but without Alt is not navigation', async () => {
  const s = await setup();
  const init = { ctrlKey: true, altKey: false, altGraph: true };
  const down = s.key(init); const up = s.key(init, 'keyup'); await settle();
  expect(down.ctrlKey).toBe(true); expect(down.altKey).toBe(false);
  expect(down.getModifierState('AltGraph')).toBe(true);
  expect(down.defaultPrevented).toBe(false); expect(up.defaultPrevented).toBe(false);
  expect(s.select).not.toHaveBeenCalled(); expect(s.resume).not.toHaveBeenCalled(); expect(s.newChat).not.toHaveBeenCalled();
});

// A global handler must not discard rename state or blur/commit the title editor.
it('K3: CtrlTab during inline rename keeps exact draft and selection ownership', async () => {
  const s = await setup();
  s.node('#tabs').children[0].children[0].click(); s.frame();
  const input = s.node('#tabs').children[0].children[0];
  expect(input.tagName).toBe('INPUT'); input.value = '  unfinished title  '; dispatch(input, 'input', {});
  s.key({}, 'keydown', input); s.key({}, 'keyup', input); await settle();
  expect(s.select).not.toHaveBeenCalled(); expect(s.rename).not.toHaveBeenCalled();
  expect(input.value).toBe('  unfinished title  '); expect(input.isConnected).toBe(true);
  expect((document as unknown as { activeElement: unknown }).activeElement).toBe(input);
});

// Cardinality and unavailable workspace must not create/resume a chat as a side effect.
it.each([
  ['zero', snapshot(null, [])], ['one', snapshot('a', ['a'])],
  ['missing', { workspace: null, recoveryPath: null }], ['recovery', { ...snapshot(), recoveryPath: '/missing' }],
] as Array<[string, View]>)('K2: %s workspace is navigation-inert', async (_label, view) => {
  const s = await setup(view); const starts = s.start.mock.calls.length;
  const textarea = _label === 'one' ? s.terminals[0].textarea : undefined;
  if (textarea) textarea.value = 'synthetic one-tab unsent draft';
  const target = textarea ?? s.doc;
  for (const shiftKey of [false, true]) {
    const down = s.key({ shiftKey }, 'keydown', target);
    const up = s.key({ shiftKey }, 'keyup', target);
    expect.soft(down.defaultPrevented, `${_label}: reserved keydown`).toBe(true);
    expect.soft(up.defaultPrevented, `${_label}: reserved keyup`).toBe(true);
  }
  await settle();
  expect.soft(s.input.mock.calls, 'reserved chord must not reach PTY bridge').toEqual([]);
  if (textarea) expect.soft(textarea.value).toBe('synthetic one-tab unsent draft');
  expect(s.select).not.toHaveBeenCalled(); expect(s.resume).not.toHaveBeenCalled(); expect(s.newChat).not.toHaveBeenCalled(); expect(s.start).toHaveBeenCalledTimes(starts);
});

it.each([[false, 'a'], [true, 'c']] as const)('K2: missing selection uses directional edge (backward=%s)', async (shiftKey, expected) => {
  const s = await setup(snapshot('missing'));
  s.key({ shiftKey }); await settle(); expect(s.select.mock.calls).toEqual([[expected]]);
});

// Select completion alone is not completion of selectChat: sleeping target launch can still be pending.
it('K4: rapid distinct/repeated keys serialize through select AND resume-start boundaries', async () => {
  const s = await setup(); const selection = deferred<View>(); const launch = deferred<{ showSharedFilesWarning: boolean }>();
  s.select.mockImplementationOnce(() => selection.promise); s.start.mockImplementationOnce(() => launch.promise);
  s.key(); s.key({ repeat: true }); s.key(); await settle();
  expect(s.select.mock.calls).toEqual([['b']]);
  s.setServer(snapshot('b')); selection.resolve(snapshot('b')); await settle();
  expect(s.select.mock.calls).toEqual([['b']]);
  launch.resolve({ showSharedFilesWarning: false }); await settle();
  expect(s.select.mock.calls).toEqual([['b'], ['c'], ['a']]);
  expect(s.input).not.toHaveBeenCalled();
});

// The queued shortcut predates this edit, but must re-check edit ownership when it executes.
it('K3/K4 regression: queued CtrlTab preserves a newer B title draft while sleeping-target start is pending', async () => {
  const s = await setup(); const launch = deferred<{ showSharedFilesWarning: boolean }>();
  s.start.mockImplementationOnce(() => launch.promise);
  s.key(); s.key({}, 'keyup'); await settle();
  expect(s.select.mock.calls).toEqual([['b']]);
  expect(s.start.mock.calls.at(-1)).toEqual([{ sessionId: 'b', cwd: '/synthetic-no-files', mode: 'resume' }]);
  expect(s.node('#tabs').children[1].className).toBe('tab selected');
  s.key(); s.key({}, 'keyup'); await settle();
  expect(s.select.mock.calls).toEqual([['b']]);
  s.node('#tabs').children[1].children[0].click(); s.frame();
  const input = s.node('#tabs').children[1].children[0];
  expect(input.tagName).toBe('INPUT');
  const draft = '  newer unfinished B title  ';
  input.value = draft; dispatch(input, 'input', {});
  expect(s.doc.contains(input)).toBe(true);
  expect((document as unknown as { activeElement: unknown }).activeElement).toBe(input);
  expect(s.rename).not.toHaveBeenCalled();
  launch.resolve({ showSharedFilesWarning: false }); await settle(); s.frame(); await settle();
  // render() may rebuild the editor: inspect the current tree, never the detached old input.
  const currentB = s.node('#tabs').children[1]; const currentDraft = currentB.children[0];
  expect.soft(s.select.mock.calls, 'queued navigation must not leave the newer active edit').toEqual([['b']]);
  expect.soft(currentB.className).toBe('tab selected');
  expect.soft(currentDraft.tagName).toBe('INPUT');
  expect.soft(currentDraft.value).toBe(draft);
  expect(s.doc.contains(currentDraft)).toBe(true);
  expect(s.rename).not.toHaveBeenCalled();
  expect(s.resume).not.toHaveBeenCalled(); expect(s.newChat).not.toHaveBeenCalled();
});

// Precomputing queued indexes selects C after C moved to Recent.
it('K4: next queued target comes from latest returned active view', async () => {
  const s = await setup(); const selection = deferred<View>(); s.select.mockImplementationOnce(() => selection.promise);
  s.key(); s.key(); await settle(); expect(s.select.mock.calls).toEqual([['b']]);
  const changed = snapshot('b', ['a', 'b']); changed.workspace!.tabs.push({ id: 'c', title: 'C', location: 'recent', state: 'sleeping' });
  s.setServer(changed); selection.resolve(changed); await settle();
  expect(s.select.mock.calls).toEqual([['b'], ['a']]); expect(s.resume).not.toHaveBeenCalled();
});

// A failed operation must surface once, consume its slot and leave successors usable.
it('K4/K5: select rejection announces once, drains queue and preserves original draft', async () => {
  const s = await setup(); const selection = deferred<View>(); s.select.mockImplementationOnce(() => selection.promise);
  s.terminals[0].textarea.value = 'old draft';
  const notices: string[] = []; let text = '';
  Object.defineProperty(s.node('#notice'), 'textContent', { get: () => text, set: (value: string) => { text = value; notices.push(value); }, configurable: true });
  s.key(); s.key(); await settle(); expect(s.select.mock.calls).toEqual([['b']]);
  selection.reject(new Error('synthetic select failure')); await settle();
  expect(notices).toHaveLength(1); expect(notices[0]).toMatch(/could not|failed|unable/i); expect(s.node('#notice').hidden).toBe(false);
  expect(s.select.mock.calls).toEqual([['b'], ['b']]);
  s.key(); await settle(); expect(s.select.mock.calls).toEqual([['b'], ['b'], ['c']]);
  expect(s.terminals[0].textarea.value).toBe('old draft'); expect(s.input).not.toHaveBeenCalled();
});

// Approved K4 bound: 8 pending directions, drop-newest overflow, no unbounded repeat queue.
it('K4: retains at most eight pending directions while select is blocked', async () => {
  const s = await setup(); const selection = deferred<View>(); s.select.mockImplementationOnce(() => selection.promise);
  s.key(); for (let i = 0; i < 20; i++) s.key({ repeat: true });
  await settle(); expect(s.select.mock.calls).toEqual([['b']]);
  s.setServer(snapshot('b')); selection.resolve(snapshot('b')); await settle();
  expect(s.select.mock.calls).toEqual(['b', 'c', 'a', 'b', 'c', 'a', 'b', 'c', 'a'].map(id => [id]));
  expect(s.input).not.toHaveBeenCalled();
});
