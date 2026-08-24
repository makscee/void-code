import type { RecoveryCode } from '../shared/contract';

export interface RecoveryGuidance { heading: string; detail: string; canRestart: boolean }

export const RECOVERY_GUIDANCE: Readonly<Record<RecoveryCode, RecoveryGuidance>> = Object.freeze({
  NONE: { heading: '', detail: '', canRestart: false },
  AUTH_PREFLIGHT_REQUIRED: {
    heading: 'Before your first chat',
    detail: 'Sign in directly from inside this app — start the guided sign-in below and make sure you have network access, then start a new chat.',
    canRestart: false,
  },
  SESSION_START_FAILED: {
    heading: 'Chat could not start',
    detail: 'If your sign-in has expired, sign in again; otherwise check your network connection, then try Restart. Still stuck? Save a Support Report.',
    canRestart: true,
  },
  RUNTIME_EXITED: {
    heading: 'Chat stopped',
    detail: 'Restart resumes this chat; no shell was opened. If it keeps stopping, check your network connection, then save a Support Report.',
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
