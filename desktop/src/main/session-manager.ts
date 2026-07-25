import type { IPty } from 'node-pty';
import type { ExitEvent, OutputEvent, SessionStatus, SubscribeRequest } from '../shared/contract';

export interface TerminalProcess {
  write(data: string): void;
  resize(cols: number, rows: number): void;
  kill(): void;
  onData(listener: (data: string) => void): { dispose(): void };
  onExit(listener: (event: { exitCode: number; signal?: number }) => void): { dispose(): void };
}
export type ProcessFactory = () => TerminalProcess;
export type Deliver = (ownerId: number, channel: string, payload: OutputEvent | ExitEvent) => void;
interface OwnedSession {
  ownerId: number;
  process: TerminalProcess;
  status: SessionStatus;
  disposables: Array<{ dispose(): void }>;
}
interface Subscription extends SubscribeRequest { ownerId: number }

export class SessionManager {
  private readonly sessions = new Map<string, OwnedSession>();
  private readonly subscriptions = new Map<string, Subscription>();
  constructor(private readonly createProcess: ProcessFactory, private readonly deliver: Deliver) {}

  start(ownerId: number, sessionId: string): SessionStatus {
    if (this.sessions.has(sessionId)) throw new Error('session already owned');
    const process = this.createProcess();
    const session: OwnedSession = { ownerId, process, status: 'running', disposables: [] };
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
    this.owned(ownerId, request.sessionId);
    if (this.subscriptions.has(request.subscriptionId)) throw new Error('subscription already exists');
    this.subscriptions.set(request.subscriptionId, { ...request, ownerId });
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
  private owned(ownerId: number, sessionId: string): OwnedSession {
    const session = this.sessions.get(sessionId);
    if (!session || session.ownerId !== ownerId) throw new Error('unknown session');
    return session;
  }
  private emit(ownerId: number, sessionId: string, kind: 'output' | 'exit', payload: OutputEvent | ExitEvent): void {
    if ([...this.subscriptions.values()].some((subscription) => subscription.ownerId === ownerId && subscription.sessionId === sessionId && subscription.kind === kind)) {
      this.deliver(ownerId, kind === 'output' ? 'terminal:output' : 'terminal:exit', payload);
    }
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

export function wrapPty(process: IPty): TerminalProcess { return process; }
