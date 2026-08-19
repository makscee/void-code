import type { RecoveryCode } from '../shared/contract';

export interface RecoveryGuidance { heading: string; detail: string; canRestart: boolean }

export const RECOVERY_GUIDANCE: Readonly<Record<RecoveryCode, RecoveryGuidance>> = Object.freeze({
  NONE: { heading: '', detail: '', canRestart: false },
  AUTH_PREFLIGHT_REQUIRED: {
    heading: 'Before your first chat',
    detail: 'Use Sign In in Void Code and complete browser authorization. Check your network and try again if authorization does not complete.',
    canRestart: false,
  },
  SESSION_START_FAILED: {
    heading: 'Chat could not start',
    detail: 'Ask your operator to verify existing VC sign-in and network access, then try Restart. If it still fails, copy or save a Support Report.',
    canRestart: true,
  },
  RUNTIME_EXITED: {
    heading: 'Chat stopped',
    detail: 'Restart resumes this chat; no shell was opened. If it stops again, ask your operator to verify sign-in and network access and save a Support Report.',
    canRestart: true,
  },
  WORKSPACE_MISSING: {
    heading: 'Workspace unavailable',
    detail: 'Locate the moved folder or remove this saved workspace. Void Code will not use a fallback folder.',
    canRestart: false,
  },
  SESSION_MISSING: {
    heading: 'Saved chat unavailable',
    detail: 'Close this saved chat and start a new one. Other saved chats and files are not removed.',
    canRestart: false,
  },
});
