import { EventEmitter } from 'node:events';
import { pathToFileURL } from 'node:url';
import { PassThrough } from 'node:stream';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { agentMetadata, deferred, extension, flush, install, localEnv, oscCopies, realPi, rig, widgetUI, type Rig, type LifecycleHandler } from './fixtures/pi-fullscreen-clipboard';

beforeEach(() => vi.useFakeTimers());
const rigs: Rig[] = [];
async function make(lines?: string[]): Promise<Rig> { const value = await rig(lines); rigs.push(value); return value; }
afterEach(() => { rigs.splice(0).reverse().forEach((value) => value.close()); vi.useRealTimers(); });

// Reject even transient live assignments, while allowing disposable inherited receivers.
function guardLiveMethod<T extends object, K extends keyof T & string>(owner: T, key: K) {
  const descriptor = Object.getOwnPropertyDescriptor(owner, key)!;
  const method = owner[key]; const attempts = vi.fn();
  Object.defineProperty(owner, key, { configurable: true, enumerable: descriptor.enumerable,
    get: () => method,
    set(value) {
      if (this === owner) { attempts(); throw new Error(`live ${key} assignment forbidden`); }
      Object.defineProperty(this, key, { value, writable: true, configurable: true });
    },
  });
  return { attempts, restore: () => Object.defineProperty(owner, key, descriptor) };
}

it('fixture control: VERSION exposed to managed code matches actual pinned config export', async () => {
  const config = await import(/* @vite-ignore */ pathToFileURL(`${agentMetadata.getPackageDir()}/dist/config.js`).href);
  expect(agentMetadata.VERSION).toBe('0.84.1');
  expect(agentMetadata.VERSION).toBe(config.VERSION);
});

it('fixture control: guarded live methods still allow genuine Pi mouse extraction and flash', async () => {
  const r = await make();
  const guards = [guardLiveMethod(r.terminal, 'write'), guardLiveMethod(r.tui, 'flash')];
  try {
    const write = r.terminal.write; const flash = r.tui.flash;
    expect(() => { r.terminal.write = write; }).toThrow('live write');
    expect(() => { r.tui.flash = flash; }).toThrow('live flash');
    guards.forEach((guard) => guard.attempts.mockClear());
    r.drag();
    expect(oscCopies(r.terminal)).toEqual(['Привет 世界 😀\nстрока два']);
    expect(r.flash).toHaveBeenCalledWith('Copied!');
    guards.forEach((guard) => expect(guard.attempts).not.toHaveBeenCalled());
  } finally { guards.forEach((guard) => guard.restore()); }
});

