import { contextBridge, ipcRenderer } from 'electron';
import { IPC, loginStatus, sessionId } from '../shared/contract';
import type { ChatSemanticStatus, ExitEvent, OutputEvent, SessionId, SubscribeRequest, SubscriptionKind, TerminalApi, Unsubscribe } from '../shared/contract';

const active = new Map<string, { channel: string; listener: (_event: Electron.IpcRendererEvent, payload: unknown) => void; request: SubscribeRequest }>();
const loginListeners = new Set<(_event: Electron.IpcRendererEvent, payload: unknown) => void>();
function subscribe<T extends OutputEvent | ExitEvent | ChatSemanticStatus>(kind: SubscriptionKind, id: SessionId, listener: (event: T) => void): Unsubscribe {
  const validId = sessionId(id, true);
  if (typeof listener !== 'function') throw new Error('listener must be a function');
  const subscriptionId = crypto.randomUUID();
  const request: SubscribeRequest = { sessionId: validId, kind, subscriptionId };
  const channel = kind === 'output' ? IPC.output : kind === 'exit' ? IPC.exit : IPC.lifecycle;
  const wrapped = (_event: Electron.IpcRendererEvent, payload: unknown): void => {
    const event = payload as T;
    if (event?.sessionId === validId) listener(event);
  };
  ipcRenderer.on(channel, wrapped);
  const reply = ipcRenderer.sendSync(IPC.subscribe, request) as { ok: boolean; error?: string };
  if (!reply.ok) {
    ipcRenderer.removeListener(channel, wrapped);
    throw new Error(reply.error ?? 'subscription rejected');
  }
  active.set(subscriptionId, { channel, listener: wrapped, request });
  return () => {
    const existing = active.get(subscriptionId);
    if (!existing) return;
    active.delete(subscriptionId);
    ipcRenderer.removeListener(existing.channel, existing.listener);
    ipcRenderer.send(IPC.unsubscribe, existing.request);
  };
}
function teardown(): void {
  for (const listener of loginListeners) ipcRenderer.removeListener(IPC.loginChanged, listener);
  loginListeners.clear();
  for (const id of [...active.keys()]) {
    const existing = active.get(id);
    if (existing) {
      active.delete(id);
      ipcRenderer.removeListener(existing.channel, existing.listener);
      ipcRenderer.send(IPC.unsubscribe, existing.request);
    }
  }
}
const api: TerminalApi = {
  start: (request) => ipcRenderer.invoke(IPC.start, request),
  input: (request) => ipcRenderer.invoke(IPC.input, request),
  resize: (request) => ipcRenderer.invoke(IPC.resize, request),
  stop: (request) => ipcRenderer.invoke(IPC.stop, request),
  status: (request) => ipcRenderer.invoke(IPC.status, request),
  lifecycleStatus: (request) => ipcRenderer.invoke(IPC.lifecycleStatus, request),
  chooseFolder: () => ipcRenderer.invoke(IPC.chooseFolder),
  openLink: (url) => ipcRenderer.invoke(IPC.openLink, { url }),
  support: Object.freeze({
    copy: (request) => ipcRenderer.invoke(IPC.supportCopy, request),
    save: (request) => ipcRenderer.invoke(IPC.supportSave, request),
  }),
  login: Object.freeze({
    start: () => ipcRenderer.invoke(IPC.loginStart),
    cancel: () => ipcRenderer.invoke(IPC.loginCancel),
    status: () => ipcRenderer.invoke(IPC.loginStatus),
    onStatus: (listener) => {
      if (typeof listener !== 'function') throw new Error('listener must be a function');
      const wrapped = (_event: Electron.IpcRendererEvent, payload: unknown): void => listener(loginStatus(payload));
      loginListeners.add(wrapped); ipcRenderer.on(IPC.loginChanged, wrapped);
      return () => { loginListeners.delete(wrapped); ipcRenderer.removeListener(IPC.loginChanged, wrapped); };
    },
  }),
  onOutput: (id, listener) => subscribe('output', id, listener),
  onExit: (id, listener) => subscribe('exit', id, listener),
  onStatus: (id, listener) => subscribe('status', id, listener),
  teardown,
  workspace: Object.freeze({
    load: () => ipcRenderer.invoke(IPC.workspaceLoad),
    choose: () => ipcRenderer.invoke(IPC.workspaceChoose),
    remove: () => ipcRenderer.invoke(IPC.workspaceRemove),
    newChat: () => ipcRenderer.invoke(IPC.workspaceNewChat),
    select: (id) => ipcRenderer.invoke(IPC.workspaceSelect, { sessionId: id }),
    close: (id) => ipcRenderer.invoke(IPC.workspaceClose, { sessionId: id }),
    resume: (id) => ipcRenderer.invoke(IPC.workspaceResume, { sessionId: id }),
  }),
};
Object.freeze(api);
window.addEventListener('beforeunload', teardown, { once: true });
contextBridge.exposeInMainWorld('voidTerminal', api);
