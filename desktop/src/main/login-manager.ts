import { spawn as nodeSpawn, type ChildProcess, type SpawnOptions } from 'node:child_process';
import type { LoginStatus } from '../shared/contract';

export type LoginSpawn = (executable: string, args: string[], options: SpawnOptions) => ChildProcess;

export class LoginManager {
  private child: ChildProcess | undefined;
  private stopping = false;
  private forceTimer: NodeJS.Timeout | undefined;
  private current: LoginStatus = { state: 'unavailable' };
  private readonly listeners = new Set<(status: LoginStatus) => void>();

  constructor(
    private readonly executable: string,
    private readonly runtimeRoot: string,
    private readonly spawn: LoginSpawn = nodeSpawn,
    private readonly environment: NodeJS.ProcessEnv = process.env,
  ) {}

  status(): LoginStatus { return this.current; }

  onStatus(listener: (status: LoginStatus) => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  start(): LoginStatus {
    if (this.child) return this.current;
    let child: ChildProcess;
    try {
      child = this.spawn(this.executable, ['login'], {
        cwd: this.runtimeRoot,
        env: { ...this.environment },
        shell: false,
        stdio: 'ignore',
        windowsHide: true,
      });
    } catch {
      this.publish({ state: 'failed' });
      return this.current;
    }
    this.child = child;
    this.stopping = false;
    child.once('error', () => this.finish(child, { state: 'failed' }));
    child.once('exit', (code) => this.finish(child, { state: code === 0 ? 'succeeded' : 'failed' }));
    this.publish({ state: 'running' });
    return this.current;
  }

  cancel(): LoginStatus {
    const child = this.child;
    if (!child) return this.current;
    if (!this.stopping) {
      this.stopping = true;
      try { child.kill(); } catch { /* forced termination below remains authoritative */ }
      this.forceTimer = setTimeout(() => { if (this.child === child) { try { child.kill('SIGKILL'); } catch { /* process exit remains observable */ } } }, 1000);
      this.publish({ state: 'cancelled' });
      this.forceTimer.unref();
    }
    return this.current;
  }

  shutdown(): void {
    const child = this.child;
    this.cancel();
    if (child && this.child === child) { try { child.kill('SIGKILL'); } catch { /* app shutdown continues */ } }
  }

  private finish(child: ChildProcess, status: LoginStatus): void {
    if (this.child !== child) return;
    if (this.forceTimer) clearTimeout(this.forceTimer);
    this.forceTimer = undefined;
    this.child = undefined;
    const cancelled = this.stopping;
    this.stopping = false;
    if (!cancelled) this.publish(status);
  }

  private publish(status: LoginStatus): void {
    this.current = status;
    for (const listener of this.listeners) { try { listener(status); } catch { /* observers cannot control process ownership */ } }
  }
}
