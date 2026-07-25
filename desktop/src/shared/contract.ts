export const IPC = {
  start: 'terminal:start',
  input: 'terminal:input',
  resize: 'terminal:resize',
  stop: 'terminal:stop',
  status: 'terminal:status',
  subscribe: 'terminal:subscribe',
  unsubscribe: 'terminal:unsubscribe',
  output: 'terminal:output',
  exit: 'terminal:exit',
} as const;

export type SessionId = string;
export type SubscriptionKind = 'output' | 'exit';
export type SessionStatus = 'running' | 'stopped' | 'exited';
export interface StartRequest { sessionId: SessionId; fixture: 'roundTrip' }
export interface InputRequest { sessionId: SessionId; data: string }
export interface ResizeRequest { sessionId: SessionId; cols: number; rows: number }
export interface SessionRequest { sessionId: SessionId }
export interface SubscribeRequest extends SessionRequest { kind: SubscriptionKind; subscriptionId: string }
export interface OutputEvent { sessionId: SessionId; data: string }
export interface ExitEvent { sessionId: SessionId; exitCode: number; signal?: number }
export interface StatusReply { sessionId: SessionId; status: SessionStatus }
export type Unsubscribe = () => void;

export interface TerminalApi {
  start(request: StartRequest): Promise<StatusReply>;
  input(request: InputRequest): Promise<void>;
  resize(request: ResizeRequest): Promise<void>;
  stop(request: SessionRequest): Promise<void>;
  status(request: SessionRequest): Promise<StatusReply>;
  onOutput(sessionId: SessionId, listener: (event: OutputEvent) => void): Unsubscribe;
  onExit(sessionId: SessionId, listener: (event: ExitEvent) => void): Unsubscribe;
  teardown(): void;
}

const SESSION_ID = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/;
const SUBSCRIPTION_ID = /^[a-f0-9-]{36}$/;
function ownedObject(value: unknown, keys: readonly string[]): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error('request must be an object');
  const object = value as Record<string, unknown>;
  const actual = Object.keys(object).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) throw new Error('request contains unknown or missing fields');
  return object;
}
export function sessionId(value: unknown): SessionId {
  if (typeof value !== 'string' || !SESSION_ID.test(value)) throw new Error('invalid sessionId');
  return value;
}
export function startRequest(value: unknown): StartRequest {
  const object = ownedObject(value, ['sessionId', 'fixture']);
  if (object.fixture !== 'roundTrip') throw new Error('only the owned roundTrip fixture is allowed');
  return { sessionId: sessionId(object.sessionId), fixture: object.fixture };
}
export function sessionRequest(value: unknown): SessionRequest {
  const object = ownedObject(value, ['sessionId']);
  return { sessionId: sessionId(object.sessionId) };
}
export function inputRequest(value: unknown): InputRequest {
  const object = ownedObject(value, ['sessionId', 'data']);
  if (typeof object.data !== 'string' || Buffer.byteLength(object.data, 'utf8') > 65_536) throw new Error('invalid input data');
  return { sessionId: sessionId(object.sessionId), data: object.data };
}
export function resizeRequest(value: unknown): ResizeRequest {
  const object = ownedObject(value, ['sessionId', 'cols', 'rows']);
  if (!Number.isInteger(object.cols) || !Number.isInteger(object.rows) || Number(object.cols) < 2 || Number(object.cols) > 1000 || Number(object.rows) < 1 || Number(object.rows) > 1000) throw new Error('invalid terminal size');
  return { sessionId: sessionId(object.sessionId), cols: Number(object.cols), rows: Number(object.rows) };
}
export function subscribeRequest(value: unknown): SubscribeRequest {
  const object = ownedObject(value, ['sessionId', 'kind', 'subscriptionId']);
  if (object.kind !== 'output' && object.kind !== 'exit') throw new Error('invalid subscription kind');
  if (typeof object.subscriptionId !== 'string' || !SUBSCRIPTION_ID.test(object.subscriptionId)) throw new Error('invalid subscriptionId');
  return { sessionId: sessionId(object.sessionId), kind: object.kind, subscriptionId: object.subscriptionId };
}
