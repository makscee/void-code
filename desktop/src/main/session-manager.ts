import type { IPty } from 'node-pty';
import type { ChatSemanticStatus, ExitEvent, OutputEvent, SessionStatus, StartRequest, SubscribeRequest } from '../shared/contract';
import type { StatusChannelStore, StatusWriteAuthority } from './status-channel';

export interface TerminalProcess {
  write(data: string): void;
  resize(cols: number, rows: number): void;
  kill(): void;
  onData(listener: (data: string) => void): { dispose(): void };
  onExit(listener: (event: { exitCode: number; signal?: number }) => void): { dispose(): void };
}
export type ProcessFactory = (request: StartRequest, authority?: StatusWriteAuthority) => TerminalProcess;
export type Deliver = (ownerId: number, channel: string, payload: OutputEvent | ExitEvent | ChatSemanticStatus) => void;
export interface StartResult { status: SessionStatus; showSharedFilesWarning: boolean }
interface OwnedSession {
  ownerId: number;
  process: TerminalProcess;
  status: SessionStatus;
  real: boolean;
  disposables: Array<{ dispose(): void }>;
  pending: { output: OutputEvent[]; exit: ExitEvent[]; status: ChatSemanticStatus[] };
}
interface Subscription extends SubscribeRequest { ownerId: number }

export class SessionManager {
  private readonly sessions = new Map<string, OwnedSession>();
  private readonly subscriptions = new Map<string, Subscription>();
  private readonly disclosedOwners = new Set<number>();
  constructor(private readonly createProcess: ProcessFactory, private readonly deliver: Deliver, private readonly statusChannels?: StatusChannelStore) {}

  start(ownerId: number, request: StartRequest): StartResult {
    const { sessionId } = request;
    if (this.sessions.has(sessionId)) throw new Error('session already owned');
    const liveBefore = this.liveRuntimeCount(ownerId);
    const real = !('fixture' in request);
    const authority = real ? this.statusChannels?.create(ownerId, sessionId) : undefined;
    let process: TerminalProcess;
    try { process = this.createProcess(request, authority); } catch (error) { if (authority) this.statusChannels?.close(ownerId, sessionId); throw error; }
    const session: OwnedSession = { ownerId, process, status: 'running', real, disposables: [], pending: { output: [], exit: [], status: [] } };
    this.sessions.set(sessionId, session);
    session.disposables.push(process.onData((data) => this.emit(ownerId, sessionId, 'output', { sessionId, data })));
    session.disposables.push(process.onExit((event) => {
      if (this.sessions.get(sessionId) !== session) return;
      session.status = 'exited';
      this.emit(ownerId, sessionId, 'exit', { sessionId, ...event });
      this.removeSubscriptions(ownerId, sessionId);
    }));
    const showSharedFilesWarning = real && request.mode === 'create' && liveBefore === 1 && this.liveRuntimeCount(ownerId) === 2 && !this.disclosedOwners.has(ownerId);
    if (showSharedFilesWarning) this.disclosedOwners.add(ownerId);
    return { status: session.status, showSharedFilesWarning };
  }

