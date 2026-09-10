import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { actualReference, deferred, extension, flush, install, localEnv, oscCopies, rig, widgetUI, type LifecycleHandler, type Rig, type TuiView } from './fixtures/pi-fullscreen-clipboard';

beforeEach(() => vi.useFakeTimers());
const rigs: Rig[] = [];
async function make() { const r = await rig(); rigs.push(r); return r; }
afterEach(() => { rigs.splice(0).reverse().forEach((r) => r.close()); vi.useRealTimers(); });
const text = 'Привет 世界 😀\nстрока два';
const hooks = ['copySelectionToClipboard', 'handleSelectionMouseEvent', 'handleViewportInput', 'setFocus', 'showOverlay'] as const;
const methods = (tui: TuiView) => hooks.map((key) => Reflect.get(tui, key));

it.each([
  { label: 'non-VC', env: {} },
  { label: 'SSH connection', env: { ...localEnv, SSH_CONNECTION: 'fixture' } },
  { label: 'SSH client', env: { ...localEnv, SSH_CLIENT: 'fixture' } },
  { label: 'SSH tty', env: { ...localEnv, SSH_TTY: '/fixture/tty' } },
  { label: 'Linux', platform: 'linux' },
  { label: 'wrong version', piVersion: '0.85.0' },
])('guard boundary: $label never probes or transiently owns a fresh TUI', async ({ label, ...options }) => {
  const r = await make(); const module = await extension();
  const mutations: Array<[string, PropertyKey]> = [];
  const calls: PropertyKey[] = [];
  const ui = new Proxy(r.tui, {
    get(target, key) {
      const value = Reflect.get(target, key, target);
      // Reading method metadata is not a clipboard operation; invoking even a
      // temporary receiver probe is. Forward calls so this remains a real TUI.
      return typeof value === 'function' ? function (...args: unknown[]) {
        calls.push(key); return Reflect.apply(value, target, args);
      } : value;
    },
    set(target, key, value) { mutations.push(['set', key]); return Reflect.set(target, key, value, target); },
    defineProperty(target, key, descriptor) { mutations.push(['defineProperty', key]); return Reflect.defineProperty(target, key, descriptor); },
    deleteProperty(target, key) { mutations.push(['deleteProperty', key]); return Reflect.deleteProperty(target, key); },
  });
  expect(() => install(module, { ...r, tui: ui }, options)).not.toThrow();
  expect.soft(mutations, `${label}: includes transient writes even when later deleted`).toEqual([]);
  expect.soft(calls, `${label}: no receiver-probe function calls`).toEqual([]);
  if (label === 'wrong version') expect.soft(r.notify).toHaveBeenCalled();
  expect(r.write).not.toHaveBeenCalled();
});

it.each(['unknown', 'revoked', 'throwing-get', 'throwing-delete'] as const)(
  'passive reference boundary: %s is diagnosed without escaping exceptions', async (kind) => {
    const r = await make(); const module = await extension();
    let ui: TuiView;
    if (kind === 'unknown') ui = {} as TuiView;
    else if (kind === 'revoked') {
      const reference = Proxy.revocable(r.tui, {}); reference.revoke(); ui = reference.proxy;
    } else if (kind === 'throwing-get') {
      ui = new Proxy(r.tui, { get() { throw new Error('reference get denied'); } });
    } else {
      // Intentionally hides its raw target/receiver. Cleanup may be impossible:
      // require passive failure, not restoration through a malicious membrane.
      ui = new Proxy(r.tui, {
        get(target, key) {
          const value = Reflect.get(target, key, target);
          return typeof value === 'function' ? (...args: unknown[]) => {
            const result = Reflect.apply(value, target, args);
            return result === target ? ui : result;
          } : value;
        },
        deleteProperty() { throw new Error('reference delete denied'); },
      });
    }
    expect.soft(() => install(module, { ...r, tui: ui })).not.toThrow();
    expect.soft(r.notify).toHaveBeenCalled();
    expect.soft(r.notify.mock.calls.some(([message]) => typeof message === 'string' && message.trim().length > 0)).toBe(true);
    r.drag(); await flush();
    expect(r.write).not.toHaveBeenCalled();
    expect(oscCopies(r.terminal)).toEqual([text]);
  },
);

