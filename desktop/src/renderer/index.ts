import { Terminal } from '@xterm/xterm';
import { activateProductRenderer, createProductTerminal, TERMINAL_OPTIONS, TERMINAL_THEME, type ProductTerminal } from './terminal-stack';
import { RECOVERY_GUIDANCE } from './recovery';
import { loginCompletionAction } from './login-retry';
import type { LoginStatus, RecoveryCode, RuntimeSupportState, SupportRequest } from '../shared/contract';
const folderElement = document.querySelector<HTMLElement>('#folder')!;
const chooseButton = document.querySelector<HTMLButtonElement>('#choose')!;
const emptyChooseButton = document.querySelector<HTMLButtonElement>('#empty-choose')!;
const newChatButton = document.querySelector<HTMLButtonElement>('#new-chat')!;
const supportToggleButton = document.querySelector<HTMLButtonElement>('#support-toggle')!;
const supportPanel = document.querySelector<HTMLElement>('#support-panel')!;
const supportCopyButton = document.querySelector<HTMLButtonElement>('#support-copy')!;
const supportSaveButton = document.querySelector<HTMLButtonElement>('#support-save')!;
const supportCloseButton = document.querySelector<HTMLButtonElement>('#support-close')!;
const tabsElement = document.querySelector<HTMLElement>('#tabs')!;
const terminalsElement = document.querySelector<HTMLElement>('#terminals')!;
const mainElement = document.querySelector<HTMLElement>('main')!;
const emptyElement = document.querySelector<HTMLElement>('#empty')!;
const preflightElement = document.querySelector<HTMLElement>('#preflight')!;
const recoveryElement = document.querySelector<HTMLElement>('#recovery')!;
const recoveryPathElement = document.querySelector<HTMLElement>('#recovery-path')!;
const locateButton = document.querySelector<HTMLButtonElement>('#locate')!;
const removeWorkspaceButton = document.querySelector<HTMLButtonElement>('#remove-workspace')!;
const noticeElement = document.querySelector<HTMLElement>('#notice')!;
const recentElement = document.querySelector<HTMLElement>('#recent')!;
const recentListElement = document.querySelector<HTMLElement>('#recent-list')!;
const recentToggleButton = document.querySelector<HTMLButtonElement>('#recent-toggle')!;
const recentCloseButton = document.querySelector<HTMLButtonElement>('#recent-close')!;
const endedElement = document.querySelector<HTMLElement>('#ended')!;
const endedHeading = document.querySelector<HTMLElement>('#ended-heading')!;
const endedDetail = document.querySelector<HTMLElement>('#ended-detail')!;
const restartButton = document.querySelector<HTMLButtonElement>('#restart')!;
const closeEndedButton = document.querySelector<HTMLButtonElement>('#close-ended')!;
const loginStartButtons = [...document.querySelectorAll<HTMLButtonElement>('.login-start')];
const loginCancelButtons = [...document.querySelectorAll<HTMLButtonElement>('.login-cancel')];
const loginStatusElements = [...document.querySelectorAll<HTMLElement>('.login-status')];

type Runtime = ProductTerminal & { container: HTMLDivElement; offOutput: () => void; offExit: () => void; offStatus: () => void; exited: boolean; recoveryCode?: RecoveryCode };
const runtimes = new Map<string, Runtime>();
const chatStatuses = new Map<string, RendererChatStatus>();
let view: RendererWorkspaceView = { workspace: null, recoveryPath: null };
let recentOpen = false;
let currentRecovery: RecoveryCode = 'AUTH_PREFLIGHT_REQUIRED';
let completingLogin = false;

