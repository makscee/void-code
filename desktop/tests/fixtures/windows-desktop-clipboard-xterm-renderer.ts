import { Terminal } from '@xterm/xterm';
import { createOrderedTerminalInputSink, installWindowsClipboardShortcuts } from '../../src/renderer/clipboard-shortcuts';

type FixtureResult = {
  implementation: '@xterm/xterm';
  instance: boolean;
  copied: string[];
  terminalData: string[];
  keydowns: Array<{ code: string; defaultPrevented: boolean }>;
  keyups: Array<{ code: string; defaultPrevented: boolean }>;
  domCopyEvents: number;
  domPasteEvents: number;
};

const result: FixtureResult = {
  implementation: '@xterm/xterm',
  instance: false,
  copied: [],
  terminalData: [],
  keydowns: [],
  keyups: [],
  domCopyEvents: 0,
  domPasteEvents: 0,
};
const terminal = new Terminal({ cols: 40, rows: 4 });
result.instance = terminal instanceof Terminal;
const host = document.querySelector<HTMLElement>('#terminal');
if (!host) throw new Error('terminal fixture host missing');
host.style.width = '640px';
host.style.height = '160px';
terminal.open(host);

document.addEventListener('keydown', (event) => {
  if (event.code === 'KeyC' || event.code === 'KeyV') result.keydowns.push({ code: event.code, defaultPrevented: event.defaultPrevented });
});
document.addEventListener('keyup', (event) => {
  if (event.code !== 'KeyC' && event.code !== 'KeyV') return;
  result.keyups.push({ code: event.code, defaultPrevented: event.defaultPrevented });
  if (event.code === 'KeyC') document.title = 'XTERM:COPY-DONE';
  else document.title = `XTERM:RESULT:${JSON.stringify(result)}`;
});
document.addEventListener('copy', () => { result.domCopyEvents++; });
document.addEventListener('paste', () => { result.domPasteEvents++; });

const terminalInput = createOrderedTerminalInputSink((data) => {
  result.terminalData.push(data);
  if (data === 'pasted once') document.title = 'XTERM:PASTE-DATA';
});
installWindowsClipboardShortcuts(
  terminal,
  'win32',
  async () => ({ kind: 'text', text: 'pasted once' }),
  terminalInput,
  async (text) => { result.copied.push(text); },
);
terminal.onData((data) => { terminalInput.send(data); });

terminal.write('selected text', () => {
  terminal.select(0, 0, 'selected text'.length);
  terminal.focus();
  document.title = 'XTERM:READY';
});