it.each([
  { label: 'non-VC', env: {} },
  { label: 'authorized VC', env: localEnv },
])('retirement boundary: next $label install immediately aborts the old pending native write', async ({ env }) => {
  const old = await make(); const next = await make(); const module = await extension();
  let current = old.tui;
  const ui = actualReference(() => current);
  const pending = deferred(); let signal: AbortSignal | undefined;
  old.write.mockImplementation((_value, writeSignal) => { signal = writeSignal; return pending.promise; });
  install(module, { ...old, tui: ui });
  old.drag(); await flush();
  expect(old.write).toHaveBeenCalledTimes(1);
  expect(signal).toBeDefined(); expect(signal!.aborted).toBe(false);
  current = next.tui;
  try {
    install(module, { ...next, tui: ui }, { env });
    // No event, flush, or writer completion may cause the retirement for us.
    expect(signal!.aborted).toBe(true);
    expect(next.write).not.toHaveBeenCalled();
  } finally {
    pending.resolve(); await flush();
  }
});

it('consumer control: actual factory binds receivers, forwards writes, changes renderer and returns fresh closures', async () => {
  const a = await make(); const b = await make(); let current = a.tui;
  const ui = actualReference(() => current);
  expect(ui).not.toBe(a.tui); expect(Object.getPrototypeOf(ui)).toBe(Object.getPrototypeOf(a.tui));
  expect('copySelectionToClipboard' in ui).toBe(true);
  expect(ui.copySelectionToClipboard).not.toBe(ui.copySelectionToClipboard);
  const flash = ui.flash; const decoy = { flash: vi.fn() };
  flash.call(decoy, 'first'); expect(a.flash).toHaveBeenCalledWith('first');
  current = b.tui; flash.call(decoy, 'second'); expect(b.flash).toHaveBeenCalledWith('second');
  expect(decoy.flash).not.toHaveBeenCalled();
  const original = b.tui.copySelectionToClipboard; const replacement = vi.fn();
  ui.copySelectionToClipboard = replacement; expect(b.tui.copySelectionToClipboard).toBe(replacement);
  ui.copySelectionToClipboard = original;
});

describe.each(['raw', 'actual-proxy'] as const)('%s paired semantic boundary', (kind) => {
  const reference = (r: Rig) => kind === 'raw' ? r.tui : actualReference(() => r.tui);
  it('R1/R2/R5/R6: release and repeated Ctrl+C write exact native bytes, no live OSC52 or early Copied', async () => {
    const r = await make(); const ui = reference(r); const pending = deferred();
    r.write.mockReturnValueOnce(pending.promise);
    install(await extension(), { ...r, tui: ui });
    r.drag(); await flush();
    expect.soft(r.write.mock.calls.map(([value]) => value)).toEqual([text]);
    expect.soft(oscCopies(r.terminal)).toEqual([]);
    expect.soft(r.flash).not.toHaveBeenCalledWith('Copied!');
    pending.resolve(); await flush();
    const bounds = r.tui.getSelectionBounds();
    for (const key of ['\x03', '\x03', '\x1b[99;5:1u']) { r.terminal.input(key); await flush(); }
    expect.soft(r.write.mock.calls.map(([value]) => value)).toEqual(Array(4).fill(text));
    expect.soft(oscCopies(r.terminal)).toEqual([]);
    expect(r.tui.getSelectionBounds()).toEqual(bounds);
    expect(r.focused.handleInput).not.toHaveBeenCalled();
    expect(r.flash.mock.calls.filter(([value]) => value === 'Copied!')).toHaveLength(4);
  });

  it.each([false, true])('R4/R7: disposal restores own hooks and preserves foreign replacements=%s', async (foreign) => {
    const r = await make(); const ui = reference(r); const originals = methods(r.tui);
    const symbols = Object.getOwnPropertySymbols(r.tui); const proxySymbols = Object.getOwnPropertySymbols(ui);
    const dispose = install(await extension(), { ...r, tui: ui });
    const later = vi.fn();
    if (foreign) r.tui.copySelectionToClipboard = later;
    dispose(); dispose();
    expect.soft(methods(r.tui)).toEqual(foreign ? [later, ...originals.slice(1)] : originals);
    expect(Object.getOwnPropertySymbols(r.tui)).toEqual(symbols);
    expect(Object.getOwnPropertySymbols(ui)).toEqual(proxySymbols);
    r.drag(); await flush(); expect(r.write).not.toHaveBeenCalled();
    if (foreign) expect(later).toHaveBeenCalledTimes(1);
    else expect.soft(oscCopies(r.terminal)).toEqual([text]);
  });

  it('R7: default lifecycle uses actual consumer widget callback and restores native behavior on shutdown/reload', async () => {
    const r = await make(); const referenceTui = reference(r);
    const ui = await widgetUI(r, referenceTui, true);
    const factory = vi.fn((actual: TuiView) => {
      expect(actual).toBe(referenceTui);
      return { render: () => [], invalidate() {}, dispose: vi.fn() };
    });
    ui.setWidget('control', factory); ui.setWidget('control', undefined);
    expect(factory.mock.results[0].value.dispose).toHaveBeenCalledTimes(1);
    const handlers = new Map<string, LifecycleHandler>();
    const module = await extension();
    await module.default({ on: (name, handler) => handlers.set(name, handler), registerProvider: vi.fn() }, {
      clipboardIO: { platform: 'darwin', env: localEnv, piVersion: '0.84.1', writeText: r.write },
    });
    const ctx = { mode: 'tui', hasUI: true, ui };
    for (let cycle = 0; cycle < 2; cycle++) {
      await handlers.get('session_start')!({ reason: cycle ? 'reload' : 'startup' }, ctx);
      r.drag(); await flush();
      expect.soft(r.write.mock.calls.map(([value]) => value)).toEqual([text]);
      expect.soft(oscCopies(r.terminal)).toEqual([]);
      await handlers.get('session_shutdown')!({ reason: 'reload' }, ctx);
      r.write.mockClear(); r.terminal.output.length = 0;
      r.drag(); await flush();
      expect(r.write).not.toHaveBeenCalled(); expect.soft(oscCopies(r.terminal)).toEqual([text]);
      r.terminal.output.length = 0;
    }
    expect(ui.setEditorComponent).not.toHaveBeenCalled();
  });
});