function announce(message: string): void { noticeElement.textContent = message; noticeElement.hidden = false; }
function presentLoginStatus(status: LoginStatus): void {
  const text = status.state === 'running' ? 'Waiting for sign-in approval in your browser.'
    : status.state === 'succeeded' ? 'Sign-in complete. Starting your chat…'
      : status.state === 'failed' ? 'Sign-in did not complete. Try again.'
        : status.state === 'cancelled' ? 'Sign-in cancelled.' : 'Sign-in is not started.';
  for (const element of loginStatusElements) element.textContent = text;
  for (const button of loginStartButtons) button.disabled = status.state === 'running';
  for (const button of loginCancelButtons) button.hidden = status.state !== 'running';
}
async function completeLogin(status: LoginStatus): Promise<void> {
  presentLoginStatus(status);
  const action = loginCompletionAction(status, Boolean(selectedTab()));
  if (action === 'none' || completingLogin) return;
  completingLogin = true;
  try {
    if (action === 'retry') {
      const tab = selectedTab(); if (!tab) return;
      await stop(tab.id); endedElement.hidden = true; await launch(tab, 'resume'); render();
    } else {
      const reply = await window.voidTerminal.workspace.newChat(); view = reply.view; render();
      const tab = selectedTab(); if (tab) await launch(tab, 'create'); render();
    }
  } finally { completingLogin = false; }
}
function showEnded(code: Exclude<RecoveryCode, 'NONE' | 'AUTH_PREFLIGHT_REQUIRED' | 'WORKSPACE_MISSING'>, runtime: Runtime): void {
  currentRecovery = code; runtime.recoveryCode = code;
  const guidance = RECOVERY_GUIDANCE[code];
  endedHeading.textContent = guidance.heading; endedDetail.textContent = guidance.detail;
  restartButton.hidden = !guidance.canRestart; endedElement.hidden = false;
}
function supportContext(): SupportRequest {
  const tab = selectedTab(); const runtime = tab ? runtimes.get(tab.id) : undefined;
  const recoveryCode = runtime?.recoveryCode ?? currentRecovery;
  const state: RuntimeSupportState = recoveryCode === 'SESSION_START_FAILED' ? 'start_failed' : runtime?.exited ? 'ended' : runtime ? 'running' : 'not_started';
  return { runtime: state, recoveryCode };
}
function setSupportOpen(open: boolean): void {
  supportPanel.hidden = !open; supportToggleButton.setAttribute('aria-expanded', String(open));
  if (open) supportCopyButton.focus(); else supportToggleButton.focus();
}
function setRecentOpen(open: boolean, focusTarget = true): void {
  const hasRecent = Boolean(view.workspace?.tabs.some((tab) => tab.location === 'recent'));
  recentOpen = open && hasRecent;
  document.body.classList.toggle('recent-open', recentOpen);
  recentElement.hidden = !recentOpen;
  recentToggleButton.setAttribute('aria-expanded', String(recentOpen));
  fitAfterLayout();
  if (focusTarget) requestAnimationFrame(() => (recentOpen ? recentCloseButton : recentToggleButton).focus());
}
async function fitRuntime(id: string, runtime: Runtime): Promise<void> {
  runtime.fit.fit();
  await window.voidTerminal.resize({ sessionId: id, cols: runtime.terminal.cols, rows: runtime.terminal.rows });
}
let pendingLayoutFit = 0;
function fitAfterLayout(): void {
  cancelAnimationFrame(pendingLayoutFit);
  pendingLayoutFit = requestAnimationFrame(() => {
    const tab = selectedTab(); const runtime = tab ? runtimes.get(tab.id) : undefined;
    if (tab && runtime && !runtime.container.hidden && !runtime.exited) void fitRuntime(tab.id, runtime);
  });
}
function dispose(id: string): void {
  const runtime = runtimes.get(id); if (!runtime) return;
  runtime.offOutput(); runtime.offExit(); runtime.offStatus(); runtime.disposeRenderer(); runtime.terminal.dispose(); runtime.container.remove(); runtimes.delete(id); chatStatuses.delete(id);
}
async function stop(id: string): Promise<void> { try { await window.voidTerminal.stop({ sessionId: id }); } catch { /* sleeping or exited */ } dispose(id); }
function selectedTab(): RendererTabRecord | undefined { return view.workspace?.tabs.find((tab) => tab.id === view.workspace?.selectedId); }

async function launch(tab: RendererTabRecord, mode: 'create' | 'resume'): Promise<void> {
  const workspace = view.workspace; if (!workspace || view.recoveryPath || runtimes.has(tab.id)) return;
  const container = document.createElement('div'); container.className = 'terminal'; container.hidden = tab.id !== workspace.selectedId; terminalsElement.append(container);
  const created = createProductTerminal({ activate: (_event: MouseEvent, text: string) => { void window.voidTerminal.openLink(text); } });
  const { terminal } = created;
  terminal.open(container); activateProductRenderer(created); terminal.onData((data: string) => { void window.voidTerminal.input({ sessionId: tab.id, data }); });
  let offOutput = (): void => undefined; let offExit = (): void => undefined; let offStatus = (): void => undefined;
  const runtime = Object.assign(created, { container, offOutput, offExit, offStatus, exited: false, recoveryCode: 'NONE' as RecoveryCode }) as Runtime; runtimes.set(tab.id, runtime);
  try {
    currentRecovery = 'NONE';
    const started = await window.voidTerminal.start({ sessionId: tab.id, cwd: workspace.path, mode });
    if (started.showSharedFilesWarning) announce('These chats share the same folder and can edit the same files. This is not isolation; use another worktree or window when changes may conflict.');
    offOutput = window.voidTerminal.onOutput(tab.id, ({ data }) => terminal.write(data));
    offExit = window.voidTerminal.onExit(tab.id, () => {
      runtime.exited = true;
      if (view.workspace?.selectedId === tab.id) { container.hidden = true; showEnded('RUNTIME_EXITED', runtime); }
    });
    runtime.offOutput = offOutput; runtime.offExit = offExit;
    chatStatuses.set(tab.id, (await window.voidTerminal.lifecycleStatus({ sessionId: tab.id })).status);
    offStatus = window.voidTerminal.onStatus(tab.id, (status) => { chatStatuses.set(tab.id, status); render(); window.__resolveProductionStatus?.(status.sessionId); });
    runtime.offStatus = offStatus;
    await fitRuntime(tab.id, runtime);
    if (!container.hidden) terminal.focus();
  } catch (error) {
    runtime.exited = true; container.hidden = true;
    const code = error instanceof Error && error.message.includes('SESSION_MISSING') ? 'SESSION_MISSING' : 'SESSION_START_FAILED';
    showEnded(code, runtime);
  }
}

