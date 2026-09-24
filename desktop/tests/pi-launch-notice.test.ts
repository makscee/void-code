import { describe, expect, it, vi } from 'vitest';
import {
  CLIPBOARD_WIDGET_KEY, fakeUI, loadManagedExtension, renderWidget,
  type ExtensionContext, type ExtensionFactory, type FakePi, type FakeUI, type Handler,
} from './fixtures/managed-extension-stubbed';

// The launch notice inside Pi — spec 2026-09-23-client-wallet-days, amendment "после панели
// void-code#76" §2, as corrected by the second panel on #76. vc does not print the wallet notice
// before Pi starts (Pi's fullscreen mode clears the screen); it hands the notice to Pi as
// VC_LAUNCH_NOTICE (pinned in cmd/vc/wallet_client_test.go), and the managed Pi extension — the
// TypeScript embedded in cmd/vc/pi_extension.go — shows it.
//
// How it shows it is what this file pins, after two defects the panel confirmed in the first
// version, which called ctx.ui.notify(notice, "warning") on every session_start of a factory:
//
//  - G2: notify appends a warning to the scrolling transcript. A reopened desktop chat
//    (`--session <file>`) gets session_start, then Pi appends the whole history below the warning
//    (renderInitialMessages runs after the extensions bind), so the warning ends up off screen.
//    The notice is therefore DOCKED: ctx.ui.setWidget(<its own key>, …) above the editor, where
//    history cannot push it away. It stays until the person sends the first prompt — the first
//    before_agent_start takes it down — because by then it has been read, and a docked line about
//    money that never leaves is noise.
//
//  - G1: Pi runs the extension factory again for every new runtime — /new, /resume, fork, /reload —
//    evaluating the module afresh (its loader imports with moduleCache: false), and
//    VC_LAUNCH_NOTICE is still in process.env, so a `shown` flag inside the factory showed the
//    launch's notice again, stale, in every later session. The notice belongs to the launch: it is
//    shown only on session_start with reason "startup", and only when the session has a UI.
//
// Technique: the embedded source is evaluated against stub Pi packages
// (tests/fixtures/managed-extension-stubbed.ts); ctx.ui.setWidget is modelled as Pi's widget map,
// and a docked widget is rendered to the lines a person reads. What this cannot prove: that the
// real Pi 0.84.1 draws the widget above its editor in fullscreen — that needs the pinned runtime
// and a terminal.

const LOW_NOTICE = 'Balance low — 1 day left. Message @makscee on Telegram to top up.';
const REFUSAL_NOTICE = 'Balance is not enough for today — message @makscee on Telegram to top up.';

// One Pi runtime: the factory run against its own handler table, the way Pi's loader runs it for
// each runtime it creates. The clipboard lifecycle shares session_start; its IO is injected so it
// stays inert.
interface Runtime { emit(name: string, event: Record<string, unknown>, ctx: ExtensionContext): Promise<void> }
function runFactory(factory: ExtensionFactory, env: Record<string, string>): Runtime {
  const handlers = new Map<string, Handler[]>();
  const pi: FakePi = {
    on(name, handler) { handlers.set(name, [...(handlers.get(name) ?? []), handler]); },
    registerProvider: vi.fn(),
  };
  factory(pi, { clipboardIO: { platform: 'darwin', env, piVersion: '0.84.1', writeText: vi.fn() } });
  return {
    // Every handler for the event, in registration order, with the same context — as Pi's runner does.
    async emit(name, event, ctx) {
      for (const handler of handlers.get(name) ?? []) await handler({ type: name, ...event }, ctx);
    },
  };
}
function startRuntime(env: Record<string, string>): Runtime {
  return runFactory(loadManagedExtension({ env }), env);
}

const tui = (ui: FakeUI): ExtensionContext => ({ mode: 'tui', hasUI: true, ui });
const promptEvent = { prompt: 'hello', systemPrompt: 'base prompt', systemPromptOptions: {} };

