import type { LoginStatus } from '../shared/contract';

export type LoginCompletionAction = 'none' | 'create' | 'retry';

export function loginCompletionAction(status: LoginStatus, hasSelectedChat: boolean): LoginCompletionAction {
  if (status.state !== 'succeeded') return 'none';
  return hasSelectedChat ? 'retry' : 'create';
}