describe('real Pi fullscreen selection -> managed native clipboard', () => {
  it('fixture control: exported pinned Pi renders and extracts Unicode via mouse SGR, without xterm selection', async () => {
    const r = await make();
    expect(r.terminal.output.join('')).toContain('Привет');
    r.drag();
    expect(r.tui.getSelectionBounds()).toMatchObject({ start: { row: 0, col: 0, scrollView: r.scroll }, end: { row: 1, col: 30, scrollView: r.scroll } });
    expect(oscCopies(r.terminal)).toEqual(['Привет 世界 😀\nстрока два']);
  });

  it('fixture control: scrolled wide-cell extraction and reverse drag agree in real Pi', async () => {
    const lines = Array.from({ length: 24 }, (_, i) => `row${i} 世界 😀 é`);
    const forward = await make(lines); const reverse = await make(lines);
    for (const target of [forward, reverse]) { target.scroll.scrollTo(10); target.draw(); }
    forward.drag([6, 1], [12, 3]); reverse.drag([12, 3], [6, 1]);
    expect(forward.tui.getSelectionBounds()).toMatchObject({ start: { row: 11, scrollView: forward.scroll }, end: { row: 13, scrollView: forward.scroll } });
    expect(oscCopies(forward.terminal)[0]).toContain('row12 世界 😀 é');
    expect(oscCopies(reverse.terminal)).toEqual(oscCopies(forward.terminal));
  });

  it('fixture control: pinned Pi retains released selection across keyboard input and routes menu/Kitty events normally', async () => {
    const r = await make(); r.drag();
    const bounds = r.tui.getSelectionBounds();
    const keys = ['draft', '\x1b[200~paste\x1b[201~', '\x1b', '\x03'];
    keys.forEach((key) => r.terminal.input(key));
    expect(r.tui.getSelectionBounds()).toEqual(bounds);
    expect(r.focused.handleInput.mock.calls.map(([text]) => text)).toEqual(keys);
    expect(r.tui.focusedComponent).toBe(r.focused);
    r.focused.handleInput.mockClear(); r.terminal.input('\x1b[99;5:3u');
    expect(r.focused.handleInput).not.toHaveBeenCalled();
    const menu = { render: () => ['menu'], invalidate() {}, handleInput: vi.fn() };
    const overlay = r.tui.showOverlay(menu); r.terminal.input('\x03');
    expect(menu.handleInput).toHaveBeenCalledWith('\x03');
    overlay.hide(); r.terminal.input('\x03');
    expect(r.focused.handleInput).toHaveBeenCalledWith('\x03');
    expect(oscCopies(r.terminal)).toEqual(['Привет 世界 😀\nстрока два']);
  });

  it.each([false, true])('R1: %s reverse drag copies exact real Pi Unicode and styled/OSC8 plain text, replacing sentinel', async (reverse) => {
    const lines = ['\x1b[31mПривет\x1b[0m 世界 😀', '\x1b]8;;https://example.invalid\x07строка два\x1b]8;;\x07'];
    const oracle = await make(lines); const r = await make(lines);
    const from = reverse ? [30, 1] : [0, 0]; const to = reverse ? [0, 0] : [30, 1];
    oracle.drag(from, to);
    expect(oscCopies(oracle.terminal)).toEqual(['Привет 世界 😀\nстрока два']);
    let clipboard = 'OLD-SENTINEL';
    r.write.mockImplementation(async (text) => { clipboard = text; });
    install(await extension(), r);
    r.drag(from, to); await flush();
    expect(r.tui.getSelectionBounds()).toMatchObject({ start: { row: 0 }, end: { row: 1 } });
    expect(r.write.mock.calls.map(([text]) => text)).toEqual(oscCopies(oracle.terminal));
    expect(clipboard).toBe('Привет 世界 😀\nстрока два');
  });

  it('R1: wide-cell partial selection and offscreen scroll contents use Pi extraction, not screen slicing', async () => {
    const lines = Array.from({ length: 24 }, (_, i) => `row${i} 世界 😀 é`);
    const oracle = await make(lines); const r = await make(lines);
    install(await extension(), r);
    for (const target of [oracle, r]) {
      target.scroll.scrollTo(10); target.draw();
      target.drag([6, 1], [12, 3]);
      expect(target.tui.getSelectionBounds()).toMatchObject({ start: { row: 11, scrollView: target.scroll }, end: { row: 13, scrollView: target.scroll } });
    }
    await flush();
    expect(oscCopies(oracle.terminal)[0]).toContain('row12 世界 😀 é');
    expect(r.write.mock.calls.map(([text]) => text)).toEqual(oscCopies(oracle.terminal));
  });

  it('R2: real Copied flash cannot precede native completion', async () => {
    const r = await make(); const pending = deferred(); r.write.mockReturnValue(pending.promise);
    install(await extension(), r); r.drag(); await flush();
    expect(r.write).toHaveBeenCalledTimes(1);
    expect(r.flash).not.toHaveBeenCalledWith('Copied!');
    pending.resolve(); await flush();
    expect(r.flash.mock.calls.filter(([text]) => text === 'Copied!')).toHaveLength(1);
    r.draw(); expect(r.terminal.output.join('')).toContain('Copied!');
  });

  it.each(['reject', 'throw'])('R2: %s is visible and text-free, has no success and permits a healthy successor', async (failure) => {
    const r = await make();
    r.write.mockImplementationOnce(() => { if (failure === 'throw') throw new Error('private Привет'); return Promise.reject(new Error('private Привет')); });
    install(await extension(), r); r.drag(); await flush();
    expect(r.write).toHaveBeenCalledTimes(1);
    expect(r.flash).not.toHaveBeenCalledWith('Copied!');
    expect(r.notify).toHaveBeenCalled();
    expect(JSON.stringify(r.notify.mock.calls)).not.toContain('Привет');
    r.drag(); await flush(); expect(r.write).toHaveBeenCalledTimes(2);
    expect(r.flash).toHaveBeenCalledWith('Copied!');
  });

  it('R2: collapsed and whitespace-only real selections do not write; nonempty control does', async () => {
    const r = await make(['    ', 'good']); install(await extension(), r);
    r.drag([0, 0], [0, 0]); r.drag([0, 0], [3, 0]); await flush();
    expect(r.write).not.toHaveBeenCalled(); expect(r.flash).not.toHaveBeenCalledWith('Copied!');
    r.drag([0, 1], [3, 1]); await flush(); expect(r.write.mock.calls[0][0]).toBe('good');
  });

  it('R3: admitted copies are serialized and snapshot B before transcript changes', async () => {
    const r = await make(['AAAA', 'BBBB']); const a = deferred(); const b = deferred();
    r.write.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise);
    install(await extension(), r); r.drag([0, 0], [3, 0]); await flush();
    r.drag([0, 1], [3, 1]); r.lines[1] = 'CHANGED'; r.draw(); await flush();
    expect(r.write.mock.calls.map(([text]) => text)).toEqual(['AAAA']);
    a.resolve(); await flush(); expect(r.write.mock.calls.map(([text]) => text)).toEqual(['AAAA', 'BBBB']);
    b.resolve(); await flush(); expect(r.flash.mock.calls.filter(([text]) => text === 'Copied!')).toHaveLength(2);
  });

  it('R3: at most eight pending copies, visible overload, no truncation, then recovery', async () => {
    const r = await make(['AAAA', 'BBBB']); const a = deferred(); r.write.mockReturnValueOnce(a.promise);
    install(await extension(), r); r.drag([0, 0], [3, 0]); await flush();
    for (let i = 0; i < 9; i++) r.drag([0, 1], [3, 1]);
    await flush(); expect(r.write).toHaveBeenCalledTimes(1); expect(r.notify).toHaveBeenCalled();
    a.resolve(); await flush();
    expect(r.write.mock.calls.map(([text]) => text)).toEqual(['AAAA', ...Array(8).fill('BBBB')]);
    r.drag([0, 0], [3, 0]); await flush(); expect(r.write).toHaveBeenCalledTimes(10);
  });

  it('R4/R7: dispose aborts active IO, discards pending work and suppresses late completion', async () => {
    const r = await make(); const pending = deferred(); r.write.mockReturnValue(pending.promise);
    const dispose = install(await extension(), r); r.drag(); r.drag(); await flush();
    expect(r.write).toHaveBeenCalledTimes(1);
    const signal = r.write.mock.calls[0][1]; expect(signal.aborted).toBe(false);
    dispose(); expect(signal.aborted).toBe(true); pending.resolve(); await flush();
    expect(r.write).toHaveBeenCalledTimes(1); expect(r.flash).not.toHaveBeenCalledWith('Copied!');
  });

  it('R5: ordinary OSC52 and pseudo-success output never acquire authority, even during a real copy', async () => {
    const r = await make(); const pending = deferred(); r.write.mockReturnValue(pending.promise);
    const originalSink = r.terminal.write; const stdout = process.stdout.write;
    install(await extension(), r);
    const untrusted = '\x1b]52;c;' + Buffer.from('ATTACK').toString('base64') + '\x07Copied!';
    r.terminal.write(untrusted); r.lines.push(untrusted); r.draw(); await flush();
    expect(r.write).not.toHaveBeenCalled();
    r.drag(); await flush(); expect(r.write).toHaveBeenCalledTimes(1);
    r.terminal.write(untrusted); r.draw(); await flush(); expect(r.write).toHaveBeenCalledTimes(1);
    expect(r.terminal.write).toBe(originalSink); expect(process.stdout.write).toBe(stdout);
    pending.resolve(); await flush(); expect(r.flash).toHaveBeenCalledWith('Copied!');
  });

  it('R5: semantic mouse copy never assigns live write or flash, even transiently', async () => {
    const r = await make(); const pending = deferred(); r.write.mockReturnValue(pending.promise);
    const guards = [guardLiveMethod(r.terminal, 'write'), guardLiveMethod(r.tui, 'flash')];
    try {
      const dispose = install(await extension(), r);
      const untrusted = '\x1b]52;c;' + Buffer.from('ATTACK').toString('base64') + '\x07Copied!';
      r.terminal.write(untrusted); await flush(); expect(r.write).not.toHaveBeenCalled();
      r.drag(); await flush();
      expect(r.write.mock.calls.map(([text]) => text)).toEqual(['Привет 世界 😀\nстрока два']);
      expect(r.flash).not.toHaveBeenCalledWith('Copied!');
      r.terminal.write(untrusted); await flush(); expect(r.write).toHaveBeenCalledTimes(1);
      pending.resolve(); await flush(); expect(r.flash).toHaveBeenCalledWith('Copied!');
      dispose();
      guards.forEach((guard) => expect(guard.attempts).not.toHaveBeenCalled());
    } finally { guards.forEach((guard) => guard.restore()); }
  });

  it('R6: active-selection Ctrl+C copies and is consumed; bare Ctrl+C/Esc/arrows/input retain focus routing', async () => {
    const r = await make(); install(await extension(), r);
    const keys = ['\x03', '\x1b', '\x1b[A', '\x1b[B', 'draft', '\x1b[200~paste\x1b[201~'];
    keys.forEach((key) => r.terminal.input(key));
    expect(r.focused.handleInput.mock.calls.map(([text]) => text)).toEqual(keys);
    r.focused.handleInput.mockClear(); r.drag([0, 0], [30, 1], false);
    expect(r.tui.getSelectionBounds()).toBeDefined();
    r.terminal.input('\x03'); await flush();
    expect(r.write.mock.calls.map(([text]) => text)).toEqual(['Привет 世界 😀\nстрока два']);
    expect(r.focused.handleInput).not.toHaveBeenCalled();
  });

  it.each(['idle', 'pending'])('R6: complete press/drag/release then Ctrl+C (%s IO) preserves draft and interrupt focus', async (state) => {
    const r = await make(); const pending = deferred();
    const input = new (await realPi()).Input(); input.setValue('untouched draft Черновик');
    const interrupt = vi.fn();
    const originalInput = input.handleInput.bind(input);
    const handleInput = vi.spyOn(input, 'handleInput').mockImplementation((key: string) => {
      if (key === '\x03') interrupt();
      originalInput(key);
    });
    r.tui.setFocus(input);
    if (state === 'pending') r.write.mockReturnValueOnce(pending.promise);
    install(await extension(), r);
    r.drag(); await flush();
    expect(r.write.mock.calls.map(([text]) => text)).toEqual(['Привет 世界 😀\nстрока два']);
    r.terminal.input('\x03'); await flush();
    expect(handleInput).not.toHaveBeenCalled(); expect(interrupt).not.toHaveBeenCalled();
    expect(input.getValue()).toBe('untouched draft Черновик');
    expect(r.tui.focusedComponent).toBe(input);
    if (state === 'pending') expect(r.write).toHaveBeenCalledTimes(1);
    pending.resolve(); await flush();
    expect(r.write.mock.calls.map(([text]) => text)).toEqual(Array(2).fill('Привет 世界 😀\nстрока два'));
  });

  it.each(['\x03', '\x1b[99;5:1u'])('R6: repeated %j presses keep the SAME fresh selection authoritative until ordinary input', async (copyKey) => {
    const r = await make(); install(await extension(), r);
    r.drag(); await flush();
    const bounds = r.tui.getSelectionBounds();
    expect(bounds).toBeDefined();
    expect(r.write).toHaveBeenCalledTimes(1);
    // No new drag, focus change, elapsed-time workaround, or synthetic copy call.
    for (let press = 0; press < 3; press++) {
      r.terminal.input(copyKey); await flush();
      expect(r.write.mock.calls.map(([text]) => text)).toEqual(Array(press + 2).fill('Привет 世界 😀\nстрока два'));
      expect(r.tui.getSelectionBounds()).toEqual(bounds);
      expect(r.focused.handleInput).not.toHaveBeenCalled();
      expect(r.tui.focusedComponent).toBe(r.focused);
    }
    r.terminal.input('draft'); r.terminal.input(copyKey); await flush();
    expect(r.focused.handleInput.mock.calls.map(([text]) => text)).toEqual(['draft', copyKey]);
    expect(r.write).toHaveBeenCalledTimes(4);
  });

  it.each(['draft', '\x1b[200~paste\x1b[201~', '\x1b'])('R6: copy then %j retires stale authority without changing input semantics', async (key) => {
    const r = await make(); install(await extension(), r);
    r.drag(); await flush(); expect(r.write).toHaveBeenCalledTimes(1);
    r.terminal.input(key); r.terminal.input('\x03'); await flush();
    expect(r.focused.handleInput.mock.calls.map(([text]) => text)).toEqual([key, '\x03']);
    expect(r.write).toHaveBeenCalledTimes(1);
    // Retirement is not permanent disablement: a fresh gesture reacquires authority.
    r.drag(); r.terminal.input('\x03'); await flush();
    expect(r.write.mock.calls.map(([text]) => text)).toEqual(Array(3).fill('Привет 世界 😀\nстрока два'));
  });

  it.each(['terminal', 'menu', 'overlay'])('R6: %s focus transfer preserves original Ctrl+C and cannot revive old copy authority', async (kind) => {
    const r = await make(); install(await extension(), r); r.drag(); await flush();
    const menu = { render: () => ['menu'], invalidate() {}, handleInput: vi.fn() };
    let overlay: ReturnType<Rig['tui']['showOverlay']> | undefined;
    if (kind === 'terminal') { r.terminal.input('\x1b[O'); r.terminal.input('\x1b[I'); }
    else if (kind === 'overlay') overlay = r.tui.showOverlay(menu);
    else r.tui.setFocus(menu);
    r.terminal.input('\x03'); await flush();
    expect((kind === 'terminal' ? r.focused : menu).handleInput).toHaveBeenCalledWith('\x03');
    expect(r.write).toHaveBeenCalledTimes(1);
    overlay?.hide(); r.tui.setFocus(r.focused); r.focused.handleInput.mockClear();
    r.terminal.input('\x03'); await flush();
    expect(r.focused.handleInput).toHaveBeenCalledWith('\x03'); expect(r.write).toHaveBeenCalledTimes(1);
  });

  it('R6: Kitty Ctrl+C release never copies; a true press with fresh selection does', async () => {
    const r = await make(); install(await extension(), r); r.drag(); await flush();
    r.terminal.input('\x1b[99;5:3u'); await flush();
    expect(r.write).toHaveBeenCalledTimes(1); expect(r.focused.handleInput).not.toHaveBeenCalled();
    r.terminal.input('\x1b[99;5:1u'); await flush();
    expect(r.write.mock.calls.map(([text]) => text)).toEqual(Array(2).fill('Привет 世界 😀\nстрока два'));
    expect(r.focused.handleInput).not.toHaveBeenCalled();
  });

  it('R7: duplicate installation cannot stack writes; disposal does not overwrite a later owner hook', async () => {
    const r = await make(); const module = await extension();
    const original = r.tui.copySelectionToClipboard;
    const dispose = install(module, r); install(await extension(), r); r.drag(); await flush();
    expect(r.write).toHaveBeenCalledTimes(1);
    const laterOwner = () => undefined; r.tui.copySelectionToClipboard = laterOwner;
    dispose(); expect(r.tui.copySelectionToClipboard).toBe(laterOwner);
    r.tui.copySelectionToClipboard = original;
  });

  it.each(['version', 'shape'])('R7: unsupported %s fails visibly without acquiring authority; supported control writes', async (kind) => {
    const r = await make(); const control = await make(); const module = await extension();
    install(module, control); control.drag(); await flush(); expect(control.write).toHaveBeenCalledTimes(1);
    const original = r.tui.copySelectionToClipboard;
    if (kind === 'shape') r.tui.copySelectionToClipboard = undefined;
    expect(() => install(module, r, { piVersion: kind === 'version' ? '0.85.0' : '0.84.1' })).not.toThrow();
    expect(r.notify).toHaveBeenCalled();
    if (kind === 'shape') r.tui.copySelectionToClipboard = original;
    r.drag(); await flush();
    expect(r.write).not.toHaveBeenCalled(); expect(oscCopies(r.terminal)).toEqual(['Привет 世界 😀\nстрока два']);
  });

  it.each([
    ['non-VC', 'darwin', {}], ['SSH', 'darwin', { ...localEnv, SSH_CONNECTION: 'fixture' }],
    ['Linux', 'linux', localEnv],
  ])('R7: %s remains Pi-native, with local VC positive control', async (_label, platform, env) => {
    const r = await make(); const control = await make(); const module = await extension();
    install(module, control); control.drag(); await flush(); expect(control.write).toHaveBeenCalledTimes(1);
    install(module, r, { platform: platform as string, env: env as Record<string, string> }); r.drag(); await flush();
    expect(r.write).not.toHaveBeenCalled(); expect(oscCopies(r.terminal)).toEqual(['Привет 世界 😀\nстрока два']);
  });
});

