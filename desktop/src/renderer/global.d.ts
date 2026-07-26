import type { ChatSemanticStatus, TabRecord, TerminalApi, WorkspaceView } from '../shared/contract';

declare global {
  interface Window {
    voidTerminal: TerminalApi;
    __resolveProductionPixelProbe?: (reply: {
      requestId: string;
      summary: { source: 'cdp-bitmap'; distinctVisibleRgb: number; contrastingPixels: number; maxContrast: number; chromaticHueBins: number; visibleRgb: string[] };
    }) => void;
  }
  type RendererTabRecord = TabRecord;
  type RendererWorkspaceView = WorkspaceView;
  type RendererChatStatus = ChatSemanticStatus;
}
export {};
