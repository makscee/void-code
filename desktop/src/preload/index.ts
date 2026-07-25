import { contextBridge, ipcRenderer } from 'electron';
import { IPC, sessionId } from '../shared/contract';
import type { ExitEvent, OutputEvent, SessionId, SubscribeRequest, SubscriptionKind, TerminalApi, Unsubscribe } from '../shared/contract';

const active = new Map<string, { channel: string; listener: (_event: Electron.IpcRendererEvent, payload: unknown) => void; request: SubscribeRequest }>();
function subscribe<T extends OutputEvent | ExitEvent>(kind: SubscriptionKind, id: SessionId, listener: (event: T) => void): Unsubscribe {
  const validId = sessionId(id);
  if (typeof listener !== 'function') throw new Error('listener must be a function');
  const subscriptionId = crypto.randomUUID();
  const request: SubscribeRequest = { sessionId: validId, kind, subscriptionId };
  const channel = kind === 'output' ? IPC.output : IPC.exit;
  const wrapped = (_event: Electron.IpcRendererEvent, payload: unknown): void => {
    const event = payload as T;
    if (event?.sessionId === validId) listener(event);
  };
  const reply = ipcRenderer.sendSync(IPC.subscribe, request) as { ok: boolean; error?: string };
  if (!reply.ok) throw new Error(reply.error ?? 'subscription rejected');
  ipcRenderer.on(channel, wrapped);
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
  onOutput: (id, listener) => subscribe('output', id, listener),
  onExit: (id, listener) => subscribe('exit', id, listener),
  teardown,
};
Object.freeze(api);
window.addEventListener('beforeunload', teardown, { once: true });
contextBridge.exposeInMainWorld('voidTerminal', api);
