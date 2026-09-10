import { Terminal } from '@xterm/xterm';
import { wireProductTerminalClipboard } from '../../src/renderer/clipboard-shortcuts';

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

const mac = new URLSearchParams(location.search).get('platform') === 'darwin';
let copyReleases = 0;
const trustedEvents: boolean[] = [];
let ordinaryKey: KeyboardEvent | undefined;
let ordinaryBefore: { copied: number; terminalData: number; domCopyEvents: number } | undefined;
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
  if (mac) trustedEvents.push(event.isTrusted);
  if (mac && event.target instanceof HTMLInputElement && event.target.id === 'ordinary-input') ordinaryKey = event;
  if (event.code === 'KeyC' || event.code === 'KeyV') result.keydowns.push({ code: event.code, defaultPrevented: event.defaultPrevented });
});
document.addEventListener('keyup', (event) => {
  if (event.code === 'KeyC' || event.code === 'KeyV') result.keyups.push({ code: event.code, defaultPrevented: event.defaultPrevented });
  if (event.code === 'KeyC' && mac) {
    copyReleases++;
    if (copyReleases === 1) terminal.clearSelection();
    if (copyReleases === 2) {
      const field = document.createElement('input'); field.id = 'ordinary-input'; field.value = 'field text';
      document.body.append(field); field.focus(); field.select();
      ordinaryBefore = { copied: result.copied.length, terminalData: result.terminalData.length, domCopyEvents: result.domCopyEvents };
    }
    if (copyReleases < 3) document.title = `XTERM:MAC-COPY:${copyReleases}`;
    else setTimeout(() => {
      // Observe after dispatch, including later bubbling handlers and queued input writes.
      // Hidden sendInputEvent does not guarantee a native menu/DOM copy action.
      const ordinaryAfter = {
        trusted: ordinaryKey?.isTrusted,
        target: (ordinaryKey?.target as HTMLElement | null)?.id,
        focused: document.activeElement?.id,
        defaultPrevented: ordinaryKey?.defaultPrevented,
        copied: result.copied.length,
        terminalData: result.terminalData.length,
      };
      document.title = `XTERM:RESULT:${JSON.stringify({ ...result, trustedEvents, activeField: document.activeElement?.id, ordinaryBefore, ordinaryAfter })}`;
    }, 0);
  }
  else if (event.code === 'KeyC') document.title = 'XTERM:COPY-DONE';
  else if (event.code === 'KeyX') document.title = `XTERM:RESULT:${JSON.stringify(result)}`;
});
document.addEventListener('copy', (event) => {
  result.domCopyEvents++;
  // Safety net, NOT a product handler: count leaked defaults but never touch the
  // host clipboard, including the intentional ordinary-input copy control.
  if (mac) event.preventDefault();
});
document.addEventListener('paste', () => { result.domPasteEvents++; });

wireProductTerminalClipboard(
  terminal,
  mac ? 'darwin' : 'win32',
  async () => ({ kind: 'text', text: 'pasted once' }),
  async (text) => { result.copied.push(text); },
  (data) => {
    result.terminalData.push(data);
    if (data === 'pasted once') document.title = 'XTERM:PASTE-DATA';
    else if (data === 'x') document.title = 'XTERM:TYPE-DATA';
  },
);

terminal.write('selected text', () => {
  terminal.select(0, 0, 'selected text'.length);
  terminal.focus();
  document.title = 'XTERM:READY';
});
