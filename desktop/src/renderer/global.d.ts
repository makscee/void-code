import type { Terminal as XTerm } from '@xterm/xterm';
import type { TerminalApi } from '../shared/contract';

declare global {
  interface Window { voidTerminal: TerminalApi }
  const Terminal: typeof XTerm;
}
export {};
