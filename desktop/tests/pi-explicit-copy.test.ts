// C1–C5: mouse has no write authority; explicit renderer data crosses the actual
// consumer reference and real Pi parser/editor. All clipboard IO is test-owned.
import { existsSync } from 'node:fs';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createOrderedTerminalInputSink, installWindowsClipboardShortcuts, wireProductTerminalClipboard } from '../src/renderer/clipboard-shortcuts';
import { extension, expectSelectionSilent, flush, install, interactiveFile, localEnv, oscCopies, rig, type Rig } from './fixtures/pi-fullscreen-clipboard';
import { editorRig } from './fixtures/pi-editor-keys';
import { consumerHooks } from './fixtures/pi-interactive-consumer';

const bundle = process.env.VC_EDITOR_KEYS_BUNDLE ?? '/tmp/vc-diana-install-proxy/payload/resources/private-runtime/pi/agent/pi~BUN.mjs';
const rigs: Rig[] = [];
beforeEach(() => vi.useFakeTimers());
afterEach(() => { rigs.splice(0).reverse().forEach(r => r.close()); vi.useRealTimers(); });
function key(overrides: Partial<KeyboardEvent> = {}): KeyboardEvent {
  return { type: 'keydown', key: 'с', code: 'KeyC', metaKey: true, ctrlKey: false, altKey: false, shiftKey: false, repeat: false, preventDefault: vi.fn(), ...overrides } as unknown as KeyboardEvent;
}
function renderer(send: (data: string) => void, selection = '', platform = 'darwin') {
  let handler!: (event: KeyboardEvent) => boolean;
  const write = vi.fn<(text: string) => Promise<void>>().mockResolvedValue(undefined);
  const target = { attachCustomKeyEventHandler: (fn: typeof handler) => { handler = fn; }, getSelection: () => selection, paste: vi.fn(), onData: vi.fn() };
  wireProductTerminalClipboard(target, platform, async () => ({ kind: 'empty' }), write, send);
  return { event: (event = key()) => handler(event), write, target };
}

it('consumer AST guard rejects unmeasured outerbinding alias but retains actual setupKeyHandlers', () => {
  const hooks = consumerHooks(interactiveFile, undefined, ['setupKeyHandlers']);
  expect(hooks.methods.has('setupKeyHandlers')).toBe(true);
  expect(() => consumerHooks('unmeasured.js', 'function createInteractiveTuiReference() {} outerbinding_InteractiveMode = class { constructor() { this.ui = createInteractiveTuiReference(() => this.renderer); } };')).toThrow('actual InteractiveMode');
});

it.each(['drag', 'reverse', 'double', 'triple', 'autoscroll'] as const)('C1: %s selection remains visible without any clipboard success', async kind => {
  const r = await rig(Array.from({ length: 30 }, (_, i) => `row${i} Привет 世界 😀`)); rigs.push(r);
  install(await extension(), r);
  if (kind === 'reverse') r.drag([20, 2], [0, 0]);
  else if (kind === 'double' || kind === 'triple') {
    for (let n = 0; n < (kind === 'double' ? 2 : 3); n++) {
      r.terminal.input('\x1b[<0;3;1M'); r.terminal.input('\x1b[<0;3;1m');
      vi.advanceTimersByTime(50);
    }
  } else {
    r.drag([0, 0], [20, kind === 'autoscroll' ? 10 : 2], kind !== 'autoscroll');
    if (kind === 'autoscroll') { vi.advanceTimersByTime(500); r.terminal.input('\x1b[<0;21;11m'); }
  }
  expect(r.tui.getSelectionBounds()).toBeDefined();
  await expectSelectionSilent(r);
});

