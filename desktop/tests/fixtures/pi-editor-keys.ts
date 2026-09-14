// Test-only views of pinned private APIs. No editor/key-handler/history implementation.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { expect, vi } from 'vitest';
import { agentDir, extension, interactiveFile, localEnv, rig, widgetUI, type ComponentView, type TuiView, type ExtensionModule } from './pi-fullscreen-clipboard';
import { consumerHooks, referenceFactory } from './pi-interactive-consumer';

export const keys = { esc: '\x1b', up: '\x1b[A', down: '\x1b[B', left: '\x1b[D', right: '\x1b[C', undo: '\x1f' };
export interface EditorView extends ComponentView {
  handleInput(data: string): void;
  setText(text: string): void;
  getText(): string;
  getExpandedText(): string;
  getCursor(): { line: number; col: number };
  addToHistory(text: string): void;
  navigateHistory(direction: -1 | 1): void;
  history: string[];
  historyIndex: number;
  isInPaste: boolean;
  jumpMode: string | null;
  onEscape?: () => void;
  onSubmit?: (text: string) => void;
  onExtensionShortcut?: (data: string) => boolean;
  onAction(action: string, handler: () => void): void;
  setAutocompleteProvider(provider: unknown): void;
  isShowingAutocomplete(): boolean;
  autocompleteRequestTask: Promise<void>;
  autocompleteList?: { getSelectedItem(): { value: string } };
}
export interface KeysContext {
  mode: string;
  hasUI: boolean;
  isIdle(): boolean;
  hasPendingMessages(): boolean;
  ui: Awaited<ReturnType<typeof widgetUI>> & { getEditorComponent(): unknown; getEditorText(): string; setEditorText(text: string): void };
}
// Proposed production export, alongside default registration in raw Go embedded source.
export interface PiEditorKeysOptions {
  platform: string;
  env: Record<string, string | undefined>;
  piVersion: string;
  ctx: KeysContext;
}
export type KeysModule = ExtensionModule & {
  installPiEditorKeys?: (tui: TuiView, options: PiEditorKeysOptions) => () => void;
};
export async function keysModule(env = localEnv): Promise<KeysModule> {
  const { CustomEditor } = await import(/* @vite-ignore */ pathToFileURL(path.join(agentDir, 'dist/modes/interactive/components/custom-editor.js')).href);
  return extension(env, undefined, { CustomEditor });
}

export async function editorRig(consumerFile = interactiveFile) {
  const r = await rig();
  // Thin module imports only PiTui, not SDK index/provider initialization.
  const { CustomEditor } = await import(/* @vite-ignore */ pathToFileURL(path.join(agentDir, 'dist/modes/interactive/components/custom-editor.js')).href);
  const tuiModule = await import(/* @vite-ignore */ pathToFileURL(path.join(agentDir, 'node_modules/@earendil-works/pi-tui/dist/index.js')).href);
  // Read the actual app definitions without loading config.js / user settings.
  const source = readFileSync(path.join(agentDir, 'dist/core/keybindings.js'), 'utf8');
  const definition = source.slice(source.indexOf('export const KEYBINDINGS ='), source.indexOf('const KEYBINDING_NAME_MIGRATIONS'));
  const definitions = new Function('TUI_KEYBINDINGS', 'process', definition.replace('export const', 'const') + '; return KEYBINDINGS;')(tuiModule.TUI_KEYBINDINGS, { platform: 'darwin' });
  const kb = new tuiModule.KeybindingsManager(definitions);
  const theme = { borderColor: (s: string) => s, selectList: { selectedPrefix: (s: string) => s, selectedText: (s: string) => s, description: (s: string) => s, scrollInfo: (s: string) => s, noMatch: (s: string) => s } };
  const reference = referenceFactory<TuiView>(consumerFile)(() => r.tui);
  const editor: EditorView = new CustomEditor(reference, theme, kb);
  r.tui.setFocus(editor);
  const session = { isStreaming: false, isBashRunning: false, abortBash: vi.fn() };
  const receiver = {
    defaultEditor: editor, editor, ui: reference, session, isBashMode: false, lastEscapeTime: 0,
    settingsManager: { getDoubleEscapeAction: () => 'none' },
    restoreQueuedMessagesToEditor: vi.fn(), updateEditorBorderColor: vi.fn(),
    handleCtrlC: vi.fn(), handleCtrlD: vi.fn(), showTreeSelector: vi.fn(), showUserMessageSelector: vi.fn(),
    handleDequeue: vi.fn(), handleClipboardPaste: vi.fn(),
  };
  const hooks = consumerHooks(consumerFile, undefined, ['setupKeyHandlers']);
  expect(hooks.methods.has('setupKeyHandlers')).toBe(true);
  const setup = new Function(`return class { ${hooks.methods.get('setupKeyHandlers')} }`)().prototype.setupKeyHandlers;
  setup.call(receiver); // Execute Pi's actual key handlers, not handwritten equivalents.
  const submit = vi.fn(); editor.onSubmit = submit;
  const ui = { ...await widgetUI(r, reference, true), getEditorComponent: vi.fn(() => undefined), getEditorText: () => editor.getText(), setEditorText: (s: string) => editor.setText(s) };
  const activity = { idle: true, pending: false };
  const ctx: KeysContext = { mode: 'tui', hasUI: true, isIdle: () => activity.idle, hasPendingMessages: () => activity.pending, ui };
  return { ...r, reference, editor, receiver, session, ctx, activity, submit,
    input(data: string) { r.terminal.input(data); },
    draft(text: string) { r.terminal.input(`\x1b[200~${text}\x1b[201~`); },
  };
}
export type EditorRig = Awaited<ReturnType<typeof editorRig>>;
export function attach(module: KeysModule, r: EditorRig, overrides: Partial<PiEditorKeysOptions> = {}) {
  expect(module.installPiEditorKeys, 'K5: Go managed source must export typed installPiEditorKeys seam').toBeTypeOf('function');
  const dispose = module.installPiEditorKeys!(r.reference, { platform: 'darwin', env: localEnv, piVersion: '0.84.1', ctx: r.ctx, ...overrides });
  r.disposers.push(dispose); return dispose;
}
export async function startDefault(module: KeysModule, r: EditorRig) {
  const handlers = new Map<string, Array<(event: { reason: string }, ctx: KeysContext) => unknown>>();
  await module.default({ on(name, handler) { const list = handlers.get(name) ?? []; list.push(handler); handlers.set(name, list); }, registerProvider: vi.fn() });
  expect(handlers.has('session_start')).toBe(true);
  for (const handler of handlers.get('session_start') ?? []) await handler({ reason: 'startup' }, r.ctx);
  return handlers;
}
