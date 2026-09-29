import { createProductTerminal } from '../../src/renderer/terminal-stack';

const terminals = document.querySelector<HTMLElement>('#terminals')!;
const recent = document.querySelector<HTMLElement>('#recent')!;
const recentList = document.querySelector<HTMLElement>('#recent-list')!;
const tabs = document.querySelector<HTMLElement>('#tabs')!;
for (const selector of ['#empty', '#preflight', '#recovery', '#ended']) document.querySelector<HTMLElement>(selector)!.hidden = true;
tabs.hidden = false;
recent.hidden = false;
document.body.classList.add('recent-open');

const host = document.createElement('div');
host.className = 'terminal';
terminals.append(host);
const product = createProductTerminal();
product.terminal.open(host);

function frame(): Promise<void> {
  return new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
}

function setRows(count: number): void {
  recentList.replaceChildren(...Array.from({ length: count }, (_, index) => {
    const row = document.createElement('div');
    row.className = 'recent-row';
    const label = document.createElement('span');
    label.textContent = `Synthetic chat ${String(index + 1).padStart(2, '0')}`;
    const button = document.createElement('button');
    button.type = 'button';
    button.textContent = 'Resume';
    button.setAttribute('aria-label', `Resume synthetic chat ${index + 1}`);
    row.append(label, button);
    return row;
  }));
}

async function settle(): Promise<void> {
  await document.fonts.ready;
  product.fit.fit();
  await frame();
}

function rect(element: Element): DOMRect {
  return element.getBoundingClientRect();
}

function inside(inner: DOMRect, outer: DOMRect): boolean {
  return inner.top >= outer.top - 1 && inner.bottom <= outer.bottom + 1 && inner.left >= outer.left - 1 && inner.right <= outer.right + 1;
}

function snapshot() {
  const panelRect = rect(recent);
  const headingRect = rect(document.querySelector('.recent-heading')!);
  const listRect = rect(recentList);
  const lastButton = recentList.querySelectorAll<HTMLButtonElement>('button').item(recentList.querySelectorAll('button').length - 1);
  const listStyle = getComputedStyle(recentList);
  return {
    viewport: { width: innerWidth, height: innerHeight },
    body: { clientHeight: document.body.clientHeight, scrollHeight: document.body.scrollHeight },
    panel: { top: panelRect.top, bottom: panelRect.bottom, height: panelRect.height, position: getComputedStyle(recent).position },
    headingVisible: inside(headingRect, panelRect),
    list: { top: listRect.top, bottom: listRect.bottom, clientHeight: recentList.clientHeight, scrollHeight: recentList.scrollHeight, scrollTop: recentList.scrollTop, overflowY: listStyle.overflowY, colorScheme: listStyle.colorScheme, scrollbarColor: listStyle.scrollbarColor },
    lastButtonVisible: lastButton ? inside(rect(lastButton), listRect) : false,
  };
}

declare global {
  interface Window {
    scrollSurfaceFixture: {
      terminalReady(): Promise<Record<string, unknown>>;
      setRecentRows(count: number): Promise<ReturnType<typeof snapshot>>;
      focusLastRecent(): Promise<ReturnType<typeof snapshot>>;
      terminalState(): Record<string, unknown>;
      recentState(): ReturnType<typeof snapshot>;
    };
  }
}

window.scrollSurfaceFixture = {
  async terminalReady() {
    await settle();
    await new Promise<void>((resolve) => product.terminal.write(Array.from({ length: 220 }, (_, index) => `synthetic terminal line ${String(index + 1).padStart(3, '0')}\r\n`).join(''), resolve));
    await settle();
    const viewport = host.querySelector<HTMLElement>('.xterm-viewport')!;
    const scrollable = host.querySelector<HTMLElement>('.xterm-scrollable-element')!;
    const slider = scrollable.querySelector<HTMLElement>('.scrollbar.vertical .slider')!;
    return {
      rows: product.terminal.rows,
      scrollback: product.terminal.options.scrollback,
      viewportOverflowY: getComputedStyle(viewport).overflowY,
      nativeViewportPresent: Boolean(viewport),
      customScrollablePresent: Boolean(scrollable),
      sliderPresent: Boolean(slider),
      sliderBackground: getComputedStyle(slider).backgroundColor,
      viewportY: product.terminal.buffer.active.viewportY,
      baseY: product.terminal.buffer.active.baseY,
      terminalCenter: { x: Math.round(rect(host).left + rect(host).width / 2), y: Math.round(rect(host).top + rect(host).height / 2) },
      sliderCenter: { x: Math.round(rect(slider).left + Math.max(1, rect(slider).width / 2)), y: Math.round(rect(slider).top + Math.max(1, rect(slider).height / 2)) },
    };
  },
  async setRecentRows(count) { setRows(count); recentList.scrollTop = 0; await settle(); return snapshot(); },
  async focusLastRecent() { const button = [...recentList.querySelectorAll<HTMLButtonElement>('button')].at(-1)!; button.focus(); button.scrollIntoView({ block: 'nearest' }); await frame(); return snapshot(); },
  terminalState() { return { viewportY: product.terminal.buffer.active.viewportY, baseY: product.terminal.buffer.active.baseY }; },
  recentState: snapshot,
};

setRows(8);
void settle().then(() => { document.title = 'scroll-surfaces-ready'; });
