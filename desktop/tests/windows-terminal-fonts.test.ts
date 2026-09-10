import { afterEach, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import ts from 'typescript';

const tick = async () => { for (let i = 0; i < 30; i++) await Promise.resolve(); };
function deferred() {
  let resolve!: () => void; let reject!: (reason: Error) => void;
  const promise = new Promise<void>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function faces() {
  return ['400', '700'].flatMap(weight => ['U+0000-00FF', 'U+0400-045F', 'U+0460-052F'].map((unicodeRange, i) => {
    const pending = deferred();
    const face = { family: 'JetBrains Mono', style: 'normal', weight, unicodeRange, status: i === 0 ? 'loaded' : 'unloaded', ...pending,
      load: vi.fn(async () => { await pending.promise; face.status = 'loaded'; return face; }) };
    if (i === 0) pending.resolve();
    return face;
  }));
}
type Face = ReturnType<typeof faces>[number];
function fontSet(owned: Face[]) {
  const unrelated = { ...faces()[1], family: 'Unrelated UI' };
  const unrelatedFailed = { ...faces()[1], family: 'Failed UI', status: 'error' };
  const italic = { ...faces()[1], style: 'italic' };
  const all = [...owned, italic, unrelatedFailed, unrelated];
  return Object.assign(new Set(all), {
    ready: new Promise(() => undefined), // waiting for all document fonts is a bug
    check: vi.fn(() => true), // default Latin check must not establish readiness
    load: vi.fn(async () => { throw new Error('Use declared owned faces, not default Latin load'); }),
  });
}
async function setup(platform = 'Win32', owned = faces(), api = true) {
  vi.resetModules(); vi.useFakeTimers();
  vi.stubGlobal('location', new URL('https://font.test/'));
  vi.stubGlobal('navigator', { platform, userAgent: platform === 'Win32' ? 'Windows NT 10.0' : platform });
  const fonts = fontSet(owned);
  vi.stubGlobal('document', { fonts: api ? fonts : undefined });
  const warning = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
  const product = await import('../src/renderer/terminal-stack');
  const prepare = () => {
    const fn = Reflect.get(product, 'prepareTerminalFonts');
    expect(fn, 'F1/F3: export prepareTerminalFonts (see fixtures/windows-terminal-fonts-plan.md)').toBeTypeOf('function');
    return (fn as () => Promise<{ status: 'loaded' | 'degraded' }>)();
  };
  return { product, prepare, owned, fonts, warning };
}
afterEach(() => { vi.clearAllTimers(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it('F1: Latin-ready is not Cyrillic/bold/ext-ready; unrelated hung font is ignored; cache and timer clean up', async () => {
  const s = await setup();
  let settled = false; const pending = s.prepare().then(result => { settled = true; return result; });
  await tick(); expect(settled).toBe(false);
  for (const face of s.owned.filter(f => f.status !== 'loaded')) expect(face.load).toHaveBeenCalledTimes(1);
  s.owned.filter(f => f.unicodeRange !== 'U+0460-052F').forEach(f => f.resolve());
  await tick(); expect(settled).toBe(false);
  s.owned.filter(f => f.weight === '400').forEach(f => f.resolve());
  await tick(); expect(settled).toBe(false);
  s.owned.forEach(f => f.resolve());
  expect(await pending).toMatchObject({ status: 'loaded' });
  const counts = s.owned.map(f => f.load.mock.calls.length);
  expect(await s.prepare()).toMatchObject({ status: 'loaded' });
  expect(s.owned.map(f => f.load.mock.calls.length)).toEqual(counts);
  for (const ignored of [...s.fonts].slice(s.owned.length)) expect(ignored.load).not.toHaveBeenCalled();
  expect(vi.getTimerCount()).toBe(0); expect(s.warning).not.toHaveBeenCalled();
});

it.each(['API', 'faces', 'bold'])('F3: absent %s explicitly degrades without hanging', async missing => {
  const owned = missing === 'faces' ? [] : faces().filter(f => missing !== 'bold' || f.weight !== '700');
  owned.forEach(f => f.resolve());
  const s = await setup('Win32', owned, missing !== 'API');
  expect(await s.prepare()).toMatchObject({ status: 'degraded' });
  const terminal = s.product.createProductTerminal().terminal;
  expect(terminal.options.fontFamily).toBe('monospace'); terminal.dispose();
  expect(s.warning).toHaveBeenCalled(); expect(vi.getTimerCount()).toBe(0);
});

it('F3: owned load error chooses stable fallback, warns and clears timeout', async () => {
  const s = await setup(); const pending = s.prepare(); await tick();
  s.owned[1].reject(new Error('synthetic font failure')); s.owned.filter((_, i) => i !== 1).forEach(f => f.resolve());
  expect(await pending).toMatchObject({ status: 'degraded' });
  const t = s.product.createProductTerminal().terminal;
  expect(t.options.fontFamily).toBe('monospace'); t.dispose();
  expect(s.warning).toHaveBeenCalled(); expect(vi.getTimerCount()).toBe(0);
});

it.each(['resolve', 'reject'] as const)('F3: exact 5000ms bound; late %s cannot swap fallback or reload', async late => {
  const s = await setup(); let settled = false;
  const pending = s.prepare().then(r => { settled = true; return r; });
  await vi.advanceTimersByTimeAsync(4999); expect(settled).toBe(false);
  await vi.advanceTimersByTimeAsync(1); expect(await pending).toMatchObject({ status: 'degraded' });
  const first = s.product.createProductTerminal().terminal;
  expect(first.options.fontFamily).toBe('monospace');
  const counts = s.owned.map(f => f.load.mock.calls.length);
  s.owned.forEach(f => late === 'resolve' ? f.resolve() : f.reject(new Error('late synthetic failure')));
  await tick(); expect(await s.prepare()).toMatchObject({ status: 'degraded' });
  const second = s.product.createProductTerminal().terminal;
  expect(second.options.fontFamily).toBe(first.options.fontFamily);
  expect(s.owned.map(f => f.load.mock.calls.length)).toEqual(counts);
  expect(s.warning).toHaveBeenCalledTimes(1); expect(vi.getTimerCount()).toBe(0);
  first.dispose(); second.dispose();
});

it.each([['Win32', 1], ['MacIntel', 0], ['Linux x86_64', 0]] as const)('F2: real xterm options on %s', async (platform, spacing) => {
  const s = await setup(platform);
  // Keep these independent controls runnable before the API exists; after it is
  // introduced, inspect the actual successful document choice, not pending state.
  if (typeof Reflect.get(s.product, 'prepareTerminalFonts') === 'function') {
    s.owned.forEach(f => f.resolve()); await s.prepare();
  }
  const terminal = s.product.createProductTerminal().terminal;
  expect(terminal.options).toMatchObject({ fontFamily: '"JetBrains Mono", monospace', fontSize: 14, fontWeight: '400', fontWeightBold: '700', lineHeight: 1.15, letterSpacing: spacing, scrollback: 10_000 });
  terminal.dispose();
});

it('F5: production evidence does not label Latin-only readiness as JBM-loaded', async () => {
  const s = await setup();
  if (typeof Reflect.get(s.product, 'prepareTerminalFonts') === 'function') {
    s.owned.forEach(f => f.resolve()); await s.prepare();
  }
  const source = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');
  const ast = ts.createSourceFile('index.ts', source, ts.ScriptTarget.Latest, true);
  const declarations: ts.FunctionDeclaration[] = [];
  const visit = (node: ts.Node) => {
    if (ts.isFunctionDeclaration(node) && node.name?.text === 'integrationFacts') declarations.push(node);
    ts.forEachChild(node, visit);
  };
  visit(ast);
  expect(declarations, 'extract exactly one actual production evidence function, including inside initialization wrappers').toHaveLength(1);
  const [declaration] = declarations;
  const js = ts.transpileModule(declaration!.getText(ast), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
  const check = vi.fn((_font: string, sample = ' ') => !/[\u0400-\u052f]/u.test(sample));
  const element = { style: {}, remove() {}, getContext: () => ({ measureText: () => ({ width: 10 }) }) };
  const doc = { createElement: () => element, body: { append() {} }, styleSheets: [], fonts: { check } };
  const { Terminal } = await import('@xterm/xterm');
  const run = new Function('document', 'getComputedStyle', 'Terminal', 'TERMINAL_THEME', 'TERMINAL_OPTIONS', `${js}; return integrationFacts;`)(doc, () => ({ fontFamily: 'JetBrains Mono' }), Terminal, s.product.TERMINAL_THEME, s.product.TERMINAL_OPTIONS);
  const terminal = s.product.createProductTerminal().terminal;
  expect(run({ terminal, renderer: 'dom' }).font.loaded, 'Latin ready, Cyrillic not ready').toBe(false);
  // A successful report must also have explicitly checked the bold Cyrillic sample.
  check.mockImplementation(() => true);
  expect(run({ terminal, renderer: 'dom' }).font.loaded).toBe(true);
  expect(check.mock.calls.some(([font, sample]) => /700/.test(font) && /[\u0400-\u052f]/u.test(sample ?? ''))).toBe(true);
  terminal.dispose();
});

// Execute the ENTIRE actual index module (Vitest's TS compiler), no copied launch,
// mocked stack or fake Terminal. Only DOM/bridge and native rendering boundaries
// are replaced. This catches an unused helper and calls made after open/fit/start.
class ElementStub {
  hidden = false; disabled = false; textContent = ''; dataset = {}; style = {}; children: ElementStub[] = [];
  classList = { add() {}, remove() {}, toggle() {} };
  listeners = new Map<string, (() => unknown)[]>();
  addEventListener(name: string, fn: () => unknown) { this.listeners.set(name, [...this.listeners.get(name) ?? [], fn]); }
  async click() { await Promise.all((this.listeners.get('click') ?? []).map(fn => fn())); }
  append(...elements: ElementStub[]) { this.children.push(...elements); }
  replaceChildren(...elements: ElementStub[]) { this.children = elements; }
  setAttribute() {} contains() { return false; } focus() {} remove() {} closest() { return null; }
  querySelectorAll() { return []; }
}
async function startup(probe: boolean, earlyGesture?: { holdLoad: boolean }) {
  const s = await setup();
  const nodes = new Map<string, ElementStub>();
  const node = (id: string) => { if (!nodes.has(id)) nodes.set(id, new ElementStub()); return nodes.get(id)!; };
  Object.assign(document, { querySelector: node, querySelectorAll: () => [], createElement: () => new ElementStub(), body: new ElementStub(), documentElement: new ElementStub(), addEventListener() {} });
  vi.stubGlobal('HTMLElement', ElementStub); vi.stubGlobal('HTMLButtonElement', ElementStub);
  vi.stubGlobal('requestAnimationFrame', () => 1); vi.stubGlobal('cancelAnimationFrame', () => undefined);
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener() {} }));
  const tab = { id: 'owned-chat', title: 'Synthetic', location: 'active', state: 'sleeping' };
  const view = { workspace: { path: '/synthetic', selectedId: tab.id, tabs: [tab] }, recoveryPath: null };
  // Keep IPC snapshots distinct: choosing must never mutate a pending load result.
  const loadGate = deferred();
  if (!earlyGesture?.holdLoad) loadGate.resolve();
  const oldLoaded = { workspace: earlyGesture?.holdLoad ? { path: '/old-loaded', selectedId: null, tabs: [] } : null, recoveryPath: null };
  const chosen = { workspace: { path: '/fresh-chosen', selectedId: null, tabs: [] }, recoveryPath: null };
  const created = { workspace: { path: '/fresh-chosen', selectedId: 'fresh-chat', tabs: [{ id: 'fresh-chat', title: 'Fresh', location: 'active', state: 'sleeping' }] }, recoveryPath: null };
  const choose = vi.fn(async () => structuredClone(chosen));
  const newChat = vi.fn(async () => ({ view: earlyGesture ? structuredClone(created) : view }));
  if (earlyGesture) node('#new-chat').hidden = true; // actual initial index.html
  const start = vi.fn(async () => ({ showSharedFilesWarning: false }));
  const bridge = { appVersion: async () => 'test', auth: { status: async () => ({ state: 'ready' }), onLoginEvent: () => () => undefined },
    workspace: { load: async () => { await loadGate.promise; return earlyGesture ? structuredClone(oldLoaded) : view; }, choose, newChat, select: async () => view, close: async () => { view.workspace.tabs = []; return view; } },
    start, resize: async () => undefined, input: async () => undefined, stop: async () => undefined,
    lifecycleStatus: async () => ({ status: { state: 'running' } }), onOutput: () => () => undefined, onExit: () => () => undefined, onStatus: () => () => undefined };
  vi.stubGlobal('window', { voidTerminal: bridge, addEventListener() {} });
  if (probe) vi.stubGlobal('location', new URL('https://font.test/?productionTerminalProbe=1'));
  const { Terminal } = await import('@xterm/xterm');
  const { FitAddon } = await import('@xterm/addon-fit');
  const open = vi.spyOn(Terminal.prototype, 'open').mockImplementation(() => undefined);
  vi.spyOn(Terminal.prototype, 'focus').mockImplementation(() => undefined);
  const write = vi.spyOn(Terminal.prototype, 'write').mockImplementation(() => undefined);
  const fit = vi.spyOn(FitAddon.prototype, 'fit').mockImplementation(() => undefined);
  const activate = vi.spyOn(s.product, 'activateProductRenderer').mockImplementation(() => undefined);
  await import('../src/renderer/index'); await tick();
  return { ...s, node, view, open, write, fit, activate, start, choose, newChat, loadGate };
}
it.each([false, true])('F5: actual index startup gates all terminal consumers (probe=%s)', async probe => {
  const s = await startup(probe);
  expect(s.open, 'index opened before Cyrillic/bold/ext readiness').not.toHaveBeenCalled();
  expect(s.fit).not.toHaveBeenCalled(); expect(s.activate).not.toHaveBeenCalled(); expect(s.write).not.toHaveBeenCalled(); expect(s.start).not.toHaveBeenCalled();
  s.owned.forEach(f => f.resolve()); await tick();
  expect(s.open).toHaveBeenCalledTimes(1); expect(s.start).toHaveBeenCalledTimes(1);
  expect([...s.fonts].at(-1)!.load).not.toHaveBeenCalled();
});
it('F4: actual UI cannot resurrect a removed pending chat or duplicate a repeated create', async () => {
  const s = await startup(false);
  // Either startup has not attached handlers yet, or launch must revalidate after waiting.
  s.view.workspace.tabs = [];
  s.view.workspace.path = '/synthetic-replacement';
  s.owned.forEach(f => f.resolve()); await tick();
  expect(s.open).not.toHaveBeenCalled(); expect(s.start).not.toHaveBeenCalled();
  s.view.workspace.tabs = [{ id: 'owned-chat', title: 'Synthetic', location: 'active', state: 'sleeping' }];
  await Promise.all([s.node('#new-chat').click(), s.node('#new-chat').click()]); await tick();
  expect(s.open).toHaveBeenCalledTimes(1); expect(s.start).toHaveBeenCalledTimes(1);
});

// A real initial gesture, not a programmatic click on the hidden New Chat button.
it.each([false, true])('F5: early Choose Folder then visible New Chat respects fonts and fresh state (pending load=%s)', async holdLoad => {
  const s = await startup(false, { holdLoad });
  expect(s.node('#choose').hidden).toBe(false);
  expect(s.node('#choose').disabled).toBe(false);
  const choosing = s.node('#choose').click();
  await tick();
  let creating: Promise<void> | undefined;
  if (!s.node('#new-chat').hidden && !s.node('#new-chat').disabled) {
    expect(s.choose).toHaveBeenCalledTimes(1);
    expect(s.node('#folder').textContent).toBe('/fresh-chosen');
    creating = s.node('#new-chat').click(); // do not await a correctly gated handler
    await tick();
  }
  expect.soft(s.open, 'early visible New Chat opened before owned Cyrillic/bold/ext fonts').not.toHaveBeenCalled();
  expect.soft(s.fit).not.toHaveBeenCalled();
  expect.soft(s.activate).not.toHaveBeenCalled();
  expect.soft(s.start).not.toHaveBeenCalled();

  // Deliver the independent stale snapshot only after the legitimate gestures.
  s.loadGate.resolve();
  await tick();
  s.owned.forEach(f => f.resolve());
  await tick(); await choosing; await creating; await tick();
  if (s.choose.mock.calls.length === 0) {
    // Initialization-before-handlers may safely ignore the early gesture.
    expect(s.newChat).not.toHaveBeenCalled();
    expect(s.open).not.toHaveBeenCalled(); expect(s.start).not.toHaveBeenCalled();
    expect(s.node('#folder').textContent).toBe(holdLoad ? '/old-loaded' : 'No folder selected');
    return;
  }
  expect(s.choose).toHaveBeenCalledTimes(1);
  expect(s.node('#folder').textContent, 'pending startup snapshot clobbered the freshly chosen workspace').toBe('/fresh-chosen');
  expect(s.node('#new-chat').hidden).toBe(false);
  if (!creating) {
    expect(s.node('#new-chat').disabled).toBe(false);
    await s.node('#new-chat').click(); await tick();
  }
  expect(s.newChat).toHaveBeenCalledTimes(1);
  expect(s.open).toHaveBeenCalledTimes(1); expect(s.start).toHaveBeenCalledTimes(1);
  expect(s.start.mock.calls[0]).toEqual([{ sessionId: 'fresh-chat', cwd: '/fresh-chosen', mode: 'create' }]);
  expect(s.node('#folder').textContent).toBe('/fresh-chosen');
  expect(s.node('#tabs').children).toHaveLength(1);
  expect(s.node('#terminals').children.filter(child => !child.hidden)).toHaveLength(1);
});