function render(): void {
  const focusedRecentControl = recentOpen && recentElement.contains(document.activeElement);
  const focusedRecentChatId = document.activeElement instanceof HTMLButtonElement ? document.activeElement.closest<HTMLElement>('.recent-row')?.dataset.chatId : undefined;
  const workspace = view.workspace; const recovering = Boolean(view.recoveryPath);
  folderElement.textContent = recovering ? 'Workspace unavailable' : workspace?.path ?? 'No folder selected';
  chooseButton.hidden = Boolean(workspace && !recovering); newChatButton.hidden = !workspace || recovering;
  emptyElement.hidden = Boolean(workspace); preflightElement.hidden = !workspace || recovering || Boolean(workspace.selectedId); recoveryElement.hidden = !recovering; tabsElement.hidden = !workspace || recovering;
  recoveryPathElement.textContent = recovering ? 'The previously selected folder cannot be found.' : '';
  tabsElement.replaceChildren(); recentListElement.replaceChildren();
  if (!workspace || recovering) { currentRecovery = recovering ? 'WORKSPACE_MISSING' : 'AUTH_PREFLIGHT_REQUIRED'; recentToggleButton.hidden = true; setRecentOpen(false, false); endedElement.hidden = true; for (const runtime of runtimes.values()) runtime.container.hidden = true; fitAfterLayout(); return; }
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
  if (selected && !selected.exited) { currentRecovery = 'NONE'; selected.container.hidden = false; if (!focusedRecentControl) selected.terminal.focus(); endedElement.hidden = true; }
  else if (selected?.exited) {
    const code = selected.recoveryCode;
    showEnded(code === 'SESSION_START_FAILED' || code === 'SESSION_MISSING' || code === 'RUNTIME_EXITED' ? code : 'RUNTIME_EXITED', selected);
  } else { endedElement.hidden = !workspace.selectedId; currentRecovery = workspace.selectedId ? 'NONE' : 'AUTH_PREFLIGHT_REQUIRED'; }
  recentToggleButton.hidden = recent.length === 0;
  recentToggleButton.disabled = recent.length === 0;
  recentToggleButton.textContent = `Recent Chats (${recent.length})`;
  recentToggleButton.setAttribute('aria-label', `Recent Chats, ${recent.length} chat${recent.length === 1 ? '' : 's'}`);
  if (recent.length === 0) setRecentOpen(false, false); else recentElement.hidden = !recentOpen;
  for (const tab of recent) { const row = document.createElement('div'); row.className = 'recent-row'; row.dataset.chatId = tab.id; const title = document.createElement('span'); title.textContent = tab.title; const resume = document.createElement('button'); resume.textContent = 'Resume'; resume.setAttribute('aria-label', `Resume ${tab.title}`); resume.addEventListener('click', () => { void resumeChat(tab.id); }); row.append(title, resume); recentListElement.append(row); }
  if (focusedRecentChatId && recentOpen) {
    const resume = [...recentListElement.querySelectorAll<HTMLButtonElement>('.recent-row button')].find((button) => button.closest<HTMLElement>('.recent-row')?.dataset.chatId === focusedRecentChatId);
    resume?.focus({ preventScroll: true });
  }
  fitAfterLayout();
}
async function selectChat(id: string): Promise<void> { view = await window.voidTerminal.workspace.select(id); const status = chatStatuses.get(id); if (status) chatStatuses.set(id, { ...status, unread: false }); render(); const tab = selectedTab(); if (tab && !runtimes.has(id)) await launch(tab, 'resume'); else if (tab) { const runtime = runtimes.get(id); if (runtime) await fitRuntime(id, runtime); chatStatuses.set(id, (await window.voidTerminal.lifecycleStatus({ sessionId: id })).status); } render(); }
async function closeChat(id: string): Promise<void> { await stop(id); view = await window.voidTerminal.workspace.close(id); render(); const tab = selectedTab(); if (tab && !runtimes.has(tab.id)) await launch(tab, 'resume'); render(); }
async function resumeChat(id: string): Promise<void> { view = await window.voidTerminal.workspace.resume(id); if (matchMedia('(max-width: 760px)').matches) setRecentOpen(false, false); render(); const tab = selectedTab(); if (tab) await launch(tab, 'resume'); render(); const runtime = runtimes.get(id); if (runtime && !runtime.exited) runtime.terminal.focus(); else (restartButton.hidden ? closeEndedButton : restartButton).focus(); fitAfterLayout(); }
async function chooseFolder(): Promise<void> {
  const chosen = await window.voidTerminal.workspace.choose(); if (!chosen) return; view = chosen;
  announce('Trusted folder: Pi can read and change files in this folder using your operating-system permissions. Before the first chat, ask your operator to confirm existing VC sign-in and network access.'); render();
}

