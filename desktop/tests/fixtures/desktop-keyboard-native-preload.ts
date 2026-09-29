import { contextBridge } from 'electron';

const calls = { selects: [] as string[], starts: [] as string[], inputs: [] as { sessionId: string; data: string }[] };
const view = { recoveryPath: null, workspace: { path: '/synthetic-not-opened', selectedId: 'A', tabs: ['A', 'R', 'B', 'C'].map(id => ({ id, title: id, location: id === 'R' ? 'recent' : 'active', createdAt: 1, updatedAt: 1 })) } };
const noop = () => () => undefined;
const forbidden = () => { throw new Error('unsupported synthetic bridge operation'); };
contextBridge.exposeInMainWorld('nativeKeysReceipt', () => structuredClone(calls));
contextBridge.exposeInMainWorld('voidTerminal', {
  appVersion: async () => 'fixture',
  auth: { status: async () => ({ ok: true, status: { authState: 'signed_in' } }), onLoginEvent: noop },
  workspace: {
    load: async () => structuredClone(view),
    select: async (id: string) => { calls.selects.push(id); view.workspace.selectedId = id; return structuredClone(view); },
    resume: forbidden, newChat: forbidden,
  },
  start: async ({ sessionId }: { sessionId: string }) => { calls.starts.push(sessionId); return { showSharedFilesWarning: false }; },
  input: async (input: { sessionId: string; data: string }) => { calls.inputs.push(input); },
  resize: async () => undefined,
  lifecycleStatus: async ({ sessionId }: { sessionId: string }) => ({ status: { sessionId, state: 'idle', unread: false } }),
  onOutput: noop, onExit: noop, onStatus: noop,
  clipboard: { read: forbidden, write: forbidden },
});
