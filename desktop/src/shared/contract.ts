import path from 'node:path';
import type { AccessRequestResult } from '../main/access-request';
import type { AuthStatus, StatusResult } from '../main/auth-session';
import type { AuthLoginPush } from '../main/auth-ipc';
export { IPC } from './preload-contract';
export type { AccessRequestResult, AuthStatus, StatusResult, AuthLoginPush };

export type SessionId = string;
export type SubscriptionKind = 'output' | 'exit' | 'status';
export type SessionStatus = 'running' | 'stopped' | 'exited';
export type ChatLifecycleState = 'running' | 'working' | 'ready';
export interface ChatLifecycleEvent { version: 1; chatId: string; generation: number; sequence: number; state: 'Working' | 'Ready'; timestamp: string }
export interface ChatSemanticStatus { sessionId: SessionId; state: ChatLifecycleState; unread: boolean; diagnostic?: string }
export interface ChatStatusReply { sessionId: SessionId; status: ChatSemanticStatus }

export interface RealStartRequest { sessionId: SessionId; cwd: string; mode: 'create' | 'resume' }
export interface FixtureStartRequest { sessionId: SessionId; fixture: 'roundTrip' | 'terminalFidelity' }
export type StartRequest = RealStartRequest | FixtureStartRequest;
export interface InputRequest { sessionId: SessionId; data: string }
export interface ResizeRequest { sessionId: SessionId; cols: number; rows: number }
export interface SessionRequest { sessionId: SessionId }
export interface SubscribeRequest extends SessionRequest { kind: SubscriptionKind; subscriptionId: string }
export interface OutputEvent { sessionId: SessionId; data: string }
export interface ExitEvent { sessionId: SessionId; exitCode: number; signal?: number }
export interface StatusReply { sessionId: SessionId; status: SessionStatus }
export interface StartReply extends StatusReply { showSharedFilesWarning: boolean }
export type Unsubscribe = () => void;
export interface TabRecord { id: string; title: string; location: 'active' | 'recent' }
export interface WorkspaceRecord { path: string; tabs: TabRecord[]; selectedId: string | null }
export interface WorkspaceView { workspace: WorkspaceRecord | null; recoveryPath: string | null }
export interface NewChatReply { view: WorkspaceView }
export type RuntimeSupportState = 'not_started' | 'running' | 'ended' | 'start_failed';
export type RecoveryCode = 'NONE' | 'AUTH_PREFLIGHT_REQUIRED' | 'SESSION_START_FAILED' | 'RUNTIME_EXITED' | 'WORKSPACE_MISSING' | 'SESSION_MISSING';
export interface SupportRequest { runtime: RuntimeSupportState; recoveryCode: RecoveryCode }
export interface SupportResult { action: 'copied' | 'saved' | 'cancelled' }