chooseButton.addEventListener('click', () => { void chooseFolder(); }); emptyChooseButton.addEventListener('click', () => { void chooseFolder(); }); locateButton.addEventListener('click', () => { void chooseFolder(); });
supportToggleButton.addEventListener('click', () => { setSupportOpen(supportPanel.hidden); });
supportCloseButton.addEventListener('click', () => { setSupportOpen(false); });
supportCopyButton.addEventListener('click', async () => { const result = await window.voidTerminal.support.copy(supportContext()); announce(result.action === 'copied' ? 'Support Report copied. Review it before sharing.' : 'Support Report was not copied.'); });
supportSaveButton.addEventListener('click', async () => { const result = await window.voidTerminal.support.save(supportContext()); announce(result.action === 'saved' ? 'Support Report saved. Review it before sharing.' : 'Support Report save cancelled.'); });
recentToggleButton.addEventListener('click', () => { setRecentOpen(!recentOpen); });
recentCloseButton.addEventListener('click', () => { setRecentOpen(false); });
document.addEventListener('keydown', (event) => { if (event.key === 'Escape' && recentOpen) { event.preventDefault(); setRecentOpen(false); } });
removeWorkspaceButton.addEventListener('click', async () => { for (const id of [...runtimes.keys()]) await stop(id); view = await window.voidTerminal.workspace.remove(); render(); });
newChatButton.addEventListener('click', async () => { const reply = await window.voidTerminal.workspace.newChat(); view = reply.view; render(); const tab = selectedTab(); if (tab) await launch(tab, 'create'); render(); });
restartButton.addEventListener('click', async () => { const tab = selectedTab(); if (!tab) return; restartButton.hidden = false; await stop(tab.id); endedElement.hidden = true; await launch(tab, 'resume'); render(); });
closeEndedButton.addEventListener('click', () => { const tab = selectedTab(); if (tab) void closeChat(tab.id); });
for (const button of loginStartButtons) button.addEventListener('click', () => { void window.voidTerminal.login.start().then((status) => completeLogin(status)); });
for (const button of loginCancelButtons) button.addEventListener('click', () => { void window.voidTerminal.login.cancel().then((status) => completeLogin(status)); });
window.voidTerminal.login.onStatus((status) => { void completeLogin(status); });
void window.voidTerminal.login.status().then((status) => completeLogin(status));
new ResizeObserver(() => { const tab = selectedTab(); const runtime = tab ? runtimes.get(tab.id) : undefined; if (!runtime || runtime.container.hidden || runtime.exited) return; void fitRuntime(tab!.id, runtime); }).observe(mainElement);

