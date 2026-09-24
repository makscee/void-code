// Regression witnesses for Pi 0.87.1 renderer replacement and viewport-listener ordering.
// Production clipboard IO is injected; renderer, parser, widget, and editor behavior are pinned Pi.
import { pathToFileURL } from 'node:url';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { editorRig } from './fixtures/pi-editor-keys';
import {
  actualReference,
  extension,
  flush,
  install,
  interactiveFile,
  localEnv,
  realPi,
  rig,
  type ComponentView,
  type LifecycleHandler,
  type Rig,
  type TuiView,
  type WidgetContent,
  type WidgetOptions,
} from './fixtures/pi-fullscreen-clipboard';

const rigs: Rig[] = [];
const selectedText = 'Привет 世界 😀\nстрока два';
const adapterHooks = ['copySelectionToClipboard', 'handleSelectionMouseEvent', 'handleViewportInput', 'setFocus', 'showOverlay'] as const;

type Renderer = TuiView & {
  mode: 'fullscreen' | 'regular';
  children: ComponentView[];
  inputListeners: Set<(data: string) => unknown>;
  addChild(component: ComponentView): void;
  clear(): void;
  getFocusedComponent(): ComponentView | null;
  getShowHardwareCursor(): boolean;
  getClearOnShrink(): boolean;
  setClearOnShrink(value: boolean): void;
  hasOverlayEntries: boolean;
  onDebug?: () => void;
  invalidate(): void;
};
type WidgetContainer = ComponentView & {
  children: ComponentView[];
  addChild(component: ComponentView): void;
  clear(): void;
};
type ModeReceiver = {
  renderer: Renderer;
  ui: TuiView;
  options: { tuiMode: 'fullscreen' | 'regular' };
  mainScreenRenderState?: unknown;
  fullscreenLayoutRoot: ComponentView;
  extensionWidgetsAbove: Map<string, ComponentView>;
  extensionWidgetsBelow: Map<string, ComponentView>;
  widgetContainerAbove: WidgetContainer;
  widgetContainerBelow: WidgetContainer;
  themeController: { rebindTui(): void };
  extensionTerminalInputSubscriptions: Set<unknown>;
  settingsManager: { getShowTerminalProgress(): boolean };
  session: { isStreaming: boolean; isCompacting: boolean };
  mountInteractiveTui(tui: Renderer, components: ComponentView[]): void;
  renderWidgets(): void;
  renderWidgetContainer(container: WidgetContainer, widgets: Map<string, ComponentView>, spacerWhenEmpty: boolean, leadingSpacer: boolean): void;
  rebindExtensionTerminalInputListeners(): void;
};
type InteractivePrototype = {
  switchTuiMode(this: ModeReceiver, mode: 'fullscreen' | 'regular', restoreProgress?: boolean, startRenderer?: boolean): boolean;
  mountInteractiveTui(this: ModeReceiver, tui: Renderer, components: ComponentView[]): void;
  setExtensionWidget(this: ModeReceiver, key: string, content: WidgetContent, options?: WidgetOptions): void;
  renderWidgets(this: ModeReceiver): void;
  renderWidgetContainer(this: ModeReceiver, container: WidgetContainer, widgets: Map<string, ComponentView>, spacerWhenEmpty: boolean, leadingSpacer: boolean): void;
  rebindExtensionTerminalInputListeners(this: ModeReceiver): void;
};

beforeEach(() => vi.useFakeTimers());
afterEach(() => {
  rigs.splice(0).reverse().forEach((entry) => entry.close());
  vi.useRealTimers();
});

function hookValues(tui: TuiView): unknown[] {
  return adapterHooks.map((key) => Reflect.get(tui, key));
}

