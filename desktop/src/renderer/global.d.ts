import type { Terminal as XTerm } from '@xterm/xterm';
import type { ChatSemanticStatus, TabRecord, TerminalApi, WorkspaceView } from '../shared/contract';

declare global {
  interface Window { voidTerminal: TerminalApi }
  type RendererTabRecord = TabRecord;
  type RendererWorkspaceView = WorkspaceView;
  type RendererChatStatus = ChatSemanticStatus;
  const Terminal: typeof XTerm;
}
export {};
