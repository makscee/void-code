// Loaded by the ACTUAL unbundled CLI or pi~BUN.mjs extension loader.
// No account, PTY, GUI, terminal emulator selection or global stdout interception.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { TuiAltScreen, ScrollView } from '@earendil-works/pi-tui';
import { installFullscreenClipboard, createNativeClipboardWriter } from './managed.ts';

export default async function (): Promise<void> {
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
  const writer = createNativeClipboardWriter({ platform: process.platform, env: process.env });
  const failures: string[] = [];
  const dispose = installFullscreenClipboard(tui, {
    piVersion: '0.84.1', platform: process.platform,
    env: { ...process.env, VC_BOOTSTRAP_EXECUTABLE: process.execPath },
    writeText: writer, notify: (message: string) => failures.push(message),
  });
  // Observe real completion flash, not OSC52. Extraction stays entirely in Pi.
  const originalFlash = tui.flash.bind(tui);
  let succeeded = 0;
  tui.flash = (text: string, ...args: any[]) => { if (text === 'Copied!') succeeded++; return originalFlash(text, ...args); };
  try {
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
  } finally { dispose(); tui.stop({ preserveScreen: true }); }
}
