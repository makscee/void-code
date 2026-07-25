const folderElement = document.querySelector<HTMLElement>('#folder')!;
const chooseButton = document.querySelector<HTMLButtonElement>('#choose')!;
const emptyChooseButton = document.querySelector<HTMLButtonElement>('#empty-choose')!;
const newChatButton = document.querySelector<HTMLButtonElement>('#new-chat')!;
const tabsElement = document.querySelector<HTMLElement>('#tabs')!;
const terminalsElement = document.querySelector<HTMLElement>('#terminals')!;
const emptyElement = document.querySelector<HTMLElement>('#empty')!;
const recoveryElement = document.querySelector<HTMLElement>('#recovery')!;
const recoveryPathElement = document.querySelector<HTMLElement>('#recovery-path')!;
const locateButton = document.querySelector<HTMLButtonElement>('#locate')!;
const removeWorkspaceButton = document.querySelector<HTMLButtonElement>('#remove-workspace')!;
const noticeElement = document.querySelector<HTMLElement>('#notice')!;
const recentElement = document.querySelector<HTMLElement>('#recent')!;
const recentListElement = document.querySelector<HTMLElement>('#recent-list')!;
const endedElement = document.querySelector<HTMLElement>('#ended')!;
const endedDetail = document.querySelector<HTMLElement>('#ended-detail')!;
const restartButton = document.querySelector<HTMLButtonElement>('#restart')!;
const closeEndedButton = document.querySelector<HTMLButtonElement>('#close-ended')!;

type Runtime = { terminal: InstanceType<typeof Terminal>; container: HTMLDivElement; offOutput: () => void; offExit: () => void; offStatus: () => void; exited: boolean };
const runtimes = new Map<string, Runtime>();
const chatStatuses = new Map<string, RendererChatStatus>();
let view: RendererWorkspaceView = { workspace: null, recoveryPath: null };

function announce(message: string): void { noticeElement.textContent = message; noticeElement.hidden = false; }
function dimensions(container: HTMLElement): { cols: number; rows: number } {
  return { cols: Math.max(2, Math.floor((container.clientWidth - 18) / 8.45)), rows: Math.max(1, Math.floor((container.clientHeight - 18) / 17)) };
}
function dispose(id: string): void {
  const runtime = runtimes.get(id); if (!runtime) return;
  runtime.offOutput(); runtime.offExit(); runtime.offStatus(); runtime.terminal.dispose(); runtime.container.remove(); runtimes.delete(id); chatStatuses.delete(id);
}
async function stop(id: string): Promise<void> { try { await window.voidTerminal.stop({ sessionId: id }); } catch { /* sleeping or exited */ } dispose(id); }
function selectedTab(): RendererTabRecord | undefined { return view.workspace?.tabs.find((tab) => tab.id === view.workspace?.selectedId); }

async function launch(tab: RendererTabRecord, mode: 'create' | 'resume'): Promise<void> {
  const workspace = view.workspace; if (!workspace || view.recoveryPath || runtimes.has(tab.id)) return;
  const container = document.createElement('div'); container.className = 'terminal'; container.hidden = tab.id !== workspace.selectedId; terminalsElement.append(container);
  const terminal = new Terminal({ cursorBlink: true, scrollback: 10_000, fontSize: 14, fontFamily: 'SFMono-Regular, Menlo, Monaco, Consolas, monospace', theme: { background: '#101216', foreground: '#e8eaed', cursor: '#d3d7de', selectionBackground: '#41506a' }, linkHandler: { activate: (_event: MouseEvent, text: string) => { void window.voidTerminal.openLink(text); } } });
  terminal.open(container); terminal.onData((data: string) => { void window.voidTerminal.input({ sessionId: tab.id, data }); });
  let offOutput = (): void => undefined; let offExit = (): void => undefined; let offStatus = (): void => undefined;
  const runtime: Runtime = { terminal, container, offOutput, offExit, offStatus, exited: false }; runtimes.set(tab.id, runtime);
  try {
    const started = await window.voidTerminal.start({ sessionId: tab.id, cwd: workspace.path, mode });
    if (started.showSharedFilesWarning) announce('These chats share the same folder and can edit the same files. This is not isolation; use another worktree or window when changes may conflict.');
    offOutput = window.voidTerminal.onOutput(tab.id, ({ data }) => terminal.write(data));
    offExit = window.voidTerminal.onExit(tab.id, ({ exitCode, signal }) => {
      runtime.exited = true;
      if (view.workspace?.selectedId === tab.id) { endedElement.hidden = false; container.hidden = true; endedDetail.textContent = `The Pi process ended (${signal === undefined ? `exit ${exitCode}` : `signal ${signal}`}). Restart resumes this chat; no shell was opened.`; }
    });
    runtime.offOutput = offOutput; runtime.offExit = offExit;
    chatStatuses.set(tab.id, (await window.voidTerminal.lifecycleStatus({ sessionId: tab.id })).status);
    offStatus = window.voidTerminal.onStatus(tab.id, (status) => { chatStatuses.set(tab.id, status); render(); });
    runtime.offStatus = offStatus;
    const size = dimensions(container); terminal.resize(size.cols, size.rows); await window.voidTerminal.resize({ sessionId: tab.id, ...size });
    if (!container.hidden) terminal.focus();
  } catch (error) {
    runtime.exited = true; container.hidden = true; endedElement.hidden = false;
    const message = error instanceof Error ? error.message : String(error);
    endedDetail.textContent = message.includes('SESSION_MISSING') ? 'The saved Pi session is unavailable. Close it and start a new chat.' : message;
    restartButton.hidden = message.includes('SESSION_MISSING');
  }
}