  input(ownerId: number, sessionId: string, data: string): void { this.owned(ownerId, sessionId).process.write(data); }
  resize(ownerId: number, sessionId: string, cols: number, rows: number): void { this.owned(ownerId, sessionId).process.resize(cols, rows); }
  status(ownerId: number, sessionId: string): SessionStatus { return this.owned(ownerId, sessionId).status; }
  lifecycleStatus(ownerId: number, sessionId: string): ChatSemanticStatus {
    this.owned(ownerId, sessionId);
    return this.statusChannels?.status(ownerId, sessionId) ?? { sessionId, state: 'running', unread: false, diagnostic: 'status channel unavailable' };
  }
  clearUnread(ownerId: number, sessionId: string): ChatSemanticStatus {
    this.owned(ownerId, sessionId);
    return this.statusChannels?.clearUnread(ownerId, sessionId) ?? { sessionId, state: 'running', unread: false, diagnostic: 'status channel unavailable' };
  }
  lifecycleChanged(ownerId: number, event: ChatSemanticStatus): void {
    const session = this.sessions.get(event.sessionId);
    if (!session || session.ownerId !== ownerId) return;
    this.emit(ownerId, event.sessionId, 'status', event);
  }
  stop(ownerId: number, sessionId: string): void {
    const session = this.owned(ownerId, sessionId);
    session.process.kill();
    this.destroySession(sessionId, session);
  }
  stopIfOwned(ownerId: number, sessionId: string): boolean {
    if (!this.sessions.has(sessionId)) return false;
    this.stop(ownerId, sessionId);
    return true;
  }
  subscribe(ownerId: number, request: SubscribeRequest): void {
    const session = this.owned(ownerId, request.sessionId);
    if (this.subscriptions.has(request.subscriptionId)) throw new Error('subscription already exists');
    this.subscriptions.set(request.subscriptionId, { ...request, ownerId });
    for (const payload of session.pending[request.kind].splice(0)) this.deliver(ownerId, request.kind === 'output' ? 'terminal:output' : request.kind === 'exit' ? 'terminal:exit' : 'chat:lifecycle', payload);
    if (request.kind === 'exit' && session.status === 'exited') this.removeSubscriptions(ownerId, request.sessionId);
  }
  unsubscribe(ownerId: number, request: SubscribeRequest): void {
    const existing = this.subscriptions.get(request.subscriptionId);
    if (!existing || existing.ownerId !== ownerId || existing.sessionId !== request.sessionId || existing.kind !== request.kind) throw new Error('unknown subscription');
    this.subscriptions.delete(request.subscriptionId);
  }
  teardownOwner(ownerId: number): void {
    for (const [sessionId, session] of this.sessions) {
      if (session.ownerId === ownerId) {
        session.process.kill();
        this.destroySession(sessionId, session);
      }
    }
    for (const [id, subscription] of this.subscriptions) if (subscription.ownerId === ownerId) this.subscriptions.delete(id);
    this.disclosedOwners.delete(ownerId);
    this.statusChannels?.closeOwner(ownerId);
  }
  teardownAll(): void {
    for (const session of [...this.sessions.values()]) this.teardownOwner(session.ownerId);
    this.statusChannels?.closeAll();
  }
  private liveRuntimeCount(ownerId: number): number {
    return [...this.sessions.values()].filter((session) => session.ownerId === ownerId && session.real && session.status === 'running').length;
  }
  private owned(ownerId: number, sessionId: string): OwnedSession {
    const session = this.sessions.get(sessionId);
    if (!session || session.ownerId !== ownerId) throw new Error('unknown session');
    return session;
  }
  private emit(ownerId: number, sessionId: string, kind: 'output' | 'exit' | 'status', payload: OutputEvent | ExitEvent | ChatSemanticStatus): void {
    if ([...this.subscriptions.values()].some((subscription) => subscription.ownerId === ownerId && subscription.sessionId === sessionId && subscription.kind === kind)) {
      this.deliver(ownerId, kind === 'output' ? 'terminal:output' : kind === 'exit' ? 'terminal:exit' : 'chat:lifecycle', payload);
      return;
    }
    const session = this.sessions.get(sessionId);
    if (session?.ownerId === ownerId) {
      if (kind === 'status') session.pending.status.splice(0);
      session.pending[kind].push(payload as OutputEvent & ExitEvent & ChatSemanticStatus);
    }
  }
  private removeSubscriptions(ownerId: number, sessionId: string): void {
    for (const [id, subscription] of this.subscriptions) if (subscription.ownerId === ownerId && subscription.sessionId === sessionId) this.subscriptions.delete(id);
  }
  private destroySession(sessionId: string, session: OwnedSession): void {
    if (this.sessions.get(sessionId) !== session) return;
    this.sessions.delete(sessionId);
    this.statusChannels?.close(session.ownerId, sessionId);
    this.removeSubscriptions(session.ownerId, sessionId);
    for (const disposable of session.disposables) disposable.dispose();
  }
}

export function wrapPty(process: IPty): TerminalProcess {
  let stopping = false;
  const stopGroup = (): void => {
    if (stopping) return;
    stopping = true;
    if (globalThis.process.platform === 'win32') {
      // node-pty enumerates the owned ConPTY console process list before
      // closing its handles, avoiding both name-based kills and orphaned children.
      try { process.kill(); } catch { /* ConPTY already closed */ }
      return;
    }
    // node-pty makes the Unix child a process-group leader.
    try { globalThis.process.kill(-process.pid, 'SIGTERM'); } catch { try { process.kill(); } catch { /* already exited */ } }
    const timer = setTimeout(() => { try { globalThis.process.kill(-process.pid, 'SIGKILL'); } catch { /* group exited */ } }, 1000);
    timer.unref();
  };
  return {
    write: (data) => process.write(data),
    resize: (cols, rows) => process.resize(cols, rows),
    onData: (listener) => process.onData(listener),
    onExit: (listener) => process.onExit((event) => { stopGroup(); listener(event); }),
    kill: stopGroup,
  };
}