// What is docked around the editor besides the clipboard's own render-nothing widget.
function noticeWidgets(ui: FakeUI) {
  return [...ui.docked].filter(([key]) => key !== CLIPBOARD_WIDGET_KEY);
}
function dockedText(ui: FakeUI): string {
  return noticeWidgets(ui).flatMap(([, widget]) => renderWidget(widget.content)).join(' ').replace(/\s+/g, ' ').trim();
}
function notifiedAbout(ui: FakeUI, notice: string): string[] {
  return ui.notified.map(([message]) => message).filter((message) => message.includes(notice));
}

describe('the launch notice is docked above the editor when Pi starts', () => {
  it.each([
    ['the low-balance notice', LOW_NOTICE],
    ['the refusal notice Relay will enforce', REFUSAL_NOTICE],
  ])('%s is one widget above the editor, reading the notice, and nothing in the transcript', async (_label, notice) => {
    const ui = fakeUI();
    await startRuntime({ VC_LAUNCH_NOTICE: notice }).emit('session_start', { reason: 'startup' }, tui(ui));

    const widgets = noticeWidgets(ui);
    expect(widgets, 'VC_LAUNCH_NOTICE reached Pi and nothing was docked — the only place left to show the wallet notice').toHaveLength(1);
    const [, widget] = widgets[0];
    expect(widget.options?.placement ?? 'aboveEditor', 'the notice is docked below the editor, not above it').toBe('aboveEditor');
    const text = dockedText(ui);
    expect(text, 'the docked widget does not read as the notice vc handed over').toContain(notice);
    expect(text.split(notice).length - 1, `the notice is drawn more than once: ${text}`).toBe(1);
    // G2: a warning in the transcript scrolls away under a reopened chat's history.
    expect(notifiedAbout(ui, notice), 'the notice went into the scrolling transcript (ctx.ui.notify)').toEqual([]);
  });

  it('keeps its own widget key — the clipboard widget that shares session_start stays docked', async () => {
    const ui = fakeUI();
    await startRuntime({ VC_LAUNCH_NOTICE: LOW_NOTICE }).emit('session_start', { reason: 'startup' }, tui(ui));
    expect(ui.docked.has(CLIPBOARD_WIDGET_KEY), 'the notice replaced the fullscreen clipboard widget').toBe(true);
    expect(dockedText(ui)).toContain(LOW_NOTICE);
  });

  it('docks nothing when vc handed it no notice', async () => {
    const ui = fakeUI();
    await startRuntime({}).emit('session_start', { reason: 'startup' }, tui(ui));
    expect(noticeWidgets(ui)).toEqual([]);
    expect(ui.notified).toEqual([]);
  });

  it('does not turn an empty notice into a blank widget', async () => {
    const ui = fakeUI();
    await startRuntime({ VC_LAUNCH_NOTICE: '' }).emit('session_start', { reason: 'startup' }, tui(ui));
    expect(noticeWidgets(ui)).toEqual([]);
    expect(ui.notified).toEqual([]);
  });

  it('stays quiet in a session without a UI, and does not fail it', async () => {
    const ui = fakeUI();
    const runtime = startRuntime({ VC_LAUNCH_NOTICE: LOW_NOTICE });
    await expect(runtime.emit('session_start', { reason: 'startup' }, { mode: 'print', hasUI: false, ui })).resolves.toBeUndefined();
    await expect(runtime.emit('before_agent_start', promptEvent, { mode: 'print', hasUI: false, ui })).resolves.toBeUndefined();
    expect(noticeWidgets(ui)).toEqual([]);
    expect(ui.notified).toEqual([]);
  });
});

