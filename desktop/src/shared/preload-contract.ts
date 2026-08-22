export const IPC = {
  start: 'terminal:start', input: 'terminal:input', resize: 'terminal:resize', stop: 'terminal:stop', status: 'terminal:status',
  chooseFolder: 'terminal:choose-folder', openLink: 'terminal:open-link', subscribe: 'terminal:subscribe', unsubscribe: 'terminal:unsubscribe',
  output: 'terminal:output', exit: 'terminal:exit', lifecycle: 'chat:lifecycle', lifecycleStatus: 'chat:lifecycle-status',
  workspaceLoad: 'workspace:load', workspaceChoose: 'workspace:choose', workspaceRemove: 'workspace:remove',
  workspaceNewChat: 'workspace:new-chat', workspaceSelect: 'workspace:select', workspaceClose: 'workspace:close', workspaceResume: 'workspace:resume',
  supportCopy: 'support:copy', supportSave: 'support:save',
  authStatus: 'auth:status', authLoginStart: 'auth:login-start', authLoginEvent: 'auth:login-event', authCodeCopy: 'auth:code-copy',
} as const;

const FIXTURE_ID = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/;
export function preloadSessionId(value: unknown): string {
  if (typeof value !== 'string' || !FIXTURE_ID.test(value)) throw new Error('invalid sessionId');
  return value;
}
