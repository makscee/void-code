import { WebglAddon } from '@xterm/addon-webgl';
import * as product from '../../src/renderer/terminal-stack';

// Isolated renderer only: no bridge, PTY, Pi, preload font calls or option overrides.
// Bundle with the same CSS/font loaders as production (see adjacent plan).
async function run() {
  const prepare = Reflect.get(product, 'prepareTerminalFonts');
  if (typeof prepare !== 'function') throw new Error('Product prepareTerminalFonts API missing');
  const readiness: unknown = await prepare();
  const facesBeforeOpen = [...document.fonts].filter(face => /JetBrains Mono/i.test(face.family)).map(face => ({
    family: face.family, weight: face.weight, style: face.style, unicodeRange: face.unicodeRange, status: face.status,
  }));
  const host = document.createElement('div');
  host.style.cssText = 'position:fixed;inset:20px;width:900px;height:400px';
  document.body.append(host);
  const instance = product.createProductTerminal();
  let webgl: WebglAddon | undefined;
  let contextLost = false;
  const originalLoadAddon = instance.terminal.loadAddon;
  instance.terminal.loadAddon = function(addon) {
    if (addon instanceof WebglAddon) {
      webgl = addon;
      addon.onContextLoss(() => { contextLost = true; });
    }
    return originalLoadAddon.call(this, addon);
  };
  const optionsBeforeOpen = { fontFamily: instance.terminal.options.fontFamily, fontSize: instance.terminal.options.fontSize,
    fontWeight: instance.terminal.options.fontWeight, fontWeightBold: instance.terminal.options.fontWeightBold,
    letterSpacing: instance.terminal.options.letterSpacing, lineHeight: instance.terminal.options.lineHeight };
  instance.terminal.open(host); product.activateProductRenderer(instance); instance.fit.fit();
  const probeGlyphs = [false, true].flatMap(bold => Array.from('WЖжШщ').map((glyph, i) => ({
    glyph, row: bold ? 4 : 2, column: 1 + i * 5, bold,
    intendedWeight: bold ? optionsBeforeOpen.fontWeightBold : optionsBeforeOpen.fontWeight,
    intendedForeground: '#ffffff', intendedBackground: '#0f1117',
  })));
  const exactWrite = '\x1b[?25l\x1b[2J' + [false, true].map(bold =>
    `\x1b[${bold ? 4 : 2};1H\x1b[0;38;2;255;255;255;48;2;15;17;23m\x1b[2K${bold ? '\x1b[1m' : ''}${Array.from('WЖжШщ').join('    ')}\x1b[0m`).join('');
  await new Promise<void>(resolve => instance.terminal.write(exactWrite, resolve));
  await new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
  const core = (instance.terminal as unknown as { _core: { _renderService: { dimensions: unknown; _renderer?: { value: object } } } })._core;
  // Read-only snapshots, also callable after capture: no writes, font checks or measurements.
  const actual = () => ({
    renderer: instance.renderer, webglIdentity: webgl instanceof WebglAddon,
    rendererClass: core._renderService._renderer?.value?.constructor.name,
    actualWebglRendererIsActive: !!webgl && (webgl as unknown as { _renderer: object })._renderer === core._renderService._renderer?.value,
    contextLost,
  });
  const report = {
    platform: navigator.platform, userAgent: navigator.userAgent, dpr: devicePixelRatio,
    nativeWindowsDpr15: /Windows/.test(navigator.userAgent) && devicePixelRatio === 1.5,
    readiness, facesBeforeOpen, optionsBeforeOpen, renderer: instance.renderer,
    canvasCount: host.querySelectorAll('canvas').length, dimensions: core._renderService.dimensions,
    actual: actual(), exactWrite, probeGlyphs,
    coordinateConvention: '1-based screen rows/columns; CSS viewport origin = screen rect + (column-1,row-1) * dimensions.css.cell; device dimensions are separate',
    screen: host.querySelector('.xterm-screen')!.getBoundingClientRect().toJSON(),
    cols: instance.terminal.cols, rowsCount: instance.terminal.rows,
    bufferBaseY: instance.terminal.buffer.active.baseY, viewportY: instance.terminal.buffer.active.viewportY,
    rows: [1, 3].map(y => Array.from({ length: 21 }, (_, x) => {
      const cell = instance.terminal.buffer.active.getLine(y)?.getCell(x);
      return { row: y + 1, column: x + 1, text: cell?.getChars(), width: cell?.getWidth(), bold: cell?.isBold() };
    })),
    host: host.getBoundingClientRect().toJSON(),
  };
  // Coordinator captures the real window and measures raster edges; cells alone
  // are deliberately not presented as a passing native glyph-image measurement.
  Object.assign(window, { windowsTerminalFontFixture: { report, afterCapture: () => ({ actual: actual() }), dispose: () => { instance.disposeRenderer(); instance.terminal.dispose(); host.remove(); } } });
  document.title = 'windows-terminal-font-fixture-ready';
}
void run().catch(error => {
  Object.assign(window, { windowsTerminalFontFixture: { error: String(error) } });
  document.title = 'windows-terminal-font-fixture-failed';
});