it.each(['proxy-first', 'raw-first'] as const)('R7: %s repeated installs through two actual proxies and raw target have one owner and no residue', async (order) => {
  const r = await make(); const module = await extension(); const original = methods(r.tui);
  const a = actualReference(() => r.tui); const b = actualReference(() => r.tui);
  const targets = order === 'proxy-first' ? [a, b, r.tui, a] : [r.tui, a, b, r.tui];
  const symbols = targets.map((target) => Object.getOwnPropertySymbols(target));
  const disposes = targets.map((tui) => install(module, { ...r, tui }));
  r.drag(); await flush();
  expect.soft(r.write.mock.calls.map(([value]) => value)).toEqual([text]);
  expect.soft(oscCopies(r.terminal)).toEqual([]);
  disposes.reverse().forEach((dispose) => dispose());
  expect.soft(methods(r.tui)).toEqual(original);
  targets.forEach((target, index) => expect(Object.getOwnPropertySymbols(target)).toEqual(symbols[index]));
  r.write.mockClear(); r.terminal.output.length = 0; r.drag(); await flush();
  expect(r.write).not.toHaveBeenCalled(); expect.soft(oscCopies(r.terminal)).toEqual([text]);
});

it('R4/R7: old proxy ownership cannot cross into a next non-VC renderer, including pending completion and disposal', async () => {
  const old = await make(); const next = await make(); let current = old.tui;
  const ui = actualReference(() => current); const originals = methods(next.tui);
  const pending = deferred(); old.write.mockReturnValue(pending.promise);
  const dispose = install(await extension(), { ...old, tui: ui });
  old.drag(); await flush();
  current = next.tui;
  install(await extension(), { ...next, tui: ui }, { env: {} });
  next.drag(); await flush();
  expect(next.write).not.toHaveBeenCalled(); expect(oscCopies(next.terminal)).toEqual([text]);
  const flashes = next.flash.mock.calls.length;
  pending.resolve(); await flush(); expect(next.flash).toHaveBeenCalledTimes(flashes);
  // A late event on the old renderer must not invoke next-renderer copy/flash methods.
  next.terminal.output.length = 0; next.flash.mockClear(); old.terminal.output.length = 0;
  old.drag(); old.terminal.input('\x03'); await flush();
  expect.soft(oscCopies(next.terminal)).toEqual([]); expect.soft(next.flash).not.toHaveBeenCalled();
  dispose(); expect(methods(next.tui)).toEqual(originals);
  expect.soft(old.write.mock.calls.length).toBeLessThanOrEqual(1);
});
