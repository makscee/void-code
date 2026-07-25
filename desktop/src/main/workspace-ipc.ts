import { chatRequest } from '../shared/contract';
import type { WorkspaceView } from '../shared/contract';
import type { SessionManager } from './session-manager';
import type { WorkspaceStore } from './workspace-store';

export function closeWorkspaceChat(manager: SessionManager, workspace: WorkspaceStore, ownerId: number, raw: unknown): WorkspaceView {
  const sessionId = chatRequest(raw).sessionId;
  workspace.assertClose(sessionId);
  manager.stopIfOwned(ownerId, sessionId);
  return workspace.close(sessionId);
}