function render(): void {
  const workspace = view.workspace; const recovering = Boolean(view.recoveryPath);
  folderElement.textContent = workspace?.path ?? 'No folder selected';
  chooseButton.hidden = Boolean(workspace && !recovering); newChatButton.hidden = !workspace || recovering;
  emptyElement.hidden = Boolean(workspace); recoveryElement.hidden = !recovering; tabsElement.hidden = !workspace || recovering;
  recoveryPathElement.textContent = view.recoveryPath ?? '';
  tabsElement.replaceChildren(); recentListElement.replaceChildren();
  if (!workspace || recovering) { recentElement.hidden = true; endedElement.hidden = true; for (const runtime of runtimes.values()) runtime.container.hidden = true; return; }
  const active = workspace.tabs.filter((tab) => tab.location === 'active'); const recent = workspace.tabs.filter((tab) => tab.location === 'recent');
  for (const tab of active) {
    const item = document.createElement('div'); item.className = `tab${tab.id === workspace.selectedId ? ' selected' : ''}`;
    const status = chatStatuses.get(tab.id);
    const badge = status ? `${status.state === 'running' ? 'Running' : status.state === 'working' ? 'Working' : 'Ready'}${status.unread ? ' •' : ''}` : (runtimes.has(tab.id) ? 'Running' : 'Sleeping');
    const select = document.createElement('button'); select.textContent = `${tab.title}  ${badge}`; select.title = status?.diagnostic ?? (runtimes.has(tab.id) ? 'Chat process active' : 'Sleeping — select to resume');
    select.addEventListener('click', () => { void selectChat(tab.id); });
    const close = document.createElement('button'); close.className = 'tab-close'; close.textContent = '×'; close.setAttribute('aria-label', `Close ${tab.title}`); close.addEventListener('click', () => { void closeChat(tab.id); });
    item.append(select, close); tabsElement.append(item);
  }
  for (const runtime of runtimes.values()) runtime.container.hidden = true;
  const selected = workspace.selectedId ? runtimes.get(workspace.selectedId) : undefined;
  if (selected && !selected.exited) { selected.container.hidden = false; selected.terminal.focus(); endedElement.hidden = true; }
  else endedElement.hidden = !workspace.selectedId;
  recentElement.hidden = recent.length === 0;
  for (const tab of recent) { const row = document.createElement('div'); row.className = 'recent-row'; const title = document.createElement('span'); title.textContent = tab.title; const resume = document.createElement('button'); resume.textContent = 'Resume'; resume.addEventListener('click', () => { void resumeChat(tab.id); }); row.append(title, resume); recentListElement.append(row); }
}
async function selectChat(id: string): Promise<void> { view = await window.voidTerminal.workspace.select(id); const status = chatStatuses.get(id); if (status) chatStatuses.set(id, { ...status, unread: false }); render(); const tab = selectedTab(); if (tab && !runtimes.has(id)) await launch(tab, 'resume'); else if (tab) chatStatuses.set(id, (await window.voidTerminal.lifecycleStatus({ sessionId: id })).status); render(); }
async function closeChat(id: string): Promise<void> { await stop(id); view = await window.voidTerminal.workspace.close(id); render(); const tab = selectedTab(); if (tab && !runtimes.has(tab.id)) await launch(tab, 'resume'); render(); }
async function resumeChat(id: string): Promise<void> { view = await window.voidTerminal.workspace.resume(id); render(); const tab = selectedTab(); if (tab) await launch(tab, 'resume'); render(); }
async function chooseFolder(): Promise<void> {
  const chosen = await window.voidTerminal.workspace.choose(); if (!chosen) return; view = chosen;
  announce('Trusted-folder prototype: Pi can read and change files in this folder using your operating-system permissions. Existing VC authentication is used.'); render();
}

chooseButton.addEventListener('click', () => { void chooseFolder(); }); emptyChooseButton.addEventListener('click', () => { void chooseFolder(); }); locateButton.addEventListener('click', () => { void chooseFolder(); });
removeWorkspaceButton.addEventListener('click', async () => { for (const id of [...runtimes.keys()]) await stop(id); view = await window.voidTerminal.workspace.remove(); render(); });
newChatButton.addEventListener('click', async () => { const reply = await window.voidTerminal.workspace.newChat(); view = reply.view; render(); const tab = selectedTab(); if (tab) await launch(tab, 'create'); render(); });
restartButton.addEventListener('click', async () => { const tab = selectedTab(); if (!tab) return; restartButton.hidden = false; await stop(tab.id); endedElement.hidden = true; await launch(tab, 'resume'); render(); });
closeEndedButton.addEventListener('click', () => { const tab = selectedTab(); if (tab) void closeChat(tab.id); });
new ResizeObserver(() => { const tab = selectedTab(); const runtime = tab ? runtimes.get(tab.id) : undefined; if (!runtime || runtime.container.hidden || runtime.exited) return; const size = dimensions(runtime.container); runtime.terminal.resize(size.cols, size.rows); void window.voidTerminal.resize({ sessionId: tab!.id, ...size }); }).observe(terminalsElement);

void window.voidTerminal.workspace.load().then(async (loaded) => { view = loaded; render(); const tab = selectedTab(); if (tab && !view.recoveryPath) await launch(tab, 'resume'); render(); });