type ByteFacts = { bytes: number; chunks: number; escBytes: number };
type VisibleColorFacts = { source: 'dom' | 'cdp-bitmap'; distinctVisibleRgb: number; contrastingPixels: number; maxContrast: number; chromaticHueBins: number; visibleRgb: string[] };
function emptyByteFacts(): ByteFacts { return { bytes: 0, chunks: 0, escBytes: 0 }; }
function addByteFacts(facts: ByteFacts, data: string): void {
  facts.bytes += new TextEncoder().encode(data).length; facts.chunks++;
  for (let index = 0; index < data.length; index++) if (data.charCodeAt(index) === 27) facts.escBytes++;
}
function luminance(red: number, green: number, blue: number): number {
  const channel = (value: number) => { const normalized = value / 255; return normalized <= 0.04045 ? normalized / 12.92 : ((normalized + 0.055) / 1.055) ** 2.4; };
  return 0.2126 * channel(red) + 0.7152 * channel(green) + 0.0722 * channel(blue);
}
function contrast(first: number[], second: number[]): number {
  const a = luminance(first[0], first[1], first[2]); const b = luminance(second[0], second[1], second[2]);
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}
function rgb(value: string): number[] | undefined {
  const numbers = value.startsWith('rgb(') ? value.slice(4, -1).split(',').map(Number) : [];
  return numbers.length === 3 && numbers.every(Number.isFinite) ? numbers : undefined;
}
function domVisibleColorFacts(runtime: Runtime): VisibleColorFacts | undefined {
  const background = rgb(getComputedStyle(runtime.container).backgroundColor) ?? [15, 17, 23]; const colors = new Map<string, number>(); let maxContrast = 0;
  for (const span of runtime.container.querySelectorAll<HTMLElement>('.xterm-rows span')) {
    const parsed = rgb(getComputedStyle(span).color); if (!parsed) continue;
    const ratio = contrast(parsed, background); if (ratio < 2) continue;
    const key = parsed.join(','); colors.set(key, (colors.get(key) ?? 0) + 1); maxContrast = Math.max(maxContrast, ratio);
  }
  if (colors.size === 0) return undefined;
  const visibleRgb = [...colors.entries()].filter(([, count]) => count >= 2).map(([color]) => color);
  return { source: 'dom', distinctVisibleRgb: visibleRgb.length, contrastingPixels: [...colors.values()].reduce((sum, count) => sum + count, 0), maxContrast, chromaticHueBins: visibleRgb.length, visibleRgb: visibleRgb.slice(0, 12) };
}
async function visibleColorFacts(runtime: Runtime): Promise<VisibleColorFacts> {
  if (runtime.renderer === 'dom') { const facts = domVisibleColorFacts(runtime); if (facts) return facts; }
  const screen = runtime.container.querySelector<HTMLElement>('.xterm-screen') ?? runtime.container; const bounds = screen.getBoundingClientRect(); const requestId = crypto.randomUUID();
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => { delete window.__resolveProductionPixelProbe; reject(new Error('production pixel probe timed out')); }, 5000);
    window.__resolveProductionPixelProbe = (reply) => { if (reply.requestId !== requestId) return; clearTimeout(timeout); delete window.__resolveProductionPixelProbe; resolve(reply.summary); };
    document.title = `VOID_PRODUCTION_PIXEL_REQUEST:${JSON.stringify({ requestId, clip: { x: bounds.x, y: bounds.y, width: bounds.width, height: bounds.height }, background: TERMINAL_THEME.background })}`;
  });
}
function renderedFacts(runtime: Runtime) {
  const terminal = runtime.terminal; const foregrounds = new Set<string>();
  let styled = 0; let nonDefaultForeground = 0; let bold = 0; let italic = 0; let underline = 0; let inverse = 0;
  for (let row = 0; row < terminal.rows; row++) {
    const line = terminal.buffer.active.getLine(row); if (!line) continue;
    for (let column = 0; column < terminal.cols; column++) {
      const cell = line.getCell(column); if (!cell || cell.getChars() === '') continue;
      if (!cell.isFgDefault()) { nonDefaultForeground++; foregrounds.add(`${cell.isFgRGB() ? 'rgb' : 'palette'}:${cell.getFgColor()}`); }
      if (cell.isBold()) bold++; if (cell.isItalic()) italic++; if (cell.isUnderline()) underline++; if (cell.isInverse()) inverse++;
      if (cell.isBold() || cell.isDim() || cell.isItalic() || cell.isUnderline() || cell.isInverse() || cell.isStrikethrough()) styled++;
    }
  }
  return { nonDefaultForeground, distinctForegrounds: foregrounds.size, styled, bold, italic, underline, inverse };
}
function integrationFacts(runtime: Runtime) {
  const probe = document.createElement('span'); probe.style.fontFamily = runtime.terminal.options.fontFamily!; probe.style.fontSize = `${runtime.terminal.options.fontSize}px`; document.body.append(probe);
  const computedFamily = getComputedStyle(probe).fontFamily; const canvas = document.createElement('canvas'); const context = canvas.getContext('2d')!;
  context.font = `${runtime.terminal.options.fontWeight} ${runtime.terminal.options.fontSize}px ${computedFamily}`;
  const narrow = context.measureText('iiiiiiii').width; const wide = context.measureText('WWWWWWWW').width; probe.remove();
  return {
    xterm: { implementation: '@xterm/xterm', instance: runtime.terminal instanceof Terminal, renderer: runtime.renderer, rows: runtime.terminal.rows, cols: runtime.terminal.cols },
    css: [...document.styleSheets].map((sheet) => sheet.href ? new URL(sheet.href).pathname.split('/').pop() : 'inline'),
    font: { configured: runtime.terminal.options.fontFamily, computed: computedFamily, loaded: runtime.terminal.options.fontFamily?.includes('JetBrains Mono') === true && document.fonts.check(`400 14px "JetBrains Mono"`), narrow, wide, equalWidth: Math.abs(narrow - wide) < 0.01 },
    palette: { foreground: TERMINAL_THEME.foreground, background: TERMINAL_THEME.background, ansiEntries: 16 },
    options: { fontSize: TERMINAL_OPTIONS.fontSize, fontWeight: TERMINAL_OPTIONS.fontWeight, fontWeightBold: TERMINAL_OPTIONS.fontWeightBold, drawBoldTextInBrightColors: TERMINAL_OPTIONS.drawBoldTextInBrightColors, scrollback: TERMINAL_OPTIONS.scrollback },
  };
}
function nextFrame(): Promise<void> { return new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))); }
async function setProbeViewport(width: number, height: number): Promise<void> {
  const requestId = crypto.randomUUID();
  await new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => { delete window.__resolveProductionViewport; reject(new Error('production viewport probe timed out')); }, 5000);
    window.__resolveProductionViewport = (reply) => { if (reply.requestId !== requestId) return; clearTimeout(timeout); delete window.__resolveProductionViewport; resolve(); };
    document.title = `VOID_PRODUCTION_VIEWPORT_REQUEST:${JSON.stringify({ requestId, width, height })}`;
  });
  await nextFrame();
}
async function requestProductionStatus(sessionId: string): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => { delete window.__resolveProductionStatus; reject(new Error('production status probe timed out')); }, 5000);
    window.__resolveProductionStatus = (observedId) => { if (observedId !== sessionId) return; clearTimeout(timeout); delete window.__resolveProductionStatus; resolve(); };
    document.title = `VOID_PRODUCTION_STATUS_REQUEST:${JSON.stringify({ sessionId })}`;
  });
  await nextFrame();
}
function inside(inner: DOMRect, outer: DOMRect): boolean {
  return inner.top >= outer.top - 1 && inner.bottom <= outer.bottom + 1 && inner.left >= outer.left - 1 && inner.right <= outer.right + 1;
}
async function recentGeometryProbe(tab: RendererTabRecord, runtime: Runtime) {
  await setProbeViewport(1100, 760); render(); await nextFrame();
  const empty = {
    panelWidth: recentElement.getBoundingClientRect().width,
    toggleHidden: recentToggleButton.hidden,
    bodyColumns: getComputedStyle(document.body).gridTemplateColumns,
  };
  const cases = [{ name: 'tall', width: 1100, height: 1000 }, { name: 'normal', width: 1100, height: 760 }, { name: 'short', width: 1100, height: 560 }, { name: 'narrow', width: 640, height: 760 }];
  const fixtures: Array<Record<string, unknown>> = [];
  for (const rowCount of [1, 8]) {
    while (view.workspace!.tabs.filter((item) => item.location === 'recent').length < rowCount) {
      const created = await window.voidTerminal.workspace.newChat(); view = created.view;
      view = await window.voidTerminal.workspace.close(view.workspace!.selectedId!);
    }
    render(); await nextFrame();
    for (const fixture of cases) {
      await setProbeViewport(fixture.width, fixture.height); setRecentOpen(false, false); render(); await nextFrame(); await fitRuntime(tab.id, runtime);
      const closedTerminal = runtime.container.getBoundingClientRect(); const closedFit = { cols: runtime.terminal.cols, rows: runtime.terminal.rows };
      const closedPanelWidth = recentElement.getBoundingClientRect().width;
      setRecentOpen(true, false); await nextFrame(); await fitRuntime(tab.id, runtime); await nextFrame();
      const viewport = new DOMRect(0, 0, window.innerWidth, window.innerHeight); const main = mainElement.getBoundingClientRect();
      const panel = recentElement.getBoundingClientRect(); const heading = recentElement.querySelector('h2')!.getBoundingClientRect(); const list = recentListElement.getBoundingClientRect();
      const rows = [...recentListElement.querySelectorAll<HTMLElement>('.recent-row')]; const lastRow = rows.at(-1)!; const lastButton = lastRow.querySelector<HTMLButtonElement>('button')!;
      lastButton.focus(); lastButton.scrollIntoView({ block: 'nearest' }); await nextFrame();
      const focusedRow = lastRow.getBoundingClientRect(); const focusedButton = lastButton.getBoundingClientRect(); const proposed = runtime.fit.proposeDimensions();
      const firstFit = { cols: runtime.terminal.cols, rows: runtime.terminal.rows }; await fitRuntime(tab.id, runtime);
      const secondFit = { cols: runtime.terminal.cols, rows: runtime.terminal.rows }; const terminal = runtime.container.getBoundingClientRect();
      const narrow = fixture.width <= 760;
      const toggleContract = !recentToggleButton.hidden && recentToggleButton.getAttribute('aria-controls') === 'recent' && recentToggleButton.getAttribute('aria-expanded') === 'true' && recentToggleButton.getAttribute('aria-label')?.includes(String(rowCount)) === true;
      const focusedControlVisible = document.activeElement === lastButton && inside(focusedRow, list) && inside(focusedButton, list);
      const listGeometry = { clientHeight: recentListElement.clientHeight, scrollHeight: recentListElement.scrollHeight };
      const internalScroll = getComputedStyle(recentListElement).overflowY === 'auto' && (rowCount === 1 || listGeometry.scrollHeight > listGeometry.clientHeight);
      setRecentOpen(false, false); await nextFrame(); await fitRuntime(tab.id, runtime); const restoredFit = { cols: runtime.terminal.cols, rows: runtime.terminal.rows };
      const assertions = {
        toggleContract,
        closedZeroWidth: closedPanelWidth === 0,
        desktopWidthBound: narrow || (panel.width >= 240 && panel.width <= 320),
        headingVisible: inside(heading, panel),
        focusedControlVisible,
        internalScroll,
        terminalNonOverlap: narrow ? inside(terminal, main) : terminal.right <= panel.left + 1,
        usefulTerminal: terminal.height >= 280 && terminal.width >= 280,
        narrowOverlay: !narrow || (getComputedStyle(recentElement).position === 'fixed' && terminal.width === closedTerminal.width && secondFit.cols === closedFit.cols),
        fitStable: firstFit.cols === secondFit.cols && firstFit.rows === secondFit.rows && proposed?.cols === secondFit.cols && proposed.rows === secondFit.rows,
        closeRestoresFit: restoredFit.cols === closedFit.cols && restoredFit.rows === closedFit.rows,
        panelInsideViewport: inside(panel, viewport),
      };
      fixtures.push({ ...fixture, viewport: { width: window.innerWidth, height: window.innerHeight }, rowCount, closed: { terminal: { width: closedTerminal.width, height: closedTerminal.height, ...closedFit }, panelWidth: closedPanelWidth }, open: { panel: { width: panel.width, height: panel.height }, list: listGeometry, terminal: { width: terminal.width, height: terminal.height, ...secondFit } }, assertions });
    }
  }
  await setProbeViewport(1100, 760); setRecentOpen(true, false); render(); await nextFrame();
  const focusedBeforeStatus = [...recentListElement.querySelectorAll<HTMLButtonElement>('.recent-row button')].at(-1)!;
  const focusedChatId = focusedBeforeStatus.closest<HTMLElement>('.recent-row')!.dataset.chatId!;
  focusedBeforeStatus.focus(); await requestProductionStatus(tab.id);
  const focusedAfterStatus = document.activeElement as HTMLButtonElement;
  const statusFocusPreserved = focusedAfterStatus !== focusedBeforeStatus && focusedAfterStatus.closest<HTMLElement>('.recent-row')?.dataset.chatId === focusedChatId;
  recentCloseButton.click(); await nextFrame(); const closeFocusReturned = !recentOpen && document.activeElement === recentToggleButton;
  recentToggleButton.click(); await nextFrame();
  const escapeButton = [...recentListElement.querySelectorAll<HTMLButtonElement>('.recent-row button')].find((button) => button.closest<HTMLElement>('.recent-row')?.dataset.chatId === focusedChatId)!;
  escapeButton.focus(); document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })); await nextFrame();
  const escapeFocusReturned = !recentOpen && document.activeElement === recentToggleButton;
  await stop(tab.id); view = await window.voidTerminal.workspace.close(tab.id); render();
  recentToggleButton.click(); await nextFrame();
  const resumeButton = [...recentListElement.querySelectorAll<HTMLButtonElement>('.recent-row button')].find((button) => button.closest<HTMLElement>('.recent-row')?.dataset.chatId === tab.id)!;
  resumeButton.click();
  const resumeDeadline = Date.now() + 10_000;
  while (Date.now() < resumeDeadline && (view.workspace?.selectedId !== tab.id || (!runtimes.get(tab.id)?.container.contains(document.activeElement) && !endedElement.contains(document.activeElement)))) await new Promise((resolve) => setTimeout(resolve, 50));
  const resumedRuntime = runtimes.get(tab.id);
  const resumeFocusTransferred = view.workspace?.selectedId === tab.id && (Boolean(resumedRuntime?.container.contains(document.activeElement)) || endedElement.contains(document.activeElement));
  const assertions = {
    emptyRecentClosed: empty.panelWidth === 0 && empty.toggleHidden && !empty.bodyColumns.includes(' 0px'),
    allGeometry: fixtures.every((fixture) => Object.values(fixture.assertions as Record<string, boolean>).every(Boolean)),
    statusFocusPreserved,
    closeFocusReturned,
    escapeFocusReturned,
    resumeFocusTransferred,
  };
  return {
    assertions, empty, focusBehavior: { focusedChatId, statusFocusPreserved, closeFocusReturned, escapeFocusReturned, resumeFocusTransferred },
    fixtures: fixtures.map((fixture) => {
      const closed = fixture.closed as { terminal: { width: number; cols: number; rows: number }; panelWidth: number };
      const open = fixture.open as { panel: { width: number }; list: { clientHeight: number; scrollHeight: number }; terminal: { width: number; cols: number; rows: number } };
      return {
        id: `${String(fixture.name)}-${String(fixture.rowCount)}`,
        closed: { panel: closed.panelWidth, width: closed.terminal.width, cols: closed.terminal.cols, rows: closed.terminal.rows },
        open: { panel: open.panel.width, width: open.terminal.width, cols: open.terminal.cols, rows: open.terminal.rows, list: `${open.list.clientHeight}/${open.list.scrollHeight}` },
        failed: Object.entries(fixture.assertions as Record<string, boolean>).filter(([, passed]) => !passed).map(([name]) => name),
      };
    }),
  };
}

