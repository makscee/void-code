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
  const optionsBeforeOpen = { fontFamily: instance.terminal.options.fontFamily, fontSize: instance.terminal.options.fontSize,
    fontWeight: instance.terminal.options.fontWeight, fontWeightBold: instance.terminal.options.fontWeightBold,
    letterSpacing: instance.terminal.options.letterSpacing, lineHeight: instance.terminal.options.lineHeight };
  instance.terminal.open(host); product.activateProductRenderer(instance); instance.fit.fit();
  await new Promise<void>(resolve => instance.terminal.write('\x1b[2J\x1b[HЖж Жж Жж\r\n\x1b[1mЖж Жж Жж\x1b[0m\r\nLatin control: MWim', resolve));
  await new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
  const core = (instance.terminal as unknown as { _core: { _renderService: { dimensions: unknown } } })._core;
  const report = {
    platform: navigator.platform, userAgent: navigator.userAgent, dpr: devicePixelRatio,
    nativeWindowsDpr15: /Windows/.test(navigator.userAgent) && devicePixelRatio === 1.5,
    readiness, facesBeforeOpen, optionsBeforeOpen, renderer: instance.renderer,
    canvasCount: host.querySelectorAll('canvas').length, dimensions: core._renderService.dimensions,
    rows: [0, 1].map(y => Array.from({ length: 8 }, (_, x) => {
      const cell = instance.terminal.buffer.active.getLine(y)?.getCell(x);
      return { x, text: cell?.getChars(), width: cell?.getWidth(), bold: cell?.isBold() };
    })),
    host: host.getBoundingClientRect().toJSON(),
  };
  // Coordinator captures the real window and measures raster edges; cells alone
  // are deliberately not presented as a passing native glyph-image measurement.
  Object.assign(window, { windowsTerminalFontFixture: { report, dispose: () => { instance.disposeRenderer(); instance.terminal.dispose(); host.remove(); } } });
  document.title = 'windows-terminal-font-fixture-ready';
}
void run().catch(error => {
  Object.assign(window, { windowsTerminalFontFixture: { error: String(error) } });
  document.title = 'windows-terminal-font-fixture-failed';
});
