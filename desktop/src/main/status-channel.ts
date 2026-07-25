import { randomUUID } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, rmSync, watch } from 'node:fs';
import type { FSWatcher } from 'node:fs';
import path from 'node:path';
import type { ChatLifecycleEvent, ChatSemanticStatus } from '../shared/contract';

export const STATUS_VERSION = 1;
export interface StatusWriteAuthority { path: string; chatId: string; generation: number }
interface LiveChannel {
  ownerId: number;
  chatId: string;
  generation: number;
  sequence: number;
  directory: string;
  file: string;
  watcher: FSWatcher;
  poller: NodeJS.Timeout;
  status: ChatSemanticStatus;
}
export type StatusListener = (ownerId: number, event: ChatSemanticStatus) => void;

export class StatusChannelStore {
  private readonly channels = new Map<string, LiveChannel>();
  private nextGeneration = 1;
  constructor(private readonly root: string, private readonly listener: StatusListener, private readonly isActive: (chatId: string) => boolean = () => true) {
    mkdirSync(root, { recursive: true, mode: 0o700 });
  }

  create(ownerId: number, chatId: string): StatusWriteAuthority {
    if (this.channels.has(chatId)) throw new Error('status channel already exists');
    const generation = this.nextGeneration++;
    const directory = path.join(this.root, `${chatId}-${randomUUID()}`);
    const file = path.join(directory, 'status.json');
    mkdirSync(directory, { mode: 0o700 });
    const channel = {
      ownerId, chatId, generation, sequence: 0, directory, file,
      watcher: undefined as unknown as FSWatcher,
      poller: undefined as unknown as NodeJS.Timeout,
      status: { sessionId: chatId, state: 'running', unread: false, diagnostic: 'status channel awaiting lifecycle' } as ChatSemanticStatus,
    };
    channel.watcher = watch(directory, (_event, filename) => { if (filename === path.basename(file)) this.read(channel); });
    channel.watcher.on('error', () => this.broken(channel, 'status channel unavailable'));
    channel.poller = setInterval(() => { if (existsSync(file)) this.read(channel); }, 50);
    channel.poller.unref();
    this.channels.set(chatId, channel);
    return { path: file, chatId, generation };
  }

  status(ownerId: number, chatId: string): ChatSemanticStatus {
    const channel = this.owned(ownerId, chatId);
    return { ...channel.status };
  }

  clearUnread(ownerId: number, chatId: string): ChatSemanticStatus {
    const channel = this.owned(ownerId, chatId);
    if (channel.status.unread) channel.status = { ...channel.status, unread: false };
    return { ...channel.status };
  }

  ingest(chatId: string, raw: unknown): boolean {
    const channel = this.channels.get(chatId);
    if (!channel) return false;
    const message = lifecycleEvent(raw);
    if (!message || message.chatId !== chatId || message.generation !== channel.generation || message.sequence <= channel.sequence) return false;
    channel.sequence = message.sequence;
    const state = message.state.toLowerCase() as 'working' | 'ready';
    channel.status = { sessionId: chatId, state, unread: state === 'ready' && !this.isActive(chatId) };
    this.listener(channel.ownerId, { ...channel.status });
    return true;
  }

  close(ownerId: number, chatId: string): void {
    const channel = this.channels.get(chatId);
    if (!channel || channel.ownerId !== ownerId) return;
    this.channels.delete(chatId);
    channel.watcher.close();
    clearInterval(channel.poller);
    rmSync(channel.directory, { recursive: true, force: true });
  }

  closeOwner(ownerId: number): void {
    for (const channel of [...this.channels.values()]) if (channel.ownerId === ownerId) this.close(ownerId, channel.chatId);
  }
  closeAll(): void {
    for (const channel of [...this.channels.values()]) this.close(channel.ownerId, channel.chatId);
    rmSync(this.root, { recursive: true, force: true });
  }

  private read(channel: LiveChannel): void {
    if (this.channels.get(channel.chatId) !== channel) return;
    try {
      const value: unknown = JSON.parse(readFileSync(channel.file, 'utf8'));
      if (!lifecycleEvent(value)) this.broken(channel, 'status channel schema rejected');
      else this.ingest(channel.chatId, value);
    } catch {
      this.broken(channel, 'status channel unreadable');
    }
  }
  private broken(channel: LiveChannel, diagnostic: string): void {
    if (this.channels.get(channel.chatId) !== channel) return;
    channel.status = { sessionId: channel.chatId, state: 'running', unread: false, diagnostic };
    this.listener(channel.ownerId, { ...channel.status });
  }
  private owned(ownerId: number, chatId: string): LiveChannel {
    const channel = this.channels.get(chatId);
    if (!channel || channel.ownerId !== ownerId) throw new Error('unknown status channel');
    return channel;
  }
}

export function lifecycleEvent(value: unknown): ChatLifecycleEvent | null {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return null;
  const object = value as Record<string, unknown>;
  const keys = Object.keys(object).sort();
  if (keys.join(',') !== 'chatId,generation,sequence,state,timestamp,version') return null;
  if (object.version !== STATUS_VERSION || typeof object.chatId !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(object.chatId)) return null;
  if (!Number.isSafeInteger(object.generation) || Number(object.generation) < 1 || !Number.isSafeInteger(object.sequence) || Number(object.sequence) < 1) return null;
  if (object.state !== 'Working' && object.state !== 'Ready') return null;
  if (typeof object.timestamp !== 'string' || !Number.isFinite(Date.parse(object.timestamp))) return null;
  return object as unknown as ChatLifecycleEvent;
}