async function productionProbe(): Promise<void> {
  const tab = selectedTab(); const workspace = view.workspace; if (!tab || !workspace) throw new Error('probe workspace missing');
  const fixtureId = 'terminal-fidelity'; const container = document.createElement('div'); container.className = 'terminal'; terminalsElement.append(container);
  const product = createProductTerminal(); product.terminal.open(container); activateProductRenderer(product); product.fit.fit();
  const fixtureBytes = emptyByteFacts();
  await window.voidTerminal.start({ sessionId: fixtureId, fixture: 'terminalFidelity' });
  const fixtureOff = window.voidTerminal.onOutput(fixtureId, ({ data }) => { addByteFacts(fixtureBytes, data); product.terminal.write(data); });
  await window.voidTerminal.resize({ sessionId: fixtureId, cols: product.terminal.cols, rows: product.terminal.rows });
  await window.voidTerminal.input({ sessionId: fixtureId, data: 'terminal-fidelity\r' });
  await new Promise((resolve) => setTimeout(resolve, 500));
  const fixtureRendered = renderedFacts({ ...product, container, offOutput: fixtureOff, offExit: () => undefined, offStatus: () => undefined, exited: false });
  const integration = integrationFacts({ ...product, container, offOutput: fixtureOff, offExit: () => undefined, offStatus: () => undefined, exited: false });
  fixtureOff(); await window.voidTerminal.stop({ sessionId: fixtureId }); product.disposeRenderer(); product.terminal.dispose(); container.remove();

  await launch(tab, 'create'); const runtime = runtimes.get(tab.id)!; const realBytes = emptyByteFacts();
  const observe = window.voidTerminal.onOutput(tab.id, ({ data }) => addByteFacts(realBytes, data));
  const outputDeadline = Date.now() + 10_000;
  while (Date.now() < outputDeadline && renderedFacts(runtime).distinctForegrounds < 2) await new Promise((resolve) => setTimeout(resolve, 100));
  await new Promise<void>((resolve) => runtime.terminal.write('', resolve));
  const realRendered = renderedFacts(runtime); const realVisible = await visibleColorFacts(runtime); observe();
  const recentGeometry = await recentGeometryProbe(tab, runtime);
  const assertions = {
    officialXterm: integration.xterm.instance, bundledFontLoaded: integration.font.loaded,
    equalWidth: integration.font.equalWidth, fixtureAnsi: fixtureBytes.escBytes > 0 && fixtureRendered.styled > 0,
    realAnsi: realBytes.escBytes > 0,
    realVisibleColor: realVisible.distinctVisibleRgb > 1 && realVisible.chromaticHueBins > 1 && realVisible.contrastingPixels > 0 && realVisible.maxContrast >= 3,
    recentGeometry: Object.values(recentGeometry.assertions).every(Boolean),
  };
  const result = { ok: Object.values(assertions).every(Boolean), assertions, integration, fixture: { bytes: fixtureBytes, rendered: fixtureRendered }, realPi: { bytes: realBytes, rendered: realRendered, visible: realVisible }, recentGeometry };
  document.title = `VOID_PRODUCTION_TERMINAL:${JSON.stringify(result)}`;
}

void window.voidTerminal.workspace.load().then(async (loaded) => {
  view = loaded; render();
  if (new URLSearchParams(location.search).get('productionTerminalProbe') === '1') await productionProbe();
  else { const tab = selectedTab(); if (tab && !view.recoveryPath) await launch(tab, 'resume'); render(); }
}).catch(() => { document.title = `VOID_PRODUCTION_TERMINAL:${JSON.stringify({ ok: false, errorCode: 'WORKSPACE_LOAD_FAILED' })}`; });
