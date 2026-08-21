// The credential-error text vc prints is a terminal instruction ("run: vc login"). Handing
// it to a window would just move the "find someone with a terminal" wall into the UI, so any
// raw error text from vc is translated to one of these stable words before it leaves this module.
const KNOWN_STATUS_ERRORS: Record<string, string> = {
  'not logged in — run: vc login': 'not_authenticated',
};
const UNKNOWN_STATUS_ERROR = 'unknown_error';

export interface AuthProcessStream { on(event: 'data', listener: (chunk: Buffer | string) => void): unknown }
export interface AuthChildProcess {
  readonly stdout: AuthProcessStream;
  readonly stderr: AuthProcessStream;
  on(event: 'exit', listener: (code: number | null, signal: string | null) => void): unknown;
}
export type AuthSpawner = (vcPath: string, args: string[]) => AuthChildProcess;

export interface AuthStatus {
  authState: string;
  identity?: string;
  pct?: number;
  resetAt?: string;
  reason?: string;
}
export type StatusResult =
  | { ok: true; status: AuthStatus }
  | { ok: false; reason: 'exit_nonzero' | 'empty_output' | 'invalid_json' };

export type LoginEvent =
  | { event: 'prompt'; userCode: string; verificationUrl: string; expiresInSeconds: number }
  | { event: 'authorized' }
  | { event: 'error'; reason: string };
export type LoginResult = { ok: true } | { ok: false; reason: string };

export function readAuthStatus(vcPath: string, spawn: AuthSpawner): Promise<StatusResult> {
  return new Promise((resolve) => {
    const child = spawn(vcPath, ['status', '--json']);
    let output = '';
    child.stdout.on('data', (chunk) => { output += chunk.toString(); });
    child.on('exit', (code) => {
      if (code !== 0) { resolve({ ok: false, reason: 'exit_nonzero' }); return; }
      const text = output.trim();
      if (text.length === 0) { resolve({ ok: false, reason: 'empty_output' }); return; }
      let parsed: Record<string, unknown>;
      try {
        parsed = JSON.parse(text) as Record<string, unknown>;
      } catch {
        resolve({ ok: false, reason: 'invalid_json' });
        return;
      }
      const { error, ...rest } = parsed;
      const status = typeof error === 'string' ? { ...rest, reason: KNOWN_STATUS_ERRORS[error] ?? UNKNOWN_STATUS_ERROR } : rest;
      resolve({ ok: true, status: status as unknown as AuthStatus });
    });
  });
}

// A line-buffering splitter: the OS delivers stdout in arbitrary chunks, so a JSON line can
// arrive split across chunks (or several lines can arrive in one chunk). Buffering here — not
// parsing whatever a single 'data' event happens to contain — is what makes the parse chunk-invariant.
function lineSplitter(onLine: (line: string) => void): { push(chunk: string): void; flush(): void } {
  let buffer = '';
  return {
    push(chunk: string): void {
      buffer += chunk;
      const lines = buffer.split('\n');
      buffer = lines.pop() ?? '';
      for (const line of lines) onLine(line);
    },
    flush(): void {
      if (buffer.length > 0) onLine(buffer);
      buffer = '';
    },
  };
}

export function runLogin(
  vcPath: string,
  spawn: AuthSpawner,
  onEvent: (event: LoginEvent) => void,
  onOpenUrl: (url: string) => void,
  onDiagnostic?: (message: string) => void,
): Promise<LoginResult> {
  return new Promise((resolve) => {
    const child = spawn(vcPath, ['login', '--json']);
    let errorReason: string | undefined;

    const handleLine = (line: string): void => {
      if (line.trim().length === 0) return;
      let parsed: Record<string, unknown>;
      try {
        parsed = JSON.parse(line) as Record<string, unknown>;
      } catch {
        onDiagnostic?.(`malformed login output: ${line}`);
        return;
      }
      if (parsed.event === 'prompt') {
        const event = parsed as unknown as { event: 'prompt'; userCode: string; verificationUrl: string; expiresInSeconds: number };
        onEvent(event);
        onOpenUrl(event.verificationUrl);
      } else if (parsed.event === 'authorized') {
        onEvent({ event: 'authorized' });
      } else if (parsed.event === 'error') {
        const reason = String(parsed.reason);
        errorReason = reason;
        onEvent({ event: 'error', reason });
      } else {
        onDiagnostic?.(`malformed login output: ${line}`);
      }
    };
    const stdout = lineSplitter(handleLine);
    child.stdout.on('data', (chunk) => stdout.push(chunk.toString()));

    // stderr never reaches the event listener and never ends a login in progress — it is
    // purely diagnostic, since vc may emit warnings (e.g. pty size) unrelated to auth state.
    child.stderr.on('data', (chunk) => {
      const text = chunk.toString().trimEnd();
      if (text.length > 0) onDiagnostic?.(text);
    });

    child.on('exit', (code) => {
      stdout.flush();
      if (errorReason !== undefined) { resolve({ ok: false, reason: errorReason }); return; }
      if (code === 0) resolve({ ok: true });
      else resolve({ ok: false, reason: 'exited_unexpectedly' });
    });
  });
}
