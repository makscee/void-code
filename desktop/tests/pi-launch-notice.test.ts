import { readFileSync } from 'node:fs';
import * as nodePath from 'node:path';
import * as nodeUrl from 'node:url';
import { transformSync } from 'esbuild';
import { describe, expect, it, vi } from 'vitest';

// The launch notice inside Pi — spec 2026-09-23-client-wallet-days, amendment "после панели
// void-code#76" §2. vc no longer prints the wallet notice before Pi starts: Pi's fullscreen mode
// clears the screen, so a line printed there is a line nobody reads. vc hands the notice to Pi as
// VC_LAUNCH_NOTICE (pinned in cmd/vc/wallet_client_test.go), and the managed Pi extension — the
// TypeScript embedded in cmd/vc/pi_extension.go — shows it on session_start through
// ctx.ui.notify(notice, "warning") when the session has a UI.
//
// Technique: the embedded source is extracted from the Go file and evaluated the way
// tests/fixtures/pi-fullscreen-clipboard.ts does it (same raw-string extraction, esbuild to CJS,
// an injected `process`), but with stub Pi packages instead of the pinned runtime, so this suite
// runs on a machine without `npm run setup`. What it proves: the extension, as shipped, reads
// VC_LAUNCH_NOTICE from its own process environment and turns it into one warning on
// session_start. What it cannot prove: that the real Pi 0.84.1 renders ctx.ui.notify visibly in
// fullscreen — that needs the pinned runtime and a terminal.

type Level = 'info' | 'warning' | 'error';
interface SessionContext {
  mode: string;
  hasUI: boolean;
  ui: {
    notify: (message: string, level?: Level) => void;
    setWidget: (...args: unknown[]) => void;
    setEditorComponent: (...args: unknown[]) => void;
  };
}
type Handler = (event: { reason: string }, ctx: SessionContext) => unknown;
interface FakePi {
  on(name: string, handler: Handler): void;
  registerProvider(...args: unknown[]): void;
}
type ExtensionModule = { default: (pi: FakePi, options?: { clipboardIO: Record<string, unknown> }) => unknown };

const LOW_NOTICE = 'Balance low — 1 day left. Message @makscee on Telegram to top up.';
const REFUSAL_NOTICE = 'Balance is not enough for today — message @makscee on Telegram to top up.';

function embeddedSource(): string {
  // Same Go raw-string extraction as tests/fixtures/pi-fullscreen-clipboard.ts and
  // scripts/check-bundled-pi-smoke.mjs.
  const go = readFileSync(nodePath.resolve('../cmd/vc/pi_extension.go'), 'utf8');
  const marker = 'const piVoidCodexExtensionSource = `';
  const start = go.indexOf(marker);
  const end = go.indexOf('`', start + marker.length);
  expect(start, 'managed transport source').toBeGreaterThanOrEqual(0);
  expect(end).toBeGreaterThan(start);
  return go.slice(start + marker.length, end);
}

const forbidden = (what: string) => (): never => { throw new Error(`launch-notice fixture forbids ${what}`); };

function loadExtension(env: Record<string, string>): ExtensionModule {
  const code = transformSync(embeddedSource(), { loader: 'ts', format: 'cjs', target: 'node22', logLevel: 'silent' }).code;
  const bootstrap = { version: 1, relayUrl: 'https://relay.invalid', authToken: 'fixture-only', providers: [{ kind: 'codex', relayProviderId: 'fixture', models: ['gpt-5.6-terra'] }] };
  const stubRequire = (id: string): unknown => {
    switch (id) {
      case 'node:child_process':
      case 'child_process':
        return { execFileSync: vi.fn(() => JSON.stringify(bootstrap)), spawn: forbidden('native clipboard IO') };
      case 'node:fs':
      case 'fs':
        return { existsSync: () => false, renameSync: forbidden('filesystem IO'), writeFileSync: forbidden('filesystem IO') };
      case 'node:path':
      case 'path':
        return nodePath;
      case 'node:url':
      case 'url':
        return nodeUrl;
      case '@earendil-works/pi-coding-agent':
        return { VERSION: '0.84.1', getPackageDir: () => '/nonexistent/pi' };
      case '@earendil-works/pi-ai':
        return { clampThinkingLevel: (_model: unknown, level: string) => level, createAssistantMessageEventStream: forbidden('streaming') };
      case '@earendil-works/pi-tui':
        return { isKeyRelease: () => false, matchesKey: () => false };
      default:
        throw new Error(`launch-notice fixture: unexpected import ${id}`);
    }
  };
  const module = { exports: {} };
  new Function('require', 'module', 'exports', 'process', 'console', 'fetch', code)(
    stubRequire, module, module.exports,
    { env, platform: 'darwin', pid: 999 },
    { error: vi.fn(), log: vi.fn(), warn: vi.fn() },
    forbidden('network'),
  );
  return module.exports as ExtensionModule;
}

// Loads the extension with `env` as Pi's process environment, starts one session the way Pi does
// (every session_start handler, in registration order) and returns what reached ctx.ui.notify.
async function startSession(env: Record<string, string>, ctx: { mode: string; hasUI: boolean }): Promise<Array<[string, Level | undefined]>> {
  const extension = loadExtension({ VC_BOOTSTRAP_EXECUTABLE: '/isolated/vc', ...env });
  const handlers = new Map<string, Handler[]>();
  const pi: FakePi = {
    on(name, handler) { handlers.set(name, [...(handlers.get(name) ?? []), handler]); },
    registerProvider: vi.fn(),
  };
  // Clipboard IO is injected so the clipboard lifecycle, which shares session_start, stays inert.
  extension.default(pi, { clipboardIO: { platform: 'darwin', env, piVersion: '0.84.1', writeText: vi.fn() } });
  const notify = vi.fn<(message: string, level?: Level) => void>();
  const session: SessionContext = { ...ctx, ui: { notify, setWidget: vi.fn(), setEditorComponent: vi.fn() } };
  for (const handler of handlers.get('session_start') ?? []) await handler({ reason: 'startup' }, session);
  return notify.mock.calls.map(([message, level]) => [message, level]);
}

describe('the managed Pi extension shows the launch notice vc handed it', () => {
  it.each([
    ['the low-balance notice', LOW_NOTICE],
    ['the refusal notice Relay will enforce', REFUSAL_NOTICE],
  ])('%s appears once, as a warning, when the session starts with a UI', async (_label, notice) => {
    const shown = await startSession({ VC_LAUNCH_NOTICE: notice }, { mode: 'tui', hasUI: true });
    expect(shown, 'VC_LAUNCH_NOTICE reached Pi, and Pi said nothing — the only place left to show the wallet notice').toEqual([[notice, 'warning']]);
  });

  it('says nothing when vc handed it no notice', async () => {
    expect(await startSession({}, { mode: 'tui', hasUI: true })).toEqual([]);
  });

  it('does not turn an empty notice into a blank warning', async () => {
    expect(await startSession({ VC_LAUNCH_NOTICE: '' }, { mode: 'tui', hasUI: true })).toEqual([]);
  });

  it('stays quiet in a session without a UI, and does not fail it', async () => {
    await expect(startSession({ VC_LAUNCH_NOTICE: LOW_NOTICE }, { mode: 'print', hasUI: false })).resolves.toEqual([]);
  });
});
