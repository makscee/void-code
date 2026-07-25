import type { IPty } from 'node-pty';
import type { ExitEvent, OutputEvent, SessionStatus, StartRequest, SubscribeRequest } from '../shared/contract';

export interface TerminalProcess {
  write(data: string): void;
  resize(cols: number, rows: number): void;
  kill(): void;
  onData(listener: (data: string) => void): { dispose(): void };
  onExit(listener: (event: { exitCode: number; signal?: number }) => void): { dispose(): void };
}
export type ProcessFactory = (request: StartRequest) => TerminalProcess;
export type Deliver = (ownerId: number, channel: string, payload: OutputEvent | ExitEvent) => void;
interface OwnedSession {
  ownerId: number;
  process: TerminalProcess;
  status: SessionStatus;
  disposables: Array<{ dispose(): void }>;
  pending: { output: OutputEvent[]; exit: ExitEvent[] };
}
interface Subscription extends SubscribeRequest { ownerId: number }

export class SessionManager {
  private readonly sessions = new Map<string, OwnedSession>();
  private readonly subscriptions = new Map<string, Subscription>();
  constructor(private readonly createProcess: ProcessFactory, private readonly deliver: Deliver) {}

  start(ownerId: number, request: StartRequest): SessionStatus {
    const { sessionId } = request;
    if (this.sessions.has(sessionId)) throw new Error('session already owned');
    const process = this.createProcess(request);
    const session: OwnedSession = { ownerId, process, status: 'running', disposables: [], pending: { output: [], exit: [] } };
    this.sessions.set(sessionId, session);
    session.disposables.push(process.onData((data) => this.emit(ownerId, sessionId, 'output', { sessionId, data })));
    session.disposables.push(process.onExit((event) => {
      if (this.sessions.get(sessionId) !== session) return;
      session.status = 'exited';
      this.emit(ownerId, sessionId, 'exit', { sessionId, ...event });
      this.removeSubscriptions(ownerId, sessionId);
    }));
    return session.status;
  }

  input(ownerId: number, sessionId: string, data: string): void { this.owned(ownerId, sessionId).process.write(data); }
  resize(ownerId: number, sessionId: string, cols: number, rows: number): void { this.owned(ownerId, sessionId).process.resize(cols, rows); }
  status(ownerId: number, sessionId: string): SessionStatus { return this.owned(ownerId, sessionId).status; }
  stop(ownerId: number, sessionId: string): void {
    const session = this.owned(ownerId, sessionId);
    session.process.kill();
    this.destroySession(sessionId, session);
  }
  subscribe(ownerId: number, request: SubscribeRequest): void {
    const session = this.owned(ownerId, request.sessionId);
    if (this.subscriptions.has(request.subscriptionId)) throw new Error('subscription already exists');
    this.subscriptions.set(request.subscriptionId, { ...request, ownerId });
    for (const payload of session.pending[request.kind].splice(0)) this.deliver(ownerId, request.kind === 'output' ? 'terminal:output' : 'terminal:exit', payload);
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
  }
  teardownAll(): void {
    for (const session of [...this.sessions.values()]) this.teardownOwner(session.ownerId);
  }
  private owned(ownerId: number, sessionId: string): OwnedSession {
    const session = this.sessions.get(sessionId);
    if (!session || session.ownerId !== ownerId) throw new Error('unknown session');
    return session;
  }
  private emit(ownerId: number, sessionId: string, kind: 'output' | 'exit', payload: OutputEvent | ExitEvent): void {
    if ([...this.subscriptions.values()].some((subscription) => subscription.ownerId === ownerId && subscription.sessionId === sessionId && subscription.kind === kind)) {
      this.deliver(ownerId, kind === 'output' ? 'terminal:output' : 'terminal:exit', payload);
      return;
    }
    const session = this.sessions.get(sessionId);
    if (session?.ownerId === ownerId) session.pending[kind].push(payload as OutputEvent & ExitEvent);
  }
  private removeSubscriptions(ownerId: number, sessionId: string): void {
    for (const [id, subscription] of this.subscriptions) if (subscription.ownerId === ownerId && subscription.sessionId === sessionId) this.subscriptions.delete(id);
  }
  private destroySession(sessionId: string, session: OwnedSession): void {
    if (this.sessions.get(sessionId) !== session) return;
    this.sessions.delete(sessionId);
    this.removeSubscriptions(session.ownerId, sessionId);
    for (const disposable of session.disposables) disposable.dispose();
  }
}

export function wrapPty(process: IPty): TerminalProcess {
  let stopping = false;
  const stopGroup = (): void => {
    if (stopping) return;
    stopping = true;
    // node-pty makes the child a process-group leader. Addressing that group
    // reaps vc and its private Node/Pi descendants without name-based kills.
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
