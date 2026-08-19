import type { ChildProcessWithoutNullStreams } from 'node:child_process';
import { spawn as nodeSpawn } from 'node:child_process';
import type { PrivateRuntime } from './resources';
import type { DesktopAuthEvent, DesktopAuthState } from '../shared/contract';

type Spawn = (file: string, args: string[], options: { windowsHide: boolean; shell: false; stdio: ['ignore', 'pipe', 'pipe'] }) => ChildProcessWithoutNullStreams;

function authEvent(line: string): DesktopAuthEvent {
  const value = JSON.parse(line) as Record<string, unknown>;
  const keys = Object.keys(value).sort();
  if (value.type === 'status' && (value.state === 'ready' || value.state === 'sign_in_required') && keys.join(',') === 'state,type') {
    return { type: 'status', state: value.state };
  }
  if (value.type === 'authorization' && typeof value.verificationUrl === 'string' && /^https:\/\//.test(value.verificationUrl) && typeof value.userCode === 'string' && /^[A-Z0-9-]{4,20}$/.test(value.userCode) && Number.isInteger(value.expiresIn) && Number(value.expiresIn) >= 30 && Number(value.expiresIn) <= 1800 && keys.join(',') === 'expiresIn,type,userCode,verificationUrl') {
    return { type: 'authorization', verificationUrl: value.verificationUrl, userCode: value.userCode, expiresIn: Number(value.expiresIn) };
  }
  if (value.type === 'complete' && value.state === 'ready' && keys.join(',') === 'state,type') return { type: 'complete', state: 'ready' };
  throw new Error('desktop auth protocol rejected');
}

export class DesktopAuthController {
  private active?: ChildProcessWithoutNullStreams;
  constructor(private readonly runtime: PrivateRuntime, private readonly spawn: Spawn = nodeSpawn as Spawn) {}

  status(): Promise<DesktopAuthState> {
    return new Promise((resolve, reject) => {
      const child = this.spawn(this.runtime.vc, ['desktop-auth', 'status'], { windowsHide: true, shell: false, stdio: ['ignore', 'pipe', 'pipe'] });
      let stdout = ''; let stderrBytes = 0;
      child.stdout.on('data', (chunk: Buffer) => { stdout += chunk.toString('utf8'); if (Buffer.byteLength(stdout) > 4096) child.kill(); });
      child.stderr.on('data', (chunk: Buffer) => { stderrBytes += chunk.length; if (stderrBytes > 4096) child.kill(); });
      child.once('error', () => reject(new Error('desktop auth unavailable')));
      child.once('close', (code) => {
        try {
          const lines = stdout.trim().split(/\r?\n/).filter(Boolean);
          const event = lines.length === 1 ? authEvent(lines[0]) : undefined;
          if (code !== 0 || event?.type !== 'status') throw new Error('desktop auth unavailable');
          resolve(event.state);
        } catch { reject(new Error('desktop auth unavailable')); }
      });
    });
  }

  start(deliver: (event: DesktopAuthEvent) => void): Promise<DesktopAuthState> {
    if (this.active) return Promise.reject(new Error('sign in already active'));
    return new Promise((resolve, reject) => {
      const child = this.spawn(this.runtime.vc, ['desktop-auth', 'start'], { windowsHide: true, shell: false, stdio: ['ignore', 'pipe', 'pipe'] });
      this.active = child;
      let buffered = ''; let stderrBytes = 0; let completed = false;
      const fail = () => { if (this.active === child) this.active = undefined; reject(new Error('sign in did not complete')); };
      child.stdout.on('data', (chunk: Buffer) => {
        buffered += chunk.toString('utf8');
        if (Buffer.byteLength(buffered) > 8192) { child.kill(); return; }
        const lines = buffered.split(/\r?\n/); buffered = lines.pop() ?? '';
        try {
          for (const line of lines.filter(Boolean)) {
            const event = authEvent(line); deliver(event);
            if (event.type === 'complete') completed = true;
          }
        } catch { child.kill(); }
      });
      child.stderr.on('data', (chunk: Buffer) => { stderrBytes += chunk.length; if (stderrBytes > 8192) child.kill(); });
      child.once('error', fail);
      child.once('close', (code) => {
        if (this.active === child) this.active = undefined;
        if (code === 0 && completed) resolve('ready'); else fail();
      });
    });
  }

  cancel(): void { this.active?.kill(); this.active = undefined; }
}

export { authEvent };
