// Loaded by the ACTUAL unbundled CLI or pi~BUN.mjs extension loader.
// No account, PTY, GUI, terminal emulator selection or global stdout interception.
import assert from 'node:assert/strict';
import childProcess, { execFileSync } from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';
import { InteractiveMode, type ExtensionAPI } from '@earendil-works/pi-coding-agent';
import path from 'node:path';
import { TuiAltScreen, ScrollView } from '@earendil-works/pi-tui';
import managed from './managed.ts';

export default async function (pi: ExtensionAPI): Promise<void> {
  assert.equal(process.env.VC_ISOLATED_CLIPBOARD_ACCEPTANCE, 'I_OWN_THIS_ISOLATED_CLIPBOARD_SESSION');
  assert.ok(process.platform === 'darwin' || process.platform === 'win32');
  const markers = ['ASTRA-A Привет 世界 😀', 'ASTRA-B Другая 日本 🦊'];
  let input: (data: string) => void = () => {};
  const terminal = {
    columns: 80, rows: 8, kittyProtocolActive: false,
    start(callback: (data: string) => void) { input = callback; }, stop() {}, async drainInput() {},
    write(_data: string) {}, moveBy() {}, hideCursor() {}, showCursor() {}, clearLine() {},
    clearFromCursor() {}, clearScreen() {}, setTitle() {}, setProgress() {},
  };
  const tui = new TuiAltScreen(terminal, false);
  let lines = [markers[0]];
  const scroll = new ScrollView({ render: () => lines, invalidate() {} }, { primary: true, follow: 'none', scrollbar: 'hidden' });
  tui.setLayoutRoot(scroll);
  tui.start();
  const failures: string[] = [];
  const handlers = new Map<string, any[]>();
  let providers = 0;
  let widgets = 0;
  const api = new Proxy(pi, { get(target, key) {
    if (key === 'on') return (name: string, handler: any) => { handlers.set(name, [...(handlers.get(name) ?? []), handler]); };
    if (key === 'registerProvider') return (...args: any[]) => { providers++; return (target.registerProvider as any)(...args); };
    return Reflect.get(target, key);
  } });
  // Real exported consumer method, without constructing a session/auth/inference stack.
  const receiver = { extensionWidgetsAbove: new Map(), extensionWidgetsBelow: new Map(), ui: tui, renderWidgets() {} };
  const setWidget = (key: string, value: any, options?: any) =>
    (InteractiveMode.prototype as any).setExtensionWidget.call(receiver, key, value, options);
  // Positive consumer control: replacement disposes before the new factory, and
  // removal disposes the stored result even when it moved below the editor.
  let disposed = 0;
  const component = () => ({ render: () => [], invalidate() {}, dispose() { disposed++; } });
  setWidget('astra-consumer-control', (actualTui: any) => { assert.equal(actualTui, tui); return component(); });
  assert.equal(receiver.extensionWidgetsAbove.size, 1);
  setWidget('astra-consumer-control', () => { assert.equal(disposed, 1); return component(); }, { placement: 'belowEditor' });
  assert.equal(receiver.extensionWidgetsAbove.size, 0);
  assert.equal(receiver.extensionWidgetsBelow.size, 1);
  setWidget('astra-consumer-control', undefined);
  assert.equal(disposed, 2); assert.equal(receiver.extensionWidgetsBelow.size, 0);
  setWidget('astra-consumer-control', undefined); assert.equal(disposed, 2);
  const ctx = { mode: 'tui', hasUI: true, ui: {
    notify: (message: string) => failures.push(message),
    setWidget: (key: string, value: any, options?: any) => {
      if (typeof value === 'function') widgets++;
      setWidget(key, value, options);
    },
    setEditorComponent: () => assert.fail('must not replace editor'),
  } };
  // Substitute only bootstrap, never spawn/native clipboard IO or the managed factory.
  // The actual consumer loader imports the unchanged Go-managed source above.
  const originalExec = childProcess.execFileSync;
  process.env.VC_BOOTSTRAP_EXECUTABLE = process.execPath;
  childProcess.execFileSync = ((file: string, args: string[], options: any) => {
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
  tui.flash = (text: string, ...args: any[]) => { if (text === 'Copied!') succeeded++; return originalFlash(text, ...args); };
  try {
    for (const handler of handlers.get('session_start') ?? []) await handler({ reason: 'startup' }, ctx);
    assert.ok(widgets > 0, 'default factory did not acquire real TUI through setWidget');
    for (const [index, marker] of markers.entries()) {
      lines = [marker]; tui.renderNow();
      input('\x1b[<0;1;1M'); input('\x1b[<32;60;1M'); input('\x1b[<0;60;1m');
      assert.ok((tui as any).getSelectionBounds()?.start.scrollView === scroll, 'real scroll-view selection missing');
      const deadline = Date.now() + 6500;
      while (succeeded <= index && failures.length === 0 && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 10));
      assert.deepEqual(failures, []); assert.equal(succeeded, index + 1, 'native completion missing');
      const readback = process.platform === 'darwin'
        ? execFileSync('/usr/bin/pbpaste', [], { encoding: 'utf8', timeout: 5000 })
        : Buffer.from(execFileSync(path.win32.join(process.env.SystemRoot!, 'System32/WindowsPowerShell/v1.0/powershell.exe'), [
          '-NoProfile', '-NonInteractive', '-Sta', '-Command',
          'Add-Type -AssemblyName System.Windows.Forms; [Console]::Write([Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes([Windows.Forms.Clipboard]::GetText())))',
        ], { encoding: 'utf8', timeout: 5000 }), 'base64').toString('utf8');
      assert.equal(readback, marker, 'OS clipboard did not contain selected Unicode marker');
    }
    console.log('ASTRA_NATIVE_SELECTION_READBACK_OK_2');
  } finally {
    for (const handler of handlers.get('session_shutdown') ?? []) await handler({ reason: 'quit' }, ctx);
    (InteractiveMode.prototype as any).clearExtensionWidgets.call(receiver);
    tui.stop({ preserveScreen: true });
  }
}
