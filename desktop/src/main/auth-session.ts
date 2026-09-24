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

// The whole vocabulary vc is allowed to report, in one place: the type below and the shape check
// further down are both derived from it, so widening one without the other is not possible.
const AUTH_STATES = ['signed_in', 'signed_out', 'invalid_credential', 'access_not_granted'] as const;
export type AuthState = (typeof AUTH_STATES)[number];

// A refusal is answered *before* anyone is identified — the credential was fine, the account has no
// access — so vc never asked "who is this" and does not read the body it got back. Any identity,
// budget figure or reset date attached to a refusal therefore comes from the same service that just
// said no, and nobody vouched for it. That rule is enforced here, at the process boundary, and not
// left to the renderer: a future vc build (or a proxy that helpfully merges fields into the reply)
// must not be able to put an unverified account fact on a screen. Such a status carries exactly one
// fact out of this module: its state.
const REFUSAL_STATES: readonly AuthState[] = ['access_not_granted'];

// The wallet as `vc status --json` mirrors it from the server (spec 2026-09-23-client-wallet-days):
// dollars for display, the tariff that draws on them, and whether today's charge has been taken.
export interface WalletTariff {
  tier: string;
  monthlyPriceUsd: number;
  dailyRateUsd: number;
}
export interface Wallet {
  balanceUsd: number;
  tariff: WalletTariff | null;
  todayPaid: boolean | null;
  fundedDays: number | null;
}

export interface AuthStatus {
  authState: AuthState;
  identity?: string;
  reason?: string;
  wallet?: Wallet;
  // The wallet line exactly as vc formats it (`$18.00 · T1 · ~9 days left`, or a bare balance);
  // absent when there is no wallet. The display rules live in vc, once, and are never re-made here.
  walletText?: string;
  // What vc hands Pi about the wallet at launch (a low balance, a day Relay will refuse); absent
  // when there is nothing to say.
  launchNotice?: string;
}
export type StatusResult =
  | { ok: true; status: AuthStatus }
  | { ok: false; reason: 'exit_nonzero' | 'empty_output' | 'invalid_json' | 'invalid_status' };

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

// JSON.parse succeeding only proves vc printed *some* well-formed JSON — not that it printed a
// status. A schema regression in vc (renamed field, unrecognised authState) must fail loudly
// here rather than pass a garbage object through as ok:true, which reads to the UI exactly like
// signed_out.
// It stays a whitelist for exactly that reason: matching `typeof value.authState === 'string'`
// would make room for access_not_granted and silently reopen the hole in the same line.
function isAuthState(value: unknown): value is AuthState {
  return typeof value === 'string' && (AUTH_STATES as readonly string[]).includes(value);
}
function isValidStatusShape(value: unknown): value is { authState: AuthState } & Record<string, unknown> {
  return isPlainObject(value) && isAuthState(value.authState);
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value);
}
function isBooleanOrNull(value: unknown): value is boolean | null {
  return value === null || typeof value === 'boolean';
}
function isIntegerOrNull(value: unknown): value is number | null {
  return value === null || Number.isInteger(value);
}

// The same shape rules as vc's own parser (internal/auth/me.go parseWallet), all or nothing: one
// field of the wrong type drops the whole wallet, because a half-read wallet could show a wrong
// balance. Only the known fields are copied — a percentage riding along inside the wallet stops
// here. An absent tariff, todayPaid or fundedDays reads as null, as it does in vc.
function readWallet(value: unknown): Wallet | undefined {
  if (!isPlainObject(value) || !isFiniteNumber(value.balanceUsd)) return undefined;
  let tariff: WalletTariff | null = null;
  if (value.tariff !== undefined && value.tariff !== null) {
    const raw = value.tariff;
    if (!isPlainObject(raw) || typeof raw.tier !== 'string' || raw.tier === '' || !isFiniteNumber(raw.monthlyPriceUsd) || !isFiniteNumber(raw.dailyRateUsd)) return undefined;
    tariff = { tier: raw.tier, monthlyPriceUsd: raw.monthlyPriceUsd, dailyRateUsd: raw.dailyRateUsd };
  }
  const todayPaid = value.todayPaid ?? null;
  const fundedDays = value.fundedDays ?? null;
  if (!isBooleanOrNull(todayPaid) || !isIntegerOrNull(fundedDays)) return undefined;
  return { balanceUsd: value.balanceUsd, tariff, todayPaid, fundedDays };
}

