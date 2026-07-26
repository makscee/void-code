import { Terminal } from '@xterm/xterm';
import { activateProductRenderer, createProductTerminal, TERMINAL_OPTIONS, TERMINAL_THEME, type ProductTerminal } from './terminal-stack';
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

type Runtime = ProductTerminal & { container: HTMLDivElement; offOutput: () => void; offExit: () => void; offStatus: () => void; exited: boolean };
const runtimes = new Map<string, Runtime>();
const chatStatuses = new Map<string, RendererChatStatus>();
let view: RendererWorkspaceView = { workspace: null, recoveryPath: null };

function announce(message: string): void { noticeElement.textContent = message; noticeElement.hidden = false; }
async function fitRuntime(id: string, runtime: Runtime): Promise<void> {
  runtime.fit.fit();
  await window.voidTerminal.resize({ sessionId: id, cols: runtime.terminal.cols, rows: runtime.terminal.rows });
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
  const runtime = Object.assign(created, { container, offOutput, offExit, offStatus, exited: false }) as Runtime; runtimes.set(tab.id, runtime);
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
    await fitRuntime(tab.id, runtime);
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
async function selectChat(id: string): Promise<void> { view = await window.voidTerminal.workspace.select(id); const status = chatStatuses.get(id); if (status) chatStatuses.set(id, { ...status, unread: false }); render(); const tab = selectedTab(); if (tab && !runtimes.has(id)) await launch(tab, 'resume'); else if (tab) { const runtime = runtimes.get(id); if (runtime) await fitRuntime(id, runtime); chatStatuses.set(id, (await window.voidTerminal.lifecycleStatus({ sessionId: id })).status); } render(); }
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
new ResizeObserver(() => { const tab = selectedTab(); const runtime = tab ? runtimes.get(tab.id) : undefined; if (!runtime || runtime.container.hidden || runtime.exited) return; void fitRuntime(tab!.id, runtime); }).observe(terminalsElement);

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
  await new Promise((resolve) => setTimeout(resolve, 2500)); await new Promise<void>((resolve) => runtime.terminal.write('', resolve));
  const realRendered = renderedFacts(runtime); const realVisible = await visibleColorFacts(runtime); observe();
  const assertions = {
    officialXterm: integration.xterm.instance, bundledFontLoaded: integration.font.loaded,
    equalWidth: integration.font.equalWidth, fixtureAnsi: fixtureBytes.escBytes > 0 && fixtureRendered.styled > 0,
    realAnsi: realBytes.escBytes > 0,
    realVisibleColor: realVisible.distinctVisibleRgb > 1 && realVisible.chromaticHueBins > 1 && realVisible.contrastingPixels > 0 && realVisible.maxContrast >= 3,
  };
  const result = { ok: Object.values(assertions).every(Boolean), assertions, integration, fixture: { bytes: fixtureBytes, rendered: fixtureRendered }, realPi: { bytes: realBytes, rendered: realRendered, visible: realVisible } };
  document.title = `VOID_PRODUCTION_TERMINAL:${JSON.stringify(result)}`;
}

void window.voidTerminal.workspace.load().then(async (loaded) => {
  view = loaded; render();
  if (new URLSearchParams(location.search).get('productionTerminalProbe') === '1') await productionProbe();
  else { const tab = selectedTab(); if (tab && !view.recoveryPath) await launch(tab, 'resume'); render(); }
}).catch((error: unknown) => { document.title = `VOID_PRODUCTION_TERMINAL:${JSON.stringify({ ok: false, error: error instanceof Error ? error.message : String(error) })}`; });
