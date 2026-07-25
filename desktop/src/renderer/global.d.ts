import type { TerminalApi } from '../shared/contract';

declare global { interface Window { voidTerminal: TerminalApi } }
export {};