export type LoginEvent =
  | { event: 'prompt'; userCode: string; verificationUrl: string; expiresInSeconds?: number }
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
      let parsed: unknown;
      try {
        parsed = JSON.parse(text);
      } catch {
        resolve({ ok: false, reason: 'invalid_json' });
        return;
      }
      if (!isValidStatusShape(parsed)) { resolve({ ok: false, reason: 'invalid_status' }); return; }
      const status: AuthStatus = { authState: parsed.authState };
      // See REFUSAL_STATES: the state word is the whole payload of a refusal. `reason` is dropped
      // with the rest — vc's sentence for this one names an operator the person has no way to
      // reach, and the fallback word 'unknown_error' would be worse than silence here: it reports
      // a fault where the system is working exactly as configured.
      if (!REFUSAL_STATES.includes(parsed.authState)) {
        // pct/resetAt are retired (spec 2026-09-23-client-wallet-days): the client shows money and
        // days, never a percentage, so an older vc that still prints them gets nothing past here.
        if (typeof parsed.identity === 'string') status.identity = parsed.identity;
        if (typeof parsed.error === 'string') status.reason = KNOWN_STATUS_ERRORS[parsed.error] ?? UNKNOWN_STATUS_ERROR;
        // The wallet, its line and the notice are account facts: they leave this module only with a
        // signed-in status, the one state in which vc heard the server vouch for them. Each is
        // checked on its own — a wallet this module cannot read does not take the line or the notice
        // with it.
        if (parsed.authState === 'signed_in') {
          const wallet = readWallet(parsed.wallet);
          if (wallet !== undefined) status.wallet = wallet;
          if (typeof parsed.walletText === 'string' && parsed.walletText !== '') status.walletText = parsed.walletText;
          if (typeof parsed.launchNotice === 'string' && parsed.launchNotice !== '') status.launchNotice = parsed.launchNotice;
        }
      }
      resolve({ ok: true, status });
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
      let parsed: unknown;
      try {
        parsed = JSON.parse(line);
      } catch {
        onDiagnostic?.(`malformed login output: ${line}`);
        return;
      }
      // Valid JSON is not the same guarantee as a known event: a plain object, a renamed
      // field, or an event word vc has never sent must fall through to diagnostics rather
      // than reach onEvent, where a window would branch on it as if it were real progress.
      if (!isPlainObject(parsed)) { onDiagnostic?.(`malformed login output: ${line}`); return; }
      // expiresInSeconds only drives the on-screen countdown — the shipped binary does not
      // always send it, and losing it must cost the countdown, not the whole prompt. userCode
      // and verificationUrl are what a person actually acts on, so those stay required.
      if (parsed.event === 'prompt' && typeof parsed.userCode === 'string' && typeof parsed.verificationUrl === 'string' && (parsed.expiresInSeconds === undefined || typeof parsed.expiresInSeconds === 'number')) {
        const event: LoginEvent = { event: 'prompt', userCode: parsed.userCode, verificationUrl: parsed.verificationUrl };
        if (typeof parsed.expiresInSeconds === 'number') event.expiresInSeconds = parsed.expiresInSeconds;
        onEvent(event);
        onOpenUrl(event.verificationUrl);
      } else if (parsed.event === 'authorized') {
        onEvent({ event: 'authorized' });
      } else if (parsed.event === 'error' && typeof parsed.reason === 'string') {
        errorReason = parsed.reason;
        onEvent({ event: 'error', reason: parsed.reason });
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
