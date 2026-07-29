import type { LoginStatus } from '../shared/contract';

export type LoginCompletionAction = 'none' | 'create' | 'retry';
export type AutomaticLoginRetry = { attempt: number; delayMs: number; mode: 'create' | 'resume' };

const AUTOMATIC_LOGIN_RETRY_DELAYS_MS = [1000, 2000, 4000, 8000, 16_000, 30_000] as const;
const AUTOMATIC_LOGIN_EARLY_EXIT_MS = 60_000;

export function automaticLoginRetry(mode: 'create' | 'resume', attempt: number, elapsedMs: number): AutomaticLoginRetry | null {
  if (elapsedMs > AUTOMATIC_LOGIN_EARLY_EXIT_MS || attempt < 0 || attempt >= AUTOMATIC_LOGIN_RETRY_DELAYS_MS.length) return null;
  return { mode, attempt: attempt + 1, delayMs: AUTOMATIC_LOGIN_RETRY_DELAYS_MS[attempt] };
}

export function loginCompletionAction(status: LoginStatus, hasSelectedChat: boolean): LoginCompletionAction {
  if (status.state !== 'succeeded') return 'none';
  return hasSelectedChat ? 'retry' : 'create';
}