export interface TerminalApi {
  start(request: StartRequest): Promise<StartReply>;
  input(request: InputRequest): Promise<void>;
  resize(request: ResizeRequest): Promise<void>;
  stop(request: SessionRequest): Promise<void>;
  status(request: SessionRequest): Promise<StatusReply>;
  lifecycleStatus(request: SessionRequest): Promise<ChatStatusReply>;
  chooseFolder(): Promise<string | null>;
  openLink(url: string): Promise<void>;
  support: {
    copy(request: SupportRequest): Promise<SupportResult>;
    save(request: SupportRequest): Promise<SupportResult>;
  };
  onOutput(sessionId: SessionId, listener: (event: OutputEvent) => void): Unsubscribe;
  onExit(sessionId: SessionId, listener: (event: ExitEvent) => void): Unsubscribe;
  onStatus(sessionId: SessionId, listener: (event: ChatSemanticStatus) => void): Unsubscribe;
  teardown(): void;
  workspace: {
    load(): Promise<WorkspaceView>;
    choose(): Promise<WorkspaceView | null>;
    remove(): Promise<WorkspaceView>;
    newChat(): Promise<NewChatReply>;
    select(sessionId: SessionId): Promise<WorkspaceView>;
    close(sessionId: SessionId): Promise<WorkspaceView>;
    resume(sessionId: SessionId): Promise<WorkspaceView>;
  };
  auth: {
    status(): Promise<StatusResult>;
    loginStart(): Promise<{ loginId: string }>;
    onLoginEvent(listener: (event: AuthLoginPush) => void): Unsubscribe;
    copyCode(code: string): Promise<void>;
    // ask=false reads the state and files nothing: showing the refusal screen must not put a row
    // in the queue. Only the button passes true.
    accessRequest(ask: boolean): Promise<AccessRequestResult>;
  };
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const FIXTURE_ID = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/;
const SUBSCRIPTION_ID = /^[a-f0-9-]{36}$/;
function ownedObject(value: unknown, keys: readonly string[]): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error('request must be an object');
  const object = value as Record<string, unknown>;
  const actual = Object.keys(object).sort(); const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) throw new Error('request contains unknown or missing fields');
  return object;
}
export function sessionId(value: unknown, fixture = false): SessionId {
  if (typeof value !== 'string' || !(fixture ? FIXTURE_ID : UUID).test(value)) throw new Error('invalid sessionId');
  return value;
}
function isAbsoluteWorkspacePath(value: string): boolean {
  const windowsDrive = /^[A-Za-z]:[\\/]/.test(value);
  const windowsShare = /^\\\\[^\\/]+\\[^\\/]+(?:\\|$)/.test(value);
  return path.posix.isAbsolute(value) || windowsDrive || windowsShare;
}
export function startRequest(value: unknown): StartRequest {
  if (typeof value === 'object' && value !== null && 'fixture' in value) {
    const object = ownedObject(value, ['sessionId', 'fixture']);
    if (object.fixture !== 'roundTrip' && object.fixture !== 'terminalFidelity') throw new Error('only an owned fixture is allowed');
    return { sessionId: sessionId(object.sessionId, true), fixture: object.fixture };
  }
  const object = ownedObject(value, ['sessionId', 'cwd', 'mode']);
  if (object.mode !== 'create' && object.mode !== 'resume') throw new Error('invalid session mode');
  if (typeof object.cwd !== 'string' || !isAbsoluteWorkspacePath(object.cwd) || Buffer.byteLength(object.cwd, 'utf8') > 4096) throw new Error('invalid cwd');
  return { sessionId: sessionId(object.sessionId), cwd: object.cwd, mode: object.mode };
}
export function supportRequest(value: unknown): SupportRequest {
  const object = ownedObject(value, ['runtime', 'recoveryCode']);
  const runtimes: RuntimeSupportState[] = ['not_started', 'running', 'ended', 'start_failed'];
  const recoveryCodes: RecoveryCode[] = ['NONE', 'AUTH_PREFLIGHT_REQUIRED', 'SESSION_START_FAILED', 'RUNTIME_EXITED', 'WORKSPACE_MISSING', 'SESSION_MISSING'];
  if (!runtimes.includes(object.runtime as RuntimeSupportState) || !recoveryCodes.includes(object.recoveryCode as RecoveryCode)) throw new Error('invalid support context');
  return { runtime: object.runtime as RuntimeSupportState, recoveryCode: object.recoveryCode as RecoveryCode };
}
export function sessionRequest(value: unknown): SessionRequest { const object = ownedObject(value, ['sessionId']); return { sessionId: sessionId(object.sessionId, true) }; }
export function chatRequest(value: unknown): SessionRequest { const object = ownedObject(value, ['sessionId']); return { sessionId: sessionId(object.sessionId) }; }
export function inputRequest(value: unknown): InputRequest {
  const object = ownedObject(value, ['sessionId', 'data']);
  if (typeof object.data !== 'string' || Buffer.byteLength(object.data, 'utf8') > 65_536) throw new Error('invalid input data');
  return { sessionId: sessionId(object.sessionId, true), data: object.data };
}
export function resizeRequest(value: unknown): ResizeRequest {
  const object = ownedObject(value, ['sessionId', 'cols', 'rows']);
  if (!Number.isInteger(object.cols) || !Number.isInteger(object.rows) || Number(object.cols) < 2 || Number(object.cols) > 1000 || Number(object.rows) < 1 || Number(object.rows) > 1000) throw new Error('invalid terminal size');
  return { sessionId: sessionId(object.sessionId, true), cols: Number(object.cols), rows: Number(object.rows) };
}
export function subscribeRequest(value: unknown): SubscribeRequest {
  const object = ownedObject(value, ['sessionId', 'kind', 'subscriptionId']);
  if (object.kind !== 'output' && object.kind !== 'exit' && object.kind !== 'status') throw new Error('invalid subscription kind');
  if (typeof object.subscriptionId !== 'string' || !SUBSCRIPTION_ID.test(object.subscriptionId)) throw new Error('invalid subscriptionId');
  return { sessionId: sessionId(object.sessionId, true), kind: object.kind, subscriptionId: object.subscriptionId };
}
export function linkRequest(value: unknown): string {
  const object = ownedObject(value, ['url']);
  if (typeof object.url !== 'string') throw new Error('invalid link');
  const parsed = new URL(object.url);
  if (parsed.protocol !== 'https:' && parsed.protocol !== 'http:') throw new Error('invalid link protocol');
  return parsed.toString();
}
// A sign-in device code, not a SupportRequest — copySupportReport builds its own report from a
// {runtime, recoveryCode} context and has no way to carry an arbitrary code string.
// The one bit that decides whether this invocation looks or files, validated like every other
// request that crosses the boundary — an absent or non-boolean `ask` must not default to either
// meaning by accident.
export function accessRequestRequest(value: unknown): boolean {
  const object = ownedObject(value, ['ask']);
  if (typeof object.ask !== 'boolean') throw new Error('invalid access request');
  return object.ask;
}
export function codeCopyRequest(value: unknown): string {
  const object = ownedObject(value, ['code']);
  if (typeof object.code !== 'string' || object.code.length === 0 || object.code.length > 64) throw new Error('invalid code');
  return object.code;
}
