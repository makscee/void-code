// Loaded by the ACTUAL unbundled CLI or pi~BUN.mjs extension loader.
// No account, PTY, GUI, terminal emulator selection or global stdout interception.
import assert from 'node:assert/strict';
import childProcess, { execFileSync } from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';
import { InteractiveMode, type ExtensionAPI } from '@earendil-works/pi-coding-agent';
import path from 'node:path';
import { TuiAltScreen, ScrollView } from '@earendil-works/pi-tui';
import managed from './managed.ts';
import type { ComponentView, LifecycleHandler, TuiView, WidgetContent, WidgetOptions } from './pi-fullscreen-clipboard';

interface WidgetReceiver {
  extensionWidgetsAbove: Map<string, ComponentView>;
  extensionWidgetsBelow: Map<string, ComponentView>;
  ui: TuiAltScreen;
  renderWidgets(): void;
}
interface WidgetConsumerView {
  setExtensionWidget(this: WidgetReceiver, key: string, value: WidgetContent, options?: WidgetOptions): void;
  clearExtensionWidgets(this: WidgetReceiver): void;
}

export default async function (pi: ExtensionAPI): Promise<void> {
  assert.equal(process.env.VC_ISOLATED_CLIPBOARD_ACCEPTANCE, 'I_OWN_THIS_ISOLATED_CLIPBOARD_SESSION');
  assert.ok(process.platform === 'darwin' || process.platform === 'win32');
  const markers = [
    '{\\rtf1\\ansi literal Привет 世界 😀}',
    '%!PS-Adobe-3.0 EPSF-3.0\n%%BoundingBox: 0 0 10 10\nshowpage',
    'ASTRA-A Привет 世界 😀',
    'ASTRA-B Другая 日本 🦊',
  ];
  // Independent reader: require the declared plain-string type, never RTF fallback.
  // Return ASCII base64 of explicitly encoded UTF-8, independent of terminal locale.
  const macReadScript = `
    ObjC.import('Foundation');
    ObjC.import('AppKit');
    const board = $.NSPasteboard.generalPasteboard;
    if (!board.types.containsObject($.NSPasteboardTypeString)) throw new Error('plain type unavailable');
    const text = board.stringForType($.NSPasteboardTypeString);
    if (text.isNil()) throw new Error('plain string unavailable');
    ObjC.unwrap(text.dataUsingEncoding($.NSUTF8StringEncoding).base64EncodedStringWithOptions(0));
  `;
  let input: (data: string) => void = () => {};
  const terminal = {
    columns: 80, rows: 8, kittyProtocolActive: false,
    start(callback: (data: string) => void) { input = callback; }, stop() {}, async drainInput() {},
    write() {}, moveBy() {}, hideCursor() {}, showCursor() {}, clearLine() {},
    clearFromCursor() {}, clearScreen() {}, setTitle() {}, setProgress() {},
  };
  const tui = new TuiAltScreen(terminal, false);
  let lines = [markers[0]];
  const scroll = new ScrollView({ render: () => lines, invalidate() {} }, { primary: true, follow: 'none', scrollbar: 'hidden' });
  tui.setLayoutRoot(scroll);
  tui.start();
  const failures: string[] = [];
  const handlers = new Map<string, LifecycleHandler[]>();
  let providers = 0;
  let widgets = 0;
  const api = new Proxy(pi, { get(target, key) {
    if (key === 'on') return (name: string, handler: LifecycleHandler) => { handlers.set(name, [...(handlers.get(name) ?? []), handler]); };
    if (key === 'registerProvider') return (...args: Parameters<ExtensionAPI['registerProvider']>) => { providers++; return target.registerProvider(...args); };
    return Reflect.get(target, key);
  } });
  // Real exported consumer method, without constructing a session/auth/inference stack.
  const receiver: WidgetReceiver = { extensionWidgetsAbove: new Map(), extensionWidgetsBelow: new Map(), ui: tui, renderWidgets() {} };
  const consumer = InteractiveMode.prototype as unknown as WidgetConsumerView;
  const setWidget = (key: string, value: WidgetContent, options?: WidgetOptions) =>
    consumer.setExtensionWidget.call(receiver, key, value, options);
  // Positive consumer control: replacement disposes before the new factory, and
  // removal disposes the stored result even when it moved below the editor.
  let disposed = 0;
  const component = () => ({ render: () => [], invalidate() {}, dispose() { disposed++; } });
  setWidget('astra-consumer-control', (actualTui) => { assert.equal(actualTui, tui); return component(); });
  assert.equal(receiver.extensionWidgetsAbove.size, 1);
  setWidget('astra-consumer-control', () => { assert.equal(disposed, 1); return component(); }, { placement: 'belowEditor' });
  assert.equal(receiver.extensionWidgetsAbove.size, 0);
  assert.equal(receiver.extensionWidgetsBelow.size, 1);
  setWidget('astra-consumer-control', undefined);
  assert.equal(disposed, 2); assert.equal(receiver.extensionWidgetsBelow.size, 0);
  setWidget('astra-consumer-control', undefined); assert.equal(disposed, 2);
  const ctx = { mode: 'tui', hasUI: true, ui: {
    notify: (message: string) => failures.push(message),
    setWidget: (key: string, value: WidgetContent, options?: WidgetOptions) => {
      if (typeof value === 'function') widgets++;
      setWidget(key, value, options);
    },
    setEditorComponent: () => assert.fail('must not replace editor'),
  } };
  // Substitute only bootstrap, never spawn/native clipboard IO or the managed factory.
  // The actual consumer loader imports the unchanged Go-managed source above.
  const originalExec = childProcess.execFileSync;
  process.env.VC_BOOTSTRAP_EXECUTABLE = process.execPath;
  childProcess.execFileSync = ((file: string, args: string[]) => {
    assert.equal(file, process.execPath); assert.deepEqual(args, ['pi-bootstrap']);
    return JSON.stringify({ version: 1, relayUrl: 'https://relay.invalid', authToken: 'fixture-only', providers: [{ kind: 'codex', relayProviderId: 'fixture', models: ['gpt-5.6-terra'] }] });
  }) as typeof execFileSync;
  try {
    syncBuiltinESMExports();
    await managed(api); // NO clipboardIO: exercise the real production default registration.
  } finally { childProcess.execFileSync = originalExec; syncBuiltinESMExports(); }
  assert.ok(providers > 0, 'synthetic bootstrap did not register provider');
  assert.ok(handlers.has('session_start'), 'default clipboard lifecycle missing');
  // Observe real completion flash, not OSC52. Extraction stays entirely in Pi.
  const originalFlash = tui.flash.bind(tui);
  let succeeded = 0;
  tui.flash = (...args: Parameters<typeof originalFlash>) => { if (args[0] === 'Copied!') succeeded++; return originalFlash(...args); };
  try {
    for (const handler of handlers.get('session_start') ?? []) await handler({ reason: 'startup' }, ctx);
    assert.ok(widgets > 0, 'default factory did not acquire real TUI through setWidget');
    for (const [index, marker] of markers.entries()) {
      lines = marker.split('\n'); tui.renderNow();
      input('\x1b[<0;1;1M'); input(`\x1b[<32;60;${lines.length}M`); input(`\x1b[<0;60;${lines.length}m`);
      assert.ok((tui as unknown as TuiView).getSelectionBounds()?.start.scrollView === scroll, 'real scroll-view selection missing');
      const deadline = Date.now() + 6500;
      while (succeeded <= index && failures.length === 0 && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 10));
      assert.deepEqual(failures, []); assert.equal(succeeded, index + 1, 'native completion missing');
      let readback: string;
      try {
        const encoded = process.platform === 'darwin'
          ? execFileSync('/usr/bin/osascript', ['-l', 'JavaScript', '-e', macReadScript], {
            encoding: 'utf8', timeout: 5000, env: { ...process.env, LC_ALL: 'C' }, stdio: ['ignore', 'pipe', 'pipe'],
          })
          : execFileSync(path.win32.join(process.env.SystemRoot!, 'System32/WindowsPowerShell/v1.0/powershell.exe'), [
            '-NoProfile', '-NonInteractive', '-Sta', '-Command',
            'Add-Type -AssemblyName System.Windows.Forms; [Console]::Write([Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes([Windows.Forms.Clipboard]::GetText())))',
          ], { encoding: 'utf8', timeout: 5000 });
        readback = Buffer.from(encoded.trim(), 'base64').toString('utf8');
      } catch {
        // Child errors may contain private stdout/stderr; never propagate them.
        assert.fail('Independent plain-text clipboard read failed');
      }
      assert.ok(readback === marker, 'OS plain-text clipboard did not exactly match complete selection');
    }
    console.log('ASTRA_NATIVE_SELECTION_READBACK_OK_4');
  } finally {
    for (const handler of handlers.get('session_shutdown') ?? []) await handler({ reason: 'quit' }, ctx);
    consumer.clearExtensionWidgets.call(receiver);
    tui.stop({ preserveScreen: true });
  }
}