it('fixture control: widget replacement/removal disposes hooks, including an immediately cleared probe', async () => {
  const r = await make(); const ui = await widgetUI(r);
  const original = r.tui.copySelectionToClipboard;
  const hook = vi.fn(); const dispose = vi.fn(() => { r.tui.copySelectionToClipboard = original; });
  const factory = vi.fn((tui) => {
    expect(tui).toBe(r.tui); tui.copySelectionToClipboard = hook;
    return { render: () => [], invalidate() {}, dispose };
  });
  ui.setWidget('probe', factory);
  r.drag(); expect(hook).toHaveBeenCalledTimes(1);
  ui.setWidget('probe', () => {
    expect(dispose).toHaveBeenCalledTimes(1);
    expect(r.tui.copySelectionToClipboard).toBe(original);
    return factory(r.tui);
  }, { placement: 'belowEditor' });
  ui.setWidget('probe', undefined);
  expect(dispose).toHaveBeenCalledTimes(2);
  ui.setWidget('probe', factory); ui.setWidget('probe', undefined);
  expect(dispose).toHaveBeenCalledTimes(3);
  ui.setWidget('probe', undefined); expect(dispose).toHaveBeenCalledTimes(3);
  r.drag(); expect(hook).toHaveBeenCalledTimes(1);
  expect(oscCopies(r.terminal)).toEqual(['Привет 世界 😀\nстрока два']);
});

