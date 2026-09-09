# Astra RED test plan (frozen API proposal)

Contract: pi-fullscreen-clipboard-spec.md. Tests only; no production stub. Pi 0.84.1's exported TuiAltScreen is instantiated with a memory Terminal, rendered with a real ScrollView, and fed SGR through Terminal.start's input callback. Private getSelectionBounds is inspected only as a version-bound acceptance observation, NOT advertised as supported extension API. Extraction oracle is an unmodified real Pi instance's OSC52 payload, never a duplicated slicing algorithm.

## Injectable API (exports from existing Go-embedded transport source)

- `installFullscreenClipboard(tui, options): () => void` — options `{piVersion: '0.84.1', platform, env, writeText(text, signal): Promise<void>, notify(message, level): void}`. Synchronous install/dispose; identical live TUI loading must not stack. Native completion causes the real TUI `flash('Copied!')`; failure is notified without payload. Unsupported shapes/versions fail visibly without throwing. Local VC authority comes from absolute `env.VC_BOOTSTRAP_EXECUTABLE`; SSH_CONNECTION/SSH_TTY exclude CLI, but VC_DESKTOP_CHAT_ID denotes desktop-local launch. Unsupported platform is passive.
- `createNativeClipboardWriter({platform, env, spawn}): (text, signal?) => Promise<void>` — spawn has node:child_process.spawn's signature; returns a Node ChildProcess-like object. Safe absolute executables, shell false, explicit stdin encoding (UTF-8 on both platforms; Windows script decodes UTF-8 explicitly), 5000ms lifetime, kill and wait for close before settling/releasing serialization. The returned writer serializes its calls (including standalone consumers), rejects NUL / >8MiB UTF-8, and never interpolates text into executable/args/env/script. Spawn options use `stdio: ['pipe','ignore','pipe']`. Omitted spawn uses native spawn; tests always inject it. Queue capacity is eight waiting operations in addition to the active one. Errors generic and text-free.
- Default export still receives normal ExtensionAPI. Optional second argument `{clipboardIO: {platform, env, piVersion, writeText}}` substitutes only local IO for tests; default production IO must be used when omitted. Acquire actual TUI through documented non-invasive `ctx.ui.setWidget` factory on session_start; no editor replacement. Bootstrap execFileSync is mocked at module load, not the default registration. Providers must continue registering. Register session_shutdown cleanup. No account/network required.

Adapters may stay in the embedded source; moving to an embedded helper requires updating fixture extraction via normal delivery, not accepting a source-tree-only fix.

## Coverage

| Requirements | probes |
|---|---|
| R1 | forward/reverse Unicode multiline, ANSI+OSC8, wide-cell partial selection, scroll-content extraction, unmodified Pi differential oracle |
| R2 | deferred completion, rejection/throw, empty/whitespace, cancellation, real flash observation |
| R3 | snapshots and FIFO, 8 pending capacity and recovery, exact 8MiB UTF-8 / NUL validation at writer boundary |
| R4 | fake-time 4999/5000ms, kill then close, spawn/stdin/exit errors, cancellation and healthy successor |
| R5 | ordinary OSC52 while idle/pending, sink identity, payload-vs-command/environment, generic failure |
| R6 | selection Ctrl+C consumed, bare Ctrl+C/Esc/arrows/text routed to focus; existing desktop clipboard shortcut suite remains prerequisite |
| R7 | real default export CLI/desktop, provider positive control, passive contexts, unsupported version/shape, duplicate/dispose ownership |
| R8 | isolated-only native fixture, real Pi selection, two Unicode markers, independent pbpaste/WinForms readback; unbundled and actual bundled consumer |

Normal command: `cd desktop && npm test -- tests/pi-fullscreen-clipboard.test.ts tests/pi-native-clipboard.test.ts tests/pi-fullscreen-native-acceptance.test.ts`.
Existing managed reconciler and CLI/desktop session tests cover atomic transport delivery; no redundant Go tests. Existing windows-desktop-clipboard.test.ts covers xterm native Shift copy/paste and must remain green.

