// Opt-in real Chromium check. This is an offline synthetic fixture, not an installed-app or live-chat check.
import { app, BrowserWindow, session } from 'electron';
import { build } from 'esbuild';
import { mkdtempSync } from 'node:fs';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, URL } from 'node:url';

const desktopRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
// This must be synchronous: Electron on macOS can defer ready until the main ESM
// module has finished evaluating, so no app.whenReady() dependency may be top-level awaited.
const temporary = mkdtempSync(path.join(os.tmpdir(), 'vc-scroll-surfaces-'));
const artifacts = path.resolve(process.argv[2] ?? path.join(temporary, 'artifacts')); 
const overallTimeoutMs = 45_000;
const userData = path.join(temporary, 'user-data');
app.setPath('userData', userData);
app.commandLine.appendSwitch('disable-gpu');

const checks = [];
const record = (name, pass, details) => checks.push({ name, pass: Boolean(pass), details });
const sleep = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
let window;

async function renderer(expression) {
  return window.webContents.executeJavaScript(expression, true);
}

async function resize(width, height) {
  window.setContentSize(width, height);
  await renderer('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
}

async function mouse(type, point, extra = {}) {
  await window.webContents.debugger.sendCommand('Input.dispatchMouseEvent', { type, ...point, ...extra });
}

async function wheelAt(point, deltaY) {
  await mouse('mouseMoved', point);
  await mouse('mouseWheel', point, { deltaY, deltaX: 0 });
}

async function screenshot(name) {
  const image = await window.webContents.capturePage();
  await writeFile(path.join(artifacts, `${name}.png`), image.toPNG());
}

function assertGeometry(name, state, { rowsFit, narrow = false } = {}) {
  record(`${name}: body does not scroll`, state.body.scrollHeight <= state.body.clientHeight, state.body);
  record(`${name}: heading remains visible`, state.headingVisible, state.panel);
  record(`${name}: panel reaches viewport bottom`, Math.abs(state.panel.bottom - state.viewport.height) <= 1, state.panel);
  record(`${name}: list fills panel grid row`, state.panel.bottom - state.list.bottom <= 13, { panelBottom: state.panel.bottom, listBottom: state.list.bottom });
  record(`${name}: list owns overflow`, state.list.overflowY === 'auto', state.list);
  if (rowsFit !== undefined) record(`${name}: expected overflow state`, rowsFit ? state.list.scrollHeight <= state.list.clientHeight : state.list.scrollHeight > state.list.clientHeight, state.list);
  if (narrow) record(`${name}: narrow layout is fixed overlay`, state.panel.position === 'fixed', state.panel);
}

async function runChecks() {
  await mkdir(artifacts, { recursive: true });
  await build({
    entryPoints: [path.join(desktopRoot, 'tests/fixtures/scroll-surfaces-renderer.ts')],
    bundle: true,
    format: 'esm',
    platform: 'browser',
    target: 'chrome130',
    loader: { '.woff2': 'file', '.woff': 'file' },
    assetNames: 'assets/[name]-[hash]',
    outfile: path.join(temporary, 'fixture.js'),
    logLevel: 'silent',
  });
  const productHtml = await readFile(path.join(desktopRoot, 'src/renderer/index.html'), 'utf8');
  const fixtureHtml = productHtml.replace('href="index.css"', 'href="fixture.css"').replace('src="index.js"', 'src="fixture.js"');
  await writeFile(path.join(temporary, 'index.html'), fixtureHtml);

  await app.whenReady();
  const isolated = session.fromPartition(`scroll-surfaces-${Date.now()}`, { cache: false });
  isolated.webRequest.onBeforeRequest((details, callback) => callback({ cancel: !['file:', 'data:', 'devtools:'].includes(new URL(details.url).protocol) }));
  window = new BrowserWindow({
    show: false,
    frame: false,
    width: 1100,
    height: 1000,
    webPreferences: { offscreen: true, backgroundThrottling: false, sandbox: true, contextIsolation: true, nodeIntegration: false, session: isolated },
  });
  await window.loadFile(path.join(temporary, 'index.html'));
  for (let attempt = 0; attempt < 100 && (await renderer('document.title')) !== 'scroll-surfaces-ready'; attempt += 1) await sleep(50);
  if ((await renderer('document.title')) !== 'scroll-surfaces-ready') throw new Error('fixture did not become ready');

  window.webContents.debugger.attach('1.3');
  await window.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', { media: '', features: [{ name: 'prefers-color-scheme', value: 'light' }] });

  const terminal = await renderer('window.scrollSurfaceFixture.terminalReady()');
  record('real product terminal keeps configured scrollback', terminal.scrollback === 10_000 && terminal.baseY > 0, terminal);
  record('redundant native xterm viewport rail is suppressed', terminal.nativeViewportPresent && terminal.viewportOverflowY === 'hidden', terminal);
  record('real xterm custom scroll surface and slider remain', terminal.customScrollablePresent && terminal.sliderPresent, terminal);
  record('actual xterm slider is transparent while idle', terminal.sliderBackground === 'rgba(0, 0, 0, 0)', terminal.sliderBackground);

  window.webContents.sendInputEvent({ type: 'mouseMove', ...terminal.sliderCenter });
  await sleep(50);
  const hover = await renderer("getComputedStyle(document.querySelector('.xterm-scrollable-element .scrollbar.vertical .slider')).backgroundColor");
  record('actual xterm slider becomes subdued on hover', hover !== terminal.sliderBackground && hover !== 'rgb(255, 255, 255)', hover);
  window.webContents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, ...terminal.sliderCenter });
  await sleep(50);
  const active = await renderer("getComputedStyle(document.querySelector('.xterm-scrollable-element .scrollbar.vertical .slider')).backgroundColor");
  record('actual xterm slider has a non-white active state', active !== terminal.sliderBackground && active !== 'rgb(255, 255, 255)', active);
  window.webContents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, ...terminal.sliderCenter });

  const beforeWheel = await renderer('window.scrollSurfaceFixture.terminalState()');
  await wheelAt(terminal.terminalCenter, -600);
  await sleep(150);
  const afterWheel = await renderer('window.scrollSurfaceFixture.terminalState()');
  record('terminal scrollback moves from an actual wheel event', afterWheel.viewportY < beforeWheel.viewportY, { beforeWheel, afterWheel });

  for (const fixture of [
    { name: 'tall-8', width: 1100, height: 1000, rows: 8, rowsFit: true },
    { name: 'normal-8', width: 1100, height: 760, rows: 8, rowsFit: true },
    { name: 'short-8', width: 1100, height: 560, rows: 8, rowsFit: true },
    { name: 'narrow-8', width: 640, height: 760, rows: 8, rowsFit: true, narrow: true },
    { name: 'tall-41', width: 1100, height: 1000, rows: 41, rowsFit: false },
  ]) {
    await resize(fixture.width, fixture.height);
    const state = await renderer(`window.scrollSurfaceFixture.setRecentRows(${fixture.rows})`);
    assertGeometry(fixture.name, state, fixture);
    await screenshot(`base-${fixture.name}`);
  }

  const light = await renderer('window.scrollSurfaceFixture.recentState()');
  const colorNumbers = light.list.scrollbarColor.match(/[\d.]+/g)?.map(Number) ?? [];
  record('Recent scrollbar stays explicitly dark under LIGHT host preference', light.list.colorScheme.includes('dark') && light.list.scrollbarColor !== 'auto' && colorNumbers.length >= 3 && Math.max(...colorNumbers.slice(0, 3)) < 200, light.list);

  const listCenter = await renderer("(() => { const r=document.querySelector('#recent-list').getBoundingClientRect(); return {x:Math.round(r.left+r.width/2),y:Math.round(r.top+r.height/2)} })()");
  await wheelAt(listCenter, 900);
  await sleep(150);
  const wheeledRecent = await renderer('window.scrollSurfaceFixture.recentState()');
  record('long Recent list responds to an actual wheel event', wheeledRecent.list.scrollTop > 0, wheeledRecent.list);
  const focused = await renderer('window.scrollSurfaceFixture.focusLastRecent()');
  record('last Resume control remains keyboard-focusable and visible', focused.lastButtonVisible, focused.list);

  const result = { kind: 'offline synthetic real-renderer check', electron: process.versions.electron, chromium: process.versions.chrome, artifacts, checks, ok: checks.every((check) => check.pass) };
  await writeFile(path.join(artifacts, 'result.json'), `${JSON.stringify(result, null, 2)}\n`);
  console.log(JSON.stringify(result, null, 2));
  return result.ok ? 0 : 1;
}

async function main() {
  let exitCode = 2;
  let timeout;
  try {
    const bounded = new Promise((_, reject) => {
      timeout = setTimeout(() => reject(new Error(`real-renderer check exceeded ${overallTimeoutMs}ms overall timeout`)), overallTimeoutMs);
    });
    exitCode = await Promise.race([runChecks(), bounded]);
  } catch (error) {
    const failure = { ok: false, kind: 'real-renderer harness error', error: error instanceof Error ? error.stack ?? error.message : String(error), artifacts };
    await mkdir(artifacts, { recursive: true }).catch(() => undefined);
    await writeFile(path.join(artifacts, 'error.json'), `${JSON.stringify(failure, null, 2)}\n`).catch(() => undefined);
    console.error(JSON.stringify(failure, null, 2));
  } finally {
    globalThis.clearTimeout(timeout);
    // Keep the last window alive while awaiting file cleanup: destroying it first lets
    // Electron's default window-all-closed path terminate macOS runs with status 0.
    if (process.argv[2]) await rm(temporary, { recursive: true, force: true });
    if (window && !window.isDestroyed()) window.destroy();
  }
  process.exit(exitCode);
}

// Deliberately not awaited at module scope; see synchronous setup above.
void main();