it.each(['cli', 'desktop'])('R7: production defaults without clipboardIO reach native spawn in %s lifecycle', async (mode) => {
  const r = await make();
  const env = mode === 'desktop' ? { ...localEnv, VC_DESKTOP_CHAT_ID: '12345678-1234-4234-8234-123456789abc', SSH_CONNECTION: 'inherited' } : localEnv;
  const bytes: Buffer[] = [];
  const child = Object.assign(new EventEmitter(), { stdin: new PassThrough(), stderr: new PassThrough(), kill: vi.fn() });
  child.stdin.on('data', (data) => bytes.push(Buffer.from(data)));
  const spawn = vi.fn(() => child);
  const module = await extension(env, spawn);
  const handlers = new Map<string, LifecycleHandler[]>();
  const pi = { on: (name: string, handler: LifecycleHandler) => handlers.set(name, [...(handlers.get(name) ?? []), handler]), registerProvider: vi.fn() };
  const ui = await widgetUI(r); const { setWidget } = ui;
  const ctx = { mode: 'tui', hasUI: true, ui };
  await module.default(pi); // Deliberately no second argument: only node subprocess IO is substituted.
  expect(pi.registerProvider).toHaveBeenCalled();
  expect(handlers.has('session_start'), 'R7: default managed extension never registers fullscreen clipboard lifecycle').toBe(true);
  try {
    for (const handler of handlers.get('session_start') ?? []) await handler({ reason: 'startup' }, ctx);
    expect(setWidget).toHaveBeenCalled();
    r.drag(); await flush();
    expect(spawn).toHaveBeenCalledWith('/usr/bin/pbcopy', expect.any(Array), expect.objectContaining({ stdio: ['pipe', 'ignore', 'pipe'] }));
    expect(Buffer.concat(bytes).toString('utf8')).toBe('Привет 世界 😀\nстрока два');
    expect(r.flash).not.toHaveBeenCalledWith('Copied!');
    child.emit('close', 0, null); await flush(); expect(r.flash).toHaveBeenCalledWith('Copied!');
    expect(ctx.ui.setEditorComponent).not.toHaveBeenCalled();
  } finally {
    for (const handler of handlers.get('session_shutdown') ?? []) await handler({ reason: 'quit' }, ctx);
  }
  r.drag(); await flush(); expect(spawn).toHaveBeenCalledTimes(1);
});

