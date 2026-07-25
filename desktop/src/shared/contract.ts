export const IPC = {
  start: 'terminal:start', input: 'terminal:input', resize: 'terminal:resize', stop: 'terminal:stop', status: 'terminal:status',
  chooseFolder: 'terminal:choose-folder', openLink: 'terminal:open-link', subscribe: 'terminal:subscribe', unsubscribe: 'terminal:unsubscribe',
  output: 'terminal:output', exit: 'terminal:exit', lifecycle: 'chat:lifecycle', lifecycleStatus: 'chat:lifecycle-status',
  workspaceLoad: 'workspace:load', workspaceChoose: 'workspace:choose', workspaceRemove: 'workspace:remove',
  workspaceNewChat: 'workspace:new-chat', workspaceSelect: 'workspace:select', workspaceClose: 'workspace:close', workspaceResume: 'workspace:resume',
} as const;

export type SessionId = string;
export type SubscriptionKind = 'output' | 'exit' | 'status';
export type SessionStatus = 'running' | 'stopped' | 'exited';
export type ChatLifecycleState = 'running' | 'working' | 'ready';
export interface ChatLifecycleEvent { version: 1; chatId: string; generation: number; sequence: number; state: 'Working' | 'Ready'; timestamp: string }
export interface ChatSemanticStatus { sessionId: SessionId; state: ChatLifecycleState; unread: boolean; diagnostic?: string }
export interface ChatStatusReply { sessionId: SessionId; status: ChatSemanticStatus }

export interface RealStartRequest { sessionId: SessionId; cwd: string; mode: 'create' | 'resume' }
export interface FixtureStartRequest { sessionId: SessionId; fixture: 'roundTrip' }
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

export interface TerminalApi {
  start(request: StartRequest): Promise<StartReply>;
  input(request: InputRequest): Promise<void>;
  resize(request: ResizeRequest): Promise<void>;
  stop(request: SessionRequest): Promise<void>;
  status(request: SessionRequest): Promise<StatusReply>;
  lifecycleStatus(request: SessionRequest): Promise<ChatStatusReply>;
  chooseFolder(): Promise<string | null>;
  openLink(url: string): Promise<void>;
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
export function startRequest(value: unknown): StartRequest {
  if (typeof value === 'object' && value !== null && 'fixture' in value) {
    const object = ownedObject(value, ['sessionId', 'fixture']);
    if (object.fixture !== 'roundTrip') throw new Error('only the owned roundTrip fixture is allowed');
    return { sessionId: sessionId(object.sessionId, true), fixture: object.fixture };
  }
  const object = ownedObject(value, ['sessionId', 'cwd', 'mode']);
  if (object.mode !== 'create' && object.mode !== 'resume') throw new Error('invalid session mode');
  if (typeof object.cwd !== 'string' || !object.cwd.startsWith('/') || Buffer.byteLength(object.cwd, 'utf8') > 4096) throw new Error('invalid cwd');
  return { sessionId: sessionId(object.sessionId), cwd: object.cwd, mode: object.mode };
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