it('renderer switch: the persistent actual widget retires old hooks and equips the next fullscreen renderer exactly once', async () => {
  const r = await rig();
  rigs.push(r);
  const pi = await realPi();
  const interactive = await import(/* @vite-ignore */ pathToFileURL(interactiveFile).href);
  const prototype = interactive.InteractiveMode.prototype as unknown as InteractivePrototype;
  const above = new pi.Container() as WidgetContainer;
  const below = new pi.Container() as WidgetContainer;
  const fullscreenLayoutRoot = new pi.VStack([
    { component: r.scroll, basis: 0, grow: 1, shrink: 1, minSize: 1 },
    { component: above, basis: 'auto', grow: 0, shrink: 1, minSize: 0 },
    { component: below, basis: 'auto', grow: 0, shrink: 1, minSize: 0 },
  ]);
  const mode = {} as ModeReceiver;
  mode.renderer = r.tui as Renderer;
  mode.ui = actualReference(() => mode.renderer);
  mode.options = { tuiMode: 'fullscreen' };
  mode.fullscreenLayoutRoot = fullscreenLayoutRoot;
  mode.extensionWidgetsAbove = new Map();
  mode.extensionWidgetsBelow = new Map();
  mode.widgetContainerAbove = above;
  mode.widgetContainerBelow = below;
  mode.themeController = { rebindTui: vi.fn() };
  mode.extensionTerminalInputSubscriptions = new Set();
  mode.settingsManager = { getShowTerminalProgress: () => false, getFullscreenCopyOnSelect: () => true };
  mode.session = { isStreaming: false, isCompacting: false };
  mode.mountInteractiveTui = (tui, components) => prototype.mountInteractiveTui.call(mode, tui, components);
  mode.renderWidgets = () => prototype.renderWidgets.call(mode);
  mode.renderWidgetContainer = (container, widgets, spacerWhenEmpty, leadingSpacer) =>
    prototype.renderWidgetContainer.call(mode, container, widgets, spacerWhenEmpty, leadingSpacer);
  mode.rebindExtensionTerminalInputListeners = () => prototype.rebindExtensionTerminalInputListeners.call(mode);

  // InteractiveMode mounts these persistent containers before session_start. The same objects are
  // transferred by switchTuiMode; the fullscreen layout also owns them when it invalidates.
  mode.renderer.addChild(above);
  mode.renderer.addChild(below);
  mode.renderer.setLayoutRoot(fullscreenLayoutRoot);
  mode.renderer.renderNow();

  const firstFullscreen = mode.renderer;
  const originalHooks = hookValues(firstFullscreen);
  const handlers = new Map<string, LifecycleHandler>();
  r.disposers.push(() => {
    void handlers.get('session_shutdown')?.({ reason: 'test-cleanup' }, ctx);
    if (mode.renderer !== r.tui) mode.renderer.stop({ preserveScreen: true });
  });
  const widgetFactories = vi.fn();
  const module = await extension();
  await module.default({ on: (name, handler) => handlers.set(name, handler), registerProvider: vi.fn() }, {
    clipboardIO: { platform: 'darwin', env: localEnv, piVersion: '0.87.1', writeText: r.write },
  });
  const ctx = {
    mode: 'tui',
    hasUI: true,
    ui: {
      setWidget(key: string, content: WidgetContent, options?: WidgetOptions) {
        const counted = typeof content === 'function' ? ((tui: TuiView, theme: { fg(color: string, text: string): string }) => {
          widgetFactories();
          return content(tui, theme);
        }) : content;
        prototype.setExtensionWidget.call(mode, key, counted, options);
      },
      notify: r.notify,
      setEditorComponent: vi.fn(),
    },
  };
  await handlers.get('session_start')!({ reason: 'startup' }, ctx);
  expect(widgetFactories).toHaveBeenCalledTimes(1);
  expect(firstFullscreen.inputListeners).toHaveLength(2);

  r.drag();
  r.terminal.input('\x03');
  await flush();
  expect(r.write.mock.calls.map(([text]) => text)).toEqual([selectedText]);
  r.write.mockClear();

  expect(prototype.switchTuiMode.call(mode, 'regular')).toBe(true);
  expect(prototype.switchTuiMode.call(mode, 'fullscreen')).toBe(true);
  const nextFullscreen = mode.renderer;
  nextFullscreen.renderNow();

  expect.soft({
    widgetFactoryCalls: widgetFactories.mock.calls.length,
    oldRenderer: {
      restoredHookCount: adapterHooks.filter((key, index) => Reflect.get(firstFullscreen, key) === originalHooks[index]).length,
      inputListenerCount: firstFullscreen.inputListeners.size,
    },
    newRenderer: {
      inputListenerCount: nextFullscreen.inputListeners.size,
      managedHookCount: adapterHooks.filter((key) => Object.prototype.hasOwnProperty.call(nextFullscreen, key)).length,
    },
  }, 'renderer replacement did not transfer exactly one adapter without recreating its widget').toEqual({
    widgetFactoryCalls: 1,
    oldRenderer: { restoredHookCount: adapterHooks.length, inputListenerCount: 1 },
    newRenderer: { inputListenerCount: 2, managedHookCount: adapterHooks.length },
  });
  expect.soft(nextFullscreen).not.toBe(firstFullscreen);

  r.terminal.input('\x1b[<0;1;1M');
  r.terminal.input('\x1b[<32;31;2M');
  r.terminal.input('\x1b[<0;31;2m');
  r.terminal.input('\x03');
  await flush();
  expect(r.write.mock.calls.map(([text]) => text)).toEqual([selectedText]);

  await handlers.get('session_shutdown')!({ reason: 'shutdown' }, ctx);
  expect(nextFullscreen.inputListeners).toHaveLength(1);
  nextFullscreen.stop({ preserveScreen: true });
});