it.each(['cli', 'desktop'])('R7: real default factory %s installs through lifecycle without provider/account coupling', async (mode) => {
  const r = await make();
  const env = mode === 'desktop' ? { ...localEnv, VC_DESKTOP_CHAT_ID: '12345678-1234-4234-8234-123456789abc', SSH_CONNECTION: 'inherited' } : localEnv;
  const module = await extension(env);
  const handlers = new Map<string, LifecycleHandler[]>();
  const pi = { on: (name: string, handler: LifecycleHandler) => handlers.set(name, [...(handlers.get(name) ?? []), handler]), registerProvider: vi.fn() };
  const ui = await widgetUI(r);
  const ctx = { mode: 'tui', hasUI: true, ui };
  await module.default(pi, { clipboardIO: { platform: 'darwin', env, piVersion: '0.84.1', writeText: r.write } });
  expect(pi.registerProvider).toHaveBeenCalledWith('void-codex', expect.objectContaining({ models: expect.arrayContaining([expect.objectContaining({ id: 'gpt-5.6-terra' })]) }));
  expect(handlers.has('session_start'), 'R7: default managed extension never registers fullscreen clipboard lifecycle').toBe(true);
  for (const handler of handlers.get('session_start') ?? []) await handler({ reason: 'startup' }, ctx);
  r.drag(); await flush();
  expect(r.write.mock.calls.map(([text]) => text)).toEqual(['Привет 世界 😀\nстрока два']);
  expect(ui.setEditorComponent).not.toHaveBeenCalled();
  for (const handler of handlers.get('session_shutdown') ?? []) await handler({ reason: 'reload' }, ctx);
  r.write.mockClear(); r.terminal.output.length = 0; r.drag(); await flush(); expect(r.write).not.toHaveBeenCalled();
  expect(oscCopies(r.terminal)).toEqual(['Привет 世界 😀\nстрока два']);
  // Same default registration reached the positive boundary above; RPC has UI but is not TUI.
  ui.setWidget.mockClear();
  for (const handler of handlers.get('session_start') ?? []) await handler({ reason: 'reload' }, { ...ctx, mode: 'rpc' });
  r.drag(); await flush(); expect(r.write).not.toHaveBeenCalled(); expect(ui.setWidget).not.toHaveBeenCalled();
  for (const handler of handlers.get('session_start') ?? []) await handler({ reason: 'reload' }, ctx);
  r.drag(); await flush(); expect(r.write).toHaveBeenCalledTimes(1);
  for (const handler of handlers.get('session_shutdown') ?? []) await handler({ reason: 'quit' }, ctx);
});
