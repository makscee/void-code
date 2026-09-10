// Loaded by the ACTUAL unbundled CLI or pi~BUN.mjs extension loader.
// No account, PTY, GUI, terminal emulator selection or global stdout interception.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, realpathSync } from 'node:fs';
import childProcess, { execFileSync } from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';
import { errorMonitor } from 'node:events';
import { InteractiveMode, type ExtensionAPI } from '@earendil-works/pi-coding-agent';
import path from 'node:path';
import { TuiAltScreen, ScrollView } from '@earendil-works/pi-tui';
import managed from './managed.ts';
import { nativeWitness, preserveNativeFailure } from './pi-fullscreen-native-witness.ts';
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
  let liveOsc52 = 0;
  const terminal = {
    columns: 80, rows: 8, kittyProtocolActive: false,
    start(callback: (data: string) => void) { input = callback; }, stop() {}, async drainInput() {},
    write(data: string) { if (data.includes('\x1b]52;')) liveOsc52++; }, moveBy() {}, hideCursor() {}, showCursor() {}, clearLine() {},
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
  // Narrow, provenance-bound hook extracted from the selected consumer's actual
  // factory (the bundle itself for pi~BUN.mjs). No runtime patch or handwritten proxy.
  // Constructing InteractiveMode would also initialize keybindings/themes/session state.
  const provenance = JSON.parse(readFileSync(process.env.VC_R8_CONSUMER_REFERENCE!, 'utf8')) as {
    file: string; entry: string; sha256: string; factory: string; name: string;
  };
  assert.equal(realpathSync(process.argv[1]), realpathSync(provenance.entry));
  assert.equal(createHash('sha256').update(readFileSync(provenance.file)).digest('hex'), provenance.sha256);
  assert.ok(InteractiveMode.toString().includes(provenance.name), 'loaded consumer constructor must use extracted factory');
  const createReference = new Function(`${provenance.factory}; return ${provenance.name};`)() as (getTui: () => TuiAltScreen) => TuiAltScreen;
  const uiReference = createReference(() => tui);
  assert.notEqual(uiReference, tui);
  assert.equal(Object.getPrototypeOf(uiReference), Object.getPrototypeOf(tui));
  assert.notEqual(uiReference.flash, uiReference.flash, 'actual consumer returns fresh bound getter closures');
  const receiver: WidgetReceiver = { extensionWidgetsAbove: new Map(), extensionWidgetsBelow: new Map(), ui: uiReference, renderWidgets() {} };
  const consumer = InteractiveMode.prototype as unknown as WidgetConsumerView;
  const setWidget = (key: string, value: WidgetContent, options?: WidgetOptions) =>
    consumer.setExtensionWidget.call(receiver, key, value, options);
  // Positive consumer control: replacement disposes before the new factory, and
  // removal disposes the stored result even when it moved below the editor.
  let disposed = 0;
  const component = () => ({ render: () => [], invalidate() {}, dispose() { disposed++; } });
  setWidget('astra-consumer-control', (actualTui) => { assert.equal(actualTui, uiReference); assert.notEqual(actualTui, tui); return component(); });
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
      setWidget(key, typeof value === 'function' ? (actualTui, theme) => {
        assert.equal(actualTui, uiReference, 'native adapter must acquire actual consumer proxy');
        return value(actualTui, theme);
      } : value, options);
    },
    setEditorComponent: () => assert.fail('must not replace editor'),
  } };
  // Substitute only bootstrap, never native clipboard IO or the managed factory.
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
  // Observe the built-in used by the default factory, never replace the native child.
  const originalSpawn = childProcess.spawn;
  // Bounded metadata only; no serialization or IO on the observed lifecycle path.
  const records: { executable: string; operation: number; elapsedMs: number; event: string; code: string | number | null; signal: NodeJS.Signals | null }[] = [];
  const maxRecords = 128;
  let operation = 0;
  let firstNativeArgs: Parameters<typeof originalSpawn> | undefined;
  let acceptanceFailed = false;
  let acceptanceError: unknown;
  let failureStage = 'setup';
  let failureIndex = -1;
  childProcess.spawn = function (...args: Parameters<typeof originalSpawn>) {
    const id = ++operation;
    if (id === 1) firstNativeArgs = args;
    const started = performance.now();
    const executable = path.basename(args[0]);
    const log = (event: string, code: string | number | null = null, signal: NodeJS.Signals | null = null) => {
      if (records.length < maxRecords) records.push({ executable, operation: id, elapsedMs: Math.round(performance.now() - started), event, code, signal });
    };
    // Only bounded symbolic codes, never Error.message (which can include argv).
    const errorCode = (error: NodeJS.ErrnoException): string | null =>
      typeof error.code === 'string' && /^[A-Z][A-Z0-9_]{0,63}$/.test(error.code) ? error.code : null;
    log('call');
    let child: ReturnType<typeof originalSpawn>;
    try { child = Reflect.apply(originalSpawn, childProcess, args); }
    catch (error) { log('throw', errorCode(error as NodeJS.ErrnoException)); throw error; }
    child.on('spawn', () => log('spawn'));
    child.on('exit', (code, signal) => log('exit', code, signal));
    child.on('close', (code, signal) => log('close', code, signal));
    child.on(errorMonitor, (error: NodeJS.ErrnoException) => log('error', errorCode(error)));
    child.stdin?.on('finish', () => log('stdin-finish'));
    child.stdin?.on(errorMonitor, (error: NodeJS.ErrnoException) => log('stdin-error', errorCode(error)));
    // No stderr/stdout data listeners, stdin writes, timers, or error handlers.
    return child;
  } as typeof originalSpawn;
  try {
    syncBuiltinESMExports();
    for (const handler of handlers.get('session_start') ?? []) await handler({ reason: 'startup' }, ctx);
    assert.ok(widgets > 0, 'default factory did not acquire real TUI through setWidget');
    for (const [index, marker] of markers.entries()) {
      failureIndex = index;
      failureStage = 'native-completion';
      lines = marker.split('\n'); tui.renderNow();
      input('\x1b[<0;1;1M'); input(`\x1b[<32;60;${lines.length}M`); input(`\x1b[<0;60;${lines.length}m`);
      assert.ok((tui as unknown as TuiView).getSelectionBounds()?.start.scrollView === scroll, 'real scroll-view selection missing');
      assert.equal(liveOsc52, 0, 'managed copy leaked to live OSC52 writer');
      assert.equal(succeeded, index, 'Copied! preceded asynchronous native completion');
      const deadline = Date.now() + 6500;
      while (succeeded <= index && failures.length === 0 && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 10));
      assert.deepEqual(failures, []); assert.equal(succeeded, index + 1, 'native completion missing');
      failureStage = 'independent-readback';
      let readback: string;
      try {
        const encoded = process.platform === 'darwin'
          ? execFileSync('/usr/bin/osascript', ['-l', 'JavaScript', '-e', macReadScript], {
            encoding: 'utf8', timeout: 5000, env: { ...process.env, LC_ALL: 'C' }, stdio: ['ignore', 'pipe', 'pipe'],
          })
          : execFileSync(path.win32.join(process.env.SystemRoot!, 'System32/WindowsPowerShell/v1.0/powershell.exe'), [
            '-NoProfile', '-NonInteractive', '-Sta', '-Command',
            "$ErrorActionPreference='Stop'; [void][Reflection.Assembly]::Load('System.Windows.Forms, Version=4.0.0.0, Culture=neutral, PublicKeyToken=b77a5c561934e089'); [Console]::Write([Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes([Windows.Forms.Clipboard]::GetText())))",
          ], { encoding: 'utf8', timeout: 5000 });
        readback = Buffer.from(encoded.trim(), 'base64').toString('utf8');
      } catch {
        // Child errors may contain private stdout/stderr; never propagate them.
        assert.fail('Independent plain-text clipboard read failed');
      }
      assert.ok(readback === marker, 'OS plain-text clipboard did not exactly match complete selection');
    }
    console.log('ASTRA_NATIVE_SELECTION_READBACK_OK_4');
  } catch (error) {
    acceptanceFailed = true;
    acceptanceError = error;
    process.stderr.write(`${JSON.stringify({ event: 'acceptance-failure', stage: failureStage, index: failureIndex })}\n`);
    await preserveNativeFailure(error, async () => {
      if (process.platform === 'win32' && process.env.VC_R8_PRIVATE_LAUNCHER === 'VERIFIED' && firstNativeArgs) {
        await nativeWitness(originalSpawn, firstNativeArgs, markers[0], (record) => process.stderr.write(`${JSON.stringify(record)}\n`));
      }
    });
  } finally {
    try {
      for (const handler of handlers.get('session_shutdown') ?? []) await handler({ reason: 'quit' }, ctx);
      consumer.clearExtensionWidgets.call(receiver);
      tui.stop({ preserveScreen: true });
    } catch (error) {
      // Cleanup must not mask the original acceptance failure.
      // eslint-disable-next-line no-unsafe-finally
      throw acceptanceFailed ? acceptanceError : error;
    } finally {
      childProcess.spawn = originalSpawn;
      syncBuiltinESMExports();
      // Native operations and lifecycle cleanup have finished; builtins are restored.
      for (const record of records) process.stderr.write(`${JSON.stringify(record)}\n`);
    }
  }
}
