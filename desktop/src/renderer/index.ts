const terminalElement = document.querySelector<HTMLDivElement>('#terminal')!;
const emptyElement = document.querySelector<HTMLElement>('#empty')!;
const endedElement = document.querySelector<HTMLElement>('#ended')!;
const endedTitle = document.querySelector<HTMLElement>('#ended-title')!;
const endedDetail = document.querySelector<HTMLElement>('#ended-detail')!;
const folderElement = document.querySelector<HTMLElement>('#folder')!;
const chooseButton = document.querySelector<HTMLButtonElement>('#choose')!;
const restartButton = document.querySelector<HTMLButtonElement>('#restart')!;
const closeButton = document.querySelector<HTMLButtonElement>('#close')!;

const terminal = new Terminal({
  cursorBlink: true, convertEol: false, scrollback: 10_000, fontSize: 14,
  fontFamily: 'SFMono-Regular, Menlo, Monaco, Consolas, monospace',
  theme: { background: '#101216', foreground: '#e8eaed', cursor: '#d3d7de', selectionBackground: '#41506a' },
  linkHandler: { activate: (_event: MouseEvent, text: string) => { void window.voidTerminal.openLink(text); } },
});
terminal.open(terminalElement);
const stateKey = 'void-code-active-chat-v1';
let session: { id: string; cwd: string } | undefined;
let offOutput: (() => void) | undefined;
let offExit: (() => void) | undefined;

function subscriptionsOff(): void { offOutput?.(); offExit?.(); offOutput = undefined; offExit = undefined; }
function dimensions(): { cols: number; rows: number } {
  return { cols: Math.max(2, Math.floor((terminalElement.clientWidth - 18) / 8.45)), rows: Math.max(1, Math.floor((terminalElement.clientHeight - 18) / 17)) };
}
async function resize(): Promise<void> {
  if (!session || terminalElement.hidden) return;
  const size = dimensions(); terminal.resize(size.cols, size.rows); await window.voidTerminal.resize({ sessionId: session.id, ...size });
}
async function launch(mode: 'create' | 'resume'): Promise<void> {
  if (!session) return;
  subscriptionsOff(); terminal.reset(); emptyElement.hidden = true; endedElement.hidden = true; terminalElement.hidden = false;
  try {
    await window.voidTerminal.start({ sessionId: session.id, cwd: session.cwd, mode });
    offOutput = window.voidTerminal.onOutput(session.id, ({ data }) => terminal.write(data));
    offExit = window.voidTerminal.onExit(session.id, ({ exitCode, signal }) => {
      subscriptionsOff(); terminalElement.hidden = true; endedElement.hidden = false;
      endedTitle.textContent = exitCode === 0 ? 'Pi exited' : 'Pi stopped';
      endedDetail.textContent = `The Pi process ended (${signal === undefined ? `exit ${exitCode}` : `signal ${signal}`}). Restart resumes this chat; no shell was opened.`;
    });
    await resize(); terminal.focus();
  } catch (error) {
    subscriptionsOff(); terminalElement.hidden = true; endedElement.hidden = false;
    const message = error instanceof Error ? error.message : String(error);
    endedTitle.textContent = message.includes('SESSION_MISSING') ? 'Saved chat missing' : 'Pi could not start';
    endedDetail.textContent = message.includes('SESSION_MISSING') ? 'The persisted Pi session is unavailable. Close this chat and start a new one.' : message;
    restartButton.hidden = message.includes('SESSION_MISSING');
  }
}
chooseButton.addEventListener('click', async () => {
  const cwd = await window.voidTerminal.chooseFolder(); if (!cwd) return;
  session = { id: crypto.randomUUID(), cwd }; localStorage.setItem(stateKey, JSON.stringify(session));
  folderElement.textContent = cwd; restartButton.hidden = false; await launch('create');
});
restartButton.addEventListener('click', async () => {
  if (!session) return;
  try { await window.voidTerminal.stop({ sessionId: session.id }); } catch { /* an exited process may already be gone */ }
  await launch('resume');
});
closeButton.addEventListener('click', async () => {
  if (session) { try { await window.voidTerminal.stop({ sessionId: session.id }); } catch { /* already gone */ } }
  subscriptionsOff(); session = undefined; localStorage.removeItem(stateKey); terminal.reset(); terminalElement.hidden = true; endedElement.hidden = true; emptyElement.hidden = false; folderElement.textContent = 'Choose a folder to start one chat';
});
terminal.onData((data: string) => { if (session) void window.voidTerminal.input({ sessionId: session.id, data }); });
new ResizeObserver(() => { void resize(); }).observe(terminalElement);
try {
  const saved = JSON.parse(localStorage.getItem(stateKey) ?? 'null') as unknown;
  if (typeof saved === 'object' && saved !== null && Object.keys(saved).length === 2 && typeof (saved as Record<string, unknown>).id === 'string' && typeof (saved as Record<string, unknown>).cwd === 'string') {
    session = { id: (saved as { id: string }).id, cwd: (saved as { cwd: string }).cwd };
    folderElement.textContent = session.cwd; restartButton.hidden = false; void launch('resume');
  }
} catch { localStorage.removeItem(stateKey); }
