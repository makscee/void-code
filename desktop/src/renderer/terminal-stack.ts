import { Terminal, type ILinkHandler, type ITheme } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { WebglAddon } from '@xterm/addon-webgl';
import '@xterm/xterm/css/xterm.css';
import '@fontsource/jetbrains-mono/400.css';
import '@fontsource/jetbrains-mono/700.css';
import './index.css';

const query = new URLSearchParams(location.search);
const probe = query.get('productionTerminalProbe') === '1' ? query.get('productionTerminalPerturb') : null;
const palette: ITheme = {
  background: '#0f1117', foreground: '#d8dee9', cursor: '#f8f8f2', cursorAccent: '#0f1117', selectionBackground: '#3b4252',
  black: '#2e3440', red: '#bf616a', green: '#a3be8c', yellow: '#ebcb8b', blue: '#81a1c1', magenta: '#b48ead', cyan: '#88c0d0', white: '#e5e9f0',
  brightBlack: '#4c566a', brightRed: '#d57780', brightGreen: '#b1d196', brightYellow: '#f0d399', brightBlue: '#8fbcdb', brightMagenta: '#c895bf', brightCyan: '#93ccdc', brightWhite: '#eceff4',
};
if (probe === 'palette-collapse') {
  for (const key of ['foreground', 'black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white', 'brightBlack', 'brightRed', 'brightGreen', 'brightYellow', 'brightBlue', 'brightMagenta', 'brightCyan', 'brightWhite'] as const) palette[key] = '#ffffff';
  document.documentElement.classList.add('probe-palette-collapse');
}
export const TERMINAL_THEME: Readonly<ITheme> = Object.freeze(palette);

export const TERMINAL_OPTIONS = Object.freeze({
  cursorBlink: true, cursorStyle: 'block' as const, drawBoldTextInBrightColors: true,
  fontFamily: probe === 'missing-font' ? 'Arial, sans-serif' : '"JetBrains Mono", monospace', fontSize: 14, fontWeight: '400' as const,
  fontWeightBold: '700' as const, letterSpacing: 0, lineHeight: 1.15,
  minimumContrastRatio: 1, scrollback: 10_000, theme: TERMINAL_THEME,
});

export type ProductTerminal = {
  terminal: Terminal;
  fit: FitAddon;
  renderer: 'webgl' | 'dom';
  disposeRenderer(): void;
};

export function createProductTerminal(linkHandler?: ILinkHandler): ProductTerminal {
  const terminal = new Terminal({ ...TERMINAL_OPTIONS, ...(linkHandler ? { linkHandler } : {}) });
  const fit = new FitAddon();
  terminal.loadAddon(fit);
  return { terminal, fit, renderer: 'dom', disposeRenderer: () => undefined };
}

export function activateProductRenderer(product: ProductTerminal): void {
  try {
    const webgl = new WebglAddon();
    webgl.onContextLoss(() => { webgl.dispose(); product.renderer = 'dom'; });
    product.terminal.loadAddon(webgl);
    product.renderer = 'webgl';
    product.disposeRenderer = () => webgl.dispose();
  } catch {
    product.renderer = 'dom';
  }
}