for (const [label, file] of [['CLI', interactiveFile], ['a08 bundle', bundle]]) describe(label, () => {
  it.skipIf(!existsSync(file))('C2/C3 cross-seam: actual Mac renderer command copies Pi selection, never clears/interrupts empty selection', async () => {
    const r = await editorRig(file); rigs.push(r);
    const env = { ...localEnv, VC_DESKTOP_CHAT_ID: '12345678-1234-4234-8234-123456789abc' };
    install(await extension(env), { ...r, tui: r.reference }, { env });
    r.draft('untouched Черновик\n世界 😀');
    const emitted: string[] = [];
    const front = renderer(data => { emitted.push(data); r.input(data); });
    // No selected transcript: do not substitute Ctrl+C or let super+c insert text.
    front.event(); await flush();
    expect.soft(emitted.length).toBe(1);
    expect.soft(emitted).not.toContain('\x03');
    expect.soft(r.editor.getText()).toBe('untouched Черновик\n世界 😀');
    expect.soft(r.receiver.handleCtrlC).not.toHaveBeenCalled();
    expect.soft(r.submit).not.toHaveBeenCalled();
    expect.soft(r.write).not.toHaveBeenCalled();
    r.drag(); await expectSelectionSilent(r);
    const bounds = r.tui.getSelectionBounds();
    front.event(); await flush();
    expect.soft(emitted.length).toBe(2);
    expect.soft(r.write.mock.calls.map(([text]) => text)).toEqual(['Привет 世界 😀\nстрока два']);
    expect.soft(front.write).not.toHaveBeenCalled();
    expect.soft(oscCopies(r.terminal)).toEqual([]);
    expect.soft(r.tui.getSelectionBounds()).toEqual(bounds);
    expect.soft(r.editor.getText()).toBe('untouched Черновик\n世界 😀');
    expect(r.receiver.handleCtrlC).not.toHaveBeenCalled();
    expect(r.submit).not.toHaveBeenCalled();
  });
});

it.each(['darwin', 'win32'])('C2: %s retains CLI Ctrl+C selection fallback and no-selection interrupt routing', async platform => {
  const r = await rig(); rigs.push(r); install(await extension(), r, { platform });
  r.terminal.input('\x03'); expect(r.focused.handleInput).toHaveBeenCalledWith('\x03'); r.focused.handleInput.mockClear();
  r.drag(); await expectSelectionSilent(r); r.terminal.input('\x03'); await flush();
  expect(r.write.mock.calls.map(([text]) => text)).toEqual(['Привет 世界 😀\nстрока два']);
  expect(r.focused.handleInput).not.toHaveBeenCalled();
});

it.each(['', 'native 世界'])('C3/C4: Mac physical KeyC selection=%j writes or emits once; repeat/release never duplicate', async selection => {
  const send = vi.fn(); const r = renderer(send, selection);
  const press = key(); expect.soft(r.event(press)).toBe(false); expect.soft(press.preventDefault).toHaveBeenCalled();
  r.event(key({ repeat: true })); r.event(key({ type: 'keyup' })); await flush();
  expect.soft(r.write.mock.calls).toEqual(selection ? [[selection]] : []);
  expect.soft(send.mock.calls.length).toBe(selection ? 0 : 1);
  r.event(); await flush();
  expect.soft(r.write.mock.calls.length).toBe(selection ? 2 : 0);
  expect(send.mock.calls.length).toBe(selection ? 0 : 2);
});

it.each([{ ctrlKey: true }, { altKey: true }, { metaKey: false }, { code: 'KeyX' }, { shiftKey: true }])('C3: Mac unrelated/modifier event %j remains terminal-owned', overrides => {
  const send = vi.fn(); const r = renderer(send, 'selected'); const event = key(overrides);
  expect(r.event(event)).toBe(true); expect(event.preventDefault).not.toHaveBeenCalled(); expect(send).not.toHaveBeenCalled(); expect(r.write).not.toHaveBeenCalled();
});

it('C4: Mac copy-only intent waits behind an existing asynchronous paste reservation', () => {
  const send = vi.fn(); const sink = createOrderedTerminalInputSink(send); const pending = sink.reserve();
  let handler!: (event: KeyboardEvent) => boolean;
  installWindowsClipboardShortcuts({ attachCustomKeyEventHandler: fn => { handler = fn; }, getSelection: () => '', paste() {} }, 'darwin', async () => ({ kind: 'empty' }), sink, vi.fn());
  handler(key()); sink.send('later'); expect(send).not.toHaveBeenCalled();
  pending.emit(() => sink.send('paste'));
  expect.soft(send.mock.calls.length).toBe(3);
  expect(send.mock.calls[0]).toEqual(['paste']); expect(send.mock.calls.at(-1)).toEqual(['later']);
  expect(send.mock.calls.flat()).not.toContain('\x03');
});