describe('the notice leaves with the first prompt', () => {
  it('the first before_agent_start takes the widget down', async () => {
    const ui = fakeUI();
    const runtime = startRuntime({ VC_LAUNCH_NOTICE: REFUSAL_NOTICE });
    await runtime.emit('session_start', { reason: 'startup' }, tui(ui));
    expect(dockedText(ui), 'precondition: the notice is docked after startup').toContain(REFUSAL_NOTICE);

    await runtime.emit('before_agent_start', promptEvent, tui(ui));
    expect(noticeWidgets(ui), 'the notice is still docked after the person sent a prompt').toEqual([]);
    expect(ui.docked.has(CLIPBOARD_WIDGET_KEY), 'taking the notice down took the clipboard widget with it').toBe(true);
  });

  it('and does not come back on later prompts', async () => {
    const ui = fakeUI();
    const runtime = startRuntime({ VC_LAUNCH_NOTICE: LOW_NOTICE });
    await runtime.emit('session_start', { reason: 'startup' }, tui(ui));
    expect(dockedText(ui), 'precondition: the notice is docked after startup').toContain(LOW_NOTICE);
    await runtime.emit('before_agent_start', promptEvent, tui(ui));
    await runtime.emit('agent_end', {}, tui(ui));
    await runtime.emit('before_agent_start', { ...promptEvent, prompt: 'and again' }, tui(ui));
    expect(noticeWidgets(ui)).toEqual([]);
    expect(notifiedAbout(ui, LOW_NOTICE)).toEqual([]);
  });
});

describe('a later runtime in the same Pi process shows nothing (G1)', () => {
  // Pi's side of a runtime switch, from interactive-mode.js: the old runtime gets session_shutdown,
  // resetExtensionUI() clears every widget, the factory runs again — against a freshly evaluated
  // module (moduleCache: false), with the same process environment — and the new runtime gets
  // session_start with the switch's reason. "The same module" is checked too, so the rule cannot
  // lean on how Pi happens to load extensions today.
  it.each([
    ['new', 'a freshly evaluated module'],
    ['resume', 'a freshly evaluated module'],
    ['fork', 'a freshly evaluated module'],
    ['reload', 'a freshly evaluated module'],
    ['new', 'the same module'],
    ['resume', 'the same module'],
    ['fork', 'the same module'],
    ['reload', 'the same module'],
  ])('session_start with reason "%s", factory from %s: nothing docked, nothing in the transcript', async (reason, source) => {
    const env = { VC_LAUNCH_NOTICE: LOW_NOTICE };
    const ui = fakeUI();
    const factory = loadManagedExtension({ env });
    const first = runFactory(factory, env);
    await first.emit('session_start', { reason: 'startup' }, tui(ui));
    // Only that the launch said it somehow — how it says it is the first describe's business, and
    // this test is about the second runtime.
    const shownAtLaunch = [dockedText(ui), ...ui.notified.map(([message]) => message)].join(' ');
    expect(shownAtLaunch, 'precondition: the launch itself shows the notice').toContain(LOW_NOTICE);

    await first.emit('session_shutdown', { reason }, tui(ui));
    ui.docked.clear(); // resetExtensionUI → clearExtensionWidgets
    ui.notified.length = 0;

    const second = runFactory(source === 'the same module' ? factory : loadManagedExtension({ env }), env);
    await second.emit('session_start', { reason }, tui(ui));
    expect(noticeWidgets(ui), `a ${reason} session re-showed the launch's notice, about a wallet that may have changed since`).toEqual([]);
    expect(notifiedAbout(ui, LOW_NOTICE), `a ${reason} session put the launch's notice in the transcript`).toEqual([]);
  });

  it.each(['new', 'resume', 'fork', 'reload'])('a runtime whose first session_start is "%s" never shows it', async (reason) => {
    const ui = fakeUI();
    await startRuntime({ VC_LAUNCH_NOTICE: REFUSAL_NOTICE }).emit('session_start', { reason }, tui(ui));
    expect(noticeWidgets(ui)).toEqual([]);
    expect(notifiedAbout(ui, REFUSAL_NOTICE)).toEqual([]);
  });
});
