import type { Terminal as XTerm } from '@xterm/xterm';
import type { TabRecord, TerminalApi, WorkspaceView } from '../shared/contract';

declare global {
  interface Window { voidTerminal: TerminalApi }
  type RendererTabRecord = TabRecord;
  type RendererWorkspaceView = WorkspaceView;
  const Terminal: typeof XTerm;
}
export {};