No fast-check dependency is installed; no dependency/network/package changes were made. Deterministic Unicode differential cases and adversarial event sequences are used instead of claiming generated-property coverage.

## Native acceptance launch (DO NOT run on shared/developer clipboard)

Only inside a dedicated disposable OS clipboard/login session explicitly owned by CI/test operator. This probe overwrites the clipboard twice and does not save/restore or inspect previous clipboard contents. It creates and removes its own isolated config directory under desktop/tests; never uses real HOME/settings/accounts. No GUI automation, SSH, model requests or live users. Run on each native platform, not under cross-compilation.

macOS unbundled, from `desktop`:

```sh
VC_ISOLATED_CLIPBOARD_ACCEPTANCE=I_OWN_THIS_ISOLATED_CLIPBOARD_SESSION \
VC_NATIVE_PI_ENTRY="$PWD/runtime/pi/node_modules/@earendil-works/pi-coding-agent/dist/cli.js" \
VC_NATIVE_PI_PACKAGE_DIR="$PWD/runtime/pi/node_modules/@earendil-works/pi-coding-agent" \
npm test -- tests/pi-fullscreen-native-acceptance.test.ts
```

Windows isolated PowerShell login session, from `desktop` (replace the two artifact paths with the actual assembled bundle paths):

```powershell
$env:VC_ISOLATED_CLIPBOARD_ACCEPTANCE='I_OWN_THIS_ISOLATED_CLIPBOARD_SESSION'
$env:VC_NATIVE_PI_ENTRY='C:\isolated-artifact\pi\pi~BUN.mjs'
$env:VC_NATIVE_PI_PACKAGE_DIR='C:\isolated-artifact\pi\package'
npm test -- tests/pi-fullscreen-native-acceptance.test.ts
```

The bundled package directory is the real bundle's `PI_PACKAGE_DIR` (use assembly metadata; `package` above is illustrative), not an npm tree. The probe imports TuiAltScreen from the actual consumer's extension-loader virtual module. It never rebundles or patches the consumer. The same command supports an already assembled macOS bundle and Windows unbundled CLI by changing the two paths. Each invocation must print `ASTRA_NATIVE_SELECTION_READBACK_OK_2` from the child; two independent native reads must equal two distinct Unicode markers. Parent process bounded to 25s; per-selection wait 6.5s; readback processes 5s. Missing gate skips before spawning or clipboard IO; missing explicit paths fails before IO. Do not set the gate in normal CI.

## Observed RED (A, before implementation)

34 targeted cases: **31 semantic failures, 2 fixture controls pass, 1 isolated native acceptance skipped**. Exact failure families:

- `R1: managed transport lacks semantic fullscreen native clipboard adapter: expected undefined to be type of 'function'`
- `R4/R5: managed transport lacks bounded stdin-only native writer: expected undefined to be type of 'function'`
- `R7: default managed extension never registers fullscreen clipboard lifecycle: expected false to be true`

Actual pinned Pi control renders text, receives SGR, returns scroll-view getSelectionBounds and emits exact Cyrillic/CJK/emoji OSC52; scrolled wide-cell forward/reverse control also passes. No import/fixture failure remains. Existing `windows-desktop-clipboard.test.ts` and `windows-desktop-clipboard-panel-regressions.test.ts`: **67 passed** (mocked IO only). Native probe TypeScript syntax checked with esbuild without executing it. Native acceptance is authored but NOT executed/verified on a real clipboard. No production change or commit. No coverage/mutation score claimed at RED; mutation verification is required after B.

Mutation phase belongs after B's frozen implementation: remove native writing/change payload; early success; drop catch; parallelize; global OSC interception; omit default registration; remove NUL/size/timeout guards. No mutation score claimed before implementation.