it.each([
  ['PageUp', '\x1b[5~', 'scrollBy'],
  ['Home', '\x1b[H', 'scrollToTop'],
] as const)('viewport-consumed %s retires stale selection authority before Ctrl+C, then a fresh selection still copies', async (_label, input, action) => {
  const r = await editorRig();
  rigs.push(r);
  install(await extension(), { ...r, tui: r.reference });
  r.draft('untouched draft');
  r.drag();
  await flush();

  const viewport = r.tui as TuiView & { scrollBy(lines: number): void; scrollToTop(): void };
  const parsedAction = vi.spyOn(viewport, action);
  r.input(input);
  expect(parsedAction, `${input} was not parsed and consumed by pinned TuiAltScreen`).toHaveBeenCalledTimes(1);

  r.input('\x03');
  await flush();
  expect.soft({
    nativeWrites: r.write.mock.calls.map(([text]) => text),
    interrupts: r.receiver.handleCtrlC.mock.calls.length,
    editorText: r.editor.getText(),
  }, 'viewport-consumed navigation left stale clipboard authority ahead of normal interrupt routing').toEqual({
    nativeWrites: [],
    interrupts: 1,
    editorText: 'untouched draft',
  });

  r.write.mockClear();
  r.receiver.handleCtrlC.mockClear();
  r.drag();
  r.input('\x03');
  await flush();
  expect(r.write.mock.calls.map(([text]) => text)).toEqual([selectedText]);
  expect(r.receiver.handleCtrlC).not.toHaveBeenCalled();
});

it.each([
  ['up', '\x1b[<64;1;1M', -1, 2],
  ['down', '\x1b[<65;1;1M', 1, 0],
] as const)('viewport-consumed SGR wheel %s retires stale selection before Ctrl+C without poisoning the next left drag', async (_direction, input, delta, initialTop) => {
  const r = await editorRig();
  rigs.push(r);
  r.lines.push(...Array.from({ length: 10 }, (_, index) => `line ${index + 2}`));
  r.draw();
  r.scroll.scrollTo(initialTop);
  r.draw();
  install(await extension(), { ...r, tui: r.reference });
  r.draft('untouched draft');
  r.drag();
  await flush();
  expect(r.tui.getSelectionBounds(), 'real left-button SGR drag did not establish a pinned Pi selection').toBeDefined();

  const scrollBy = vi.spyOn(r.scroll, 'scrollBy');
  const topBeforeWheel = r.scroll.scrollTop;
  r.input(input);
  expect(scrollBy, `${input} was not parsed and routed by pinned TuiAltScreen wheel behavior`).toHaveBeenCalledWith(delta);
  expect(r.scroll.scrollTop).toBe(topBeforeWheel + delta);

  r.input('\x03');
  await flush();
  expect.soft({
    nativeWrites: r.write.mock.calls.map(([text]) => text),
    interrupts: r.receiver.handleCtrlC.mock.calls.length,
    editorText: r.editor.getText(),
    editorFocused: r.tui.focusedComponent === r.editor,
  }, 'viewport-consumed wheel left stale clipboard authority ahead of normal interrupt routing').toEqual({
    nativeWrites: [],
    interrupts: 1,
    editorText: 'untouched draft',
    editorFocused: true,
  });

  r.write.mockClear();
  r.receiver.handleCtrlC.mockClear();
  r.draw();
  r.drag();
  r.input('\x03');
  await flush();
  expect(r.write.mock.calls.map(([text]) => text)).toEqual(['строка два\nline 2']);
  expect(r.receiver.handleCtrlC).not.toHaveBeenCalled();
});
