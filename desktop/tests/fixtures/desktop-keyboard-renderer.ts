import { expect, vi } from 'vitest';

type Listener = { fn: (event: KeyboardEvent) => unknown; capture: boolean };
export class Element {
  hidden = false; disabled = false; textContent = ''; value = ''; className = ''; title = '';
  dataset: Record<string, string> = {}; style = {}; children: Element[] = []; parent: Element | null = null;
  attributes = new Map<string, string>(); listeners = new Map<string, Listener[]>();
  classList = { add() {}, remove() {}, toggle() {} };
  constructor(public tagName = 'DIV') {}
  addEventListener(type: string, fn: Listener['fn'], options?: boolean | AddEventListenerOptions): void {
    this.listeners.set(type, [...this.listeners.get(type) ?? [], { fn, capture: options === true || (typeof options === 'object' && options.capture === true) }]);
  }
  removeEventListener(): void {}
  append(...nodes: Element[]): void { nodes.forEach(node => { node.parent = this; this.children.push(node); }); }
  replaceChildren(...nodes: Element[]): void { this.children.forEach(node => { node.parent = null; }); this.children = []; this.append(...nodes); }
  setAttribute(name: string, value: string): void { this.attributes.set(name, value); }
  getAttribute(name: string): string | null { return this.attributes.get(name) ?? null; }
  contains(node: unknown): boolean { return node === this || this.children.some(child => child.contains(node)); }
  focus(): void { (document as unknown as { activeElement: Element }).activeElement = this; }
  setSelectionRange(): void {}
  get isConnected(): boolean { return this.parent !== null; }
  remove(): void { if (this.parent) this.parent.children = this.parent.children.filter(child => child !== this); this.parent = null; }
  closest(selector: string): Element | null { return selector.startsWith('.') && this.className.split(' ').includes(selector.slice(1)) ? this : this.parent?.closest(selector) ?? null; }
  querySelectorAll(): Element[] { return []; }
  click(): void { dispatch(this, 'click', {}); }
}
export type KeyInit = KeyboardEventInit & { altGraph?: boolean };
export function dispatch(target: Element, type: string, init: KeyInit): KeyboardEvent {
  let stopped = false; let immediate = false;
  const event = { type, key: '', code: '', ctrlKey: false, shiftKey: false, altKey: false, metaKey: false, repeat: false, isComposing: false,
    ...init, target, defaultPrevented: false, preventDefault() { this.defaultPrevented = true; },
    stopPropagation() { stopped = true; }, stopImmediatePropagation() { stopped = true; immediate = true; },
    getModifierState(name: string) { return name === 'AltGraph' && (init.altGraph ?? (this.ctrlKey && this.altKey)); },
  } as unknown as KeyboardEvent;
  const ancestors: Element[] = []; for (let node = target.parent; node; node = node.parent) ancestors.push(node);
  const invoke = (node: Element, capture: boolean): void => {
    for (const listener of node.listeners.get(type) ?? []) { if (listener.capture === capture) listener.fn(event); if (immediate) break; }
  };
  for (const node of [...ancestors].reverse()) { invoke(node, true); if (stopped) return event; }
  invoke(target, true); if (!immediate) invoke(target, false);
  if (!stopped) for (const node of ancestors) { invoke(node, false); if (stopped) break; }
  return event;
}
export function deferred<T>() {
  let resolve!: (value: T) => void; let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
// A macrotask barrier drains promise continuations without arbitrary sleeps or guessed microtask counts.
export async function settle(): Promise<void> { await new Promise<void>(resolve => setImmediate(resolve)); }
export type Tab = { id: string; title: string; location: 'active' | 'recent'; state: 'sleeping' };
export type View = { workspace: { path: string; selectedId: string | null; tabs: Tab[] } | null; recoveryPath: string | null };
export function snapshot(selectedId: string | null = 'a', active = ['a', 'b', 'c']): View {
  const tab = (id: string, location: Tab['location']): Tab => ({ id, title: id.toUpperCase(), location, state: 'sleeping' });
  return { workspace: { path: '/synthetic-no-files', selectedId, tabs: [tab('recent', 'recent'), ...active.map(id => tab(id, 'active'))] }, recoveryPath: null };
}
export async function renderer(initial = snapshot(), platform = 'Windows') {
  vi.resetModules();
  const doc = new Element('DOCUMENT'); const body = new Element('BODY'); doc.append(body);
  const nodes = new Map<string, Element>();
  const node = (id: string): Element => { if (!nodes.has(id)) { const item = new Element(); nodes.set(id, item); body.append(item); } return nodes.get(id)!; };
  Object.assign(doc, { body, activeElement: body, querySelector: node, createElement: (tag: string) => new Element(tag.toUpperCase()) });
  vi.stubGlobal('document', doc); vi.stubGlobal('HTMLElement', Element); vi.stubGlobal('HTMLButtonElement', Element); vi.stubGlobal('HTMLInputElement', Element);
  vi.stubGlobal('navigator', { userAgent: platform, platform }); vi.stubGlobal('location', new URL('https://synthetic.invalid/'));
  const frames: FrameRequestCallback[] = [];
  vi.stubGlobal('requestAnimationFrame', (fn: FrameRequestCallback) => { frames.push(fn); return frames.length; });
  vi.stubGlobal('cancelAnimationFrame', () => undefined);
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} }); vi.stubGlobal('matchMedia', () => ({ matches: false }));
  let server = structuredClone(initial);
  const select = vi.fn(async (id: string): Promise<View> => { if (server.workspace) server.workspace.selectedId = id; return structuredClone(server); });
  const start = vi.fn(async (request: unknown) => { void request; return { showSharedFilesWarning: false }; });
  const input = vi.fn(async (request: unknown) => { void request; });
  const rename = vi.fn(); const resume = vi.fn(); const newChat = vi.fn();
  const bridge = { appVersion: async () => 'test', auth: { status: async () => ({ ok: true, status: { state: 'ready' } }), onLoginEvent() {} },
    workspace: { load: async () => structuredClone(initial), select, rename, resume, newChat },
    start, input, resize: async () => undefined, stop: async () => undefined,
    clipboard: { read: vi.fn(async () => ({ kind: 'empty' })), write: vi.fn(async () => undefined) },
    lifecycleStatus: async () => ({ status: { state: 'running' } }), onOutput: () => () => undefined, onExit: () => () => undefined, onStatus: () => () => undefined,
  };
  const win = new Element('WINDOW'); win.append(doc); Object.assign(win, { voidTerminal: bridge }); vi.stubGlobal('window', win);
  const { Terminal } = await import('@xterm/xterm'); const { FitAddon } = await import('@xterm/addon-fit');
  const product = await import('../../src/renderer/terminal-stack');
  const terminals: Array<{ terminal: InstanceType<typeof Terminal>; textarea: Element }> = [];
  const handlers = new WeakMap<InstanceType<typeof Terminal>, (event: KeyboardEvent) => boolean>();
  const originalAttach = Terminal.prototype.attachCustomKeyEventHandler;
  vi.spyOn(Terminal.prototype, 'attachCustomKeyEventHandler').mockImplementation(function(handler) { handlers.set(this, handler); originalAttach.call(this, handler); });
  vi.spyOn(Terminal.prototype, 'open').mockImplementation(function(host) {
    const textarea = new Element('TEXTAREA'); textarea.className = 'xterm-helper-textarea'; (host as unknown as Element).append(textarea);
    terminals.push({ terminal: this, textarea });
    for (const type of ['keydown', 'keyup']) textarea.addEventListener(type, event => {
      // Mocked routing only: open and keyboard parsing are replaced, not native xterm keys.
      // Actual entry, Terminal.input and onData remain real; capture must precede this target.
      if (handlers.get(this)?.(event) === false || event.defaultPrevented) return;
      if (type === 'keydown' && event.key === 'Tab') this.input('\t', true);
    });
  });
  vi.spyOn(Terminal.prototype, 'focus').mockImplementation(function() { terminals.find(item => item.terminal === this)?.textarea.focus(); });
  vi.spyOn(FitAddon.prototype, 'fit').mockImplementation(() => undefined);
  vi.spyOn(product, 'activateProductRenderer').mockImplementation(() => undefined);
  await import('../../src/renderer/index'); await settle();
  expect(node('#folder').textContent, 'actual entry completed workspace load').toBe(initial.recoveryPath ? 'Workspace unavailable' : initial.workspace?.path ?? 'No folder selected');
  const key = (init: KeyInit = {}, type = 'keydown', target = (doc as unknown as { activeElement: Element }).activeElement): KeyboardEvent => dispatch(target, type, { key: 'Tab', code: 'Tab', ctrlKey: true, ...init });
  return { node, doc, key, select, start, input, rename, resume, newChat, terminals, bridge,
    setServer(view: View) { server = structuredClone(view); },
    frame() { frames.splice(0).forEach(fn => fn(0)); },
    dispose() { terminals.forEach(item => item.terminal.dispose()); },
  };
}
