# Astra RED test plan (frozen API proposal)

Contract: pi-fullscreen-clipboard-spec.md. Tests only; no production stub. Pi 0.84.1's exported TuiAltScreen is instantiated with a memory Terminal, rendered with a real ScrollView, and fed SGR through Terminal.start's input callback. Private getSelectionBounds is inspected only as a version-bound acceptance observation, NOT advertised as supported extension API. Extraction oracle is an unmodified real Pi instance's OSC52 payload, never a duplicated slicing algorithm.

## Injectable API (exports from existing Go-embedded transport source)

- `installFullscreenClipboard(tui, options): () => void` — options `{piVersion: '0.84.1', platform, env, writeText(text, signal): Promise<void>, notify(message, level): void}`. Synchronous install/dispose; identical live TUI loading must not stack. Native completion causes the real TUI `flash('Copied!')`; failure is notified without payload. Unsupported shapes/versions fail visibly without throwing. Local VC authority comes from absolute `env.VC_BOOTSTRAP_EXECUTABLE`; SSH_CONNECTION/SSH_TTY exclude CLI, but VC_DESKTOP_CHAT_ID denotes desktop-local launch. Unsupported platform is passive.
- `createNativeClipboardWriter({platform, env, spawn}): (text, signal?) => Promise<void>` — spawn has node:child_process.spawn's signature; returns a Node ChildProcess-like object. Safe absolute executables, shell false, explicit stdin encoding (UTF-8 on both platforms; Mac static JXA/Foundation/AppKit via `/usr/bin/osascript` decodes stdin with NSUTF8StringEncoding and uses setStringForType with NSPasteboardTypeString; Windows script decodes UTF-8 explicitly, unchanged), 5000ms lifetime, kill and wait for close before settling/releasing serialization. The returned writer serializes its calls (including standalone consumers), rejects NUL / >8MiB UTF-8, and never interpolates text into executable/args/env/script. Spawn options use `stdio: ['pipe','ignore','pipe']`. Omitted spawn uses native spawn. Unit tests substitute spawn either in writer options or at the node:child_process module boundary; isolated R8 uses genuine native spawn. Queue capacity is eight waiting operations in addition to the active one. Errors generic and text-free.
- Default export still receives normal ExtensionAPI. Optional second argument `{clipboardIO: {platform, env, piVersion, writeText}}` substitutes only local IO for tests; default production IO must be used when omitted. Acquire actual TUI through documented non-invasive `ctx.ui.setWidget` factory on session_start; no editor replacement. Bootstrap execFileSync is mocked at module load, not the default registration. Providers must continue registering. Register session_shutdown cleanup. No account/network required.

Adapters may stay in the embedded source; moving to an embedded helper requires updating fixture extraction via normal delivery, not accepting a source-tree-only fix.

## Coverage

| Requirements | probes |
|---|---|
| R1 | forward/reverse Unicode multiline, ANSI+OSC8, wide-cell partial selection, scroll-content extraction, unmodified Pi differential oracle |
| R2 | deferred completion, rejection/throw, empty/whitespace, cancellation, real flash observation |
| R3 | snapshots and FIFO, 8 pending capacity and recovery, exact 8MiB UTF-8 / NUL validation at writer boundary |
| R4 | fake-time 4999/5000ms, kill then close, synchronous spawn failure and asynchronous ChildProcess ENOENT/EACCES error followed by close (without exit), text-free rejection, queued healthy successor, stdin/exit errors and cancellation |
| R5 | ordinary OSC52 while idle/pending, sink identity, payload-vs-command/environment, generic failure |
| R6 | full press/drag/release then Ctrl+C with idle/pending IO, exact text and real Input draft/interrupt focus untouched; typing/paste/Esc and terminal/menu/overlay focus loss retire stale authority; fresh gesture reacquires it; Kitty release versus press; bare Ctrl+C/Esc/arrows/text retain routing; existing desktop clipboard shortcut suite remains prerequisite |
| R7 | real default export CLI/desktop both with optional IO and with NO second argument (only node spawn substituted), native stdin/completion positive control, provider registration, passive contexts, unsupported version/shape, duplicate/dispose ownership |
| R8 | isolated-only actual consumer loader imports unchanged managed source; real default factory with no clipboardIO, synthetic bootstrap only, ordinary session_start/shutdown handlers and documented setWidget callback exposing real Pi TUI; real native defaults, four selections (literal RTF, literal EPS, ASTRA-A, ASTRA-B) with LC_ALL=C and LANG absent; independent AppKit declared plain-string type/UTF-8 base64 readback on Mac, unchanged WinForms readback on Windows; unbundled and actual bundled consumer |

Normal command: `cd desktop && npm test -- tests/pi-fullscreen-clipboard.test.ts tests/pi-native-clipboard.test.ts tests/pi-fullscreen-native-acceptance.test.ts`.
Existing managed reconciler and CLI/desktop session tests cover atomic transport delivery; no redundant Go tests. Existing windows-desktop-clipboard.test.ts covers xterm native Shift copy/paste and must remain green.

No fast-check dependency is installed; no dependency/network/package changes were made. Deterministic Unicode differential cases and adversarial event sequences are used instead of claiming generated-property coverage.

## Native acceptance launch (DO NOT run on shared/developer clipboard)

Only inside a dedicated disposable OS clipboard/login session explicitly owned by CI/test operator. This probe overwrites the clipboard four times and does not save/restore or inspect previous clipboard contents. It creates and removes its own isolated config directory under desktop/tests; never uses real HOME/settings/accounts. No GUI automation, SSH, model requests or live users. Run on each native platform, not under cross-compilation.

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

The bundled package directory is the real bundle's `PI_PACKAGE_DIR` (use assembly metadata; `package` above is illustrative), not an npm tree. The probe imports TuiAltScreen from the actual consumer's extension-loader virtual module. It never rebundles or patches the consumer. The same command supports an already assembled macOS bundle and Windows unbundled CLI by changing the two paths. Each invocation must print `ASTRA_NATIVE_SELECTION_READBACK_OK_4` from the child; four independent native reads must exactly equal all four complete selections, with final `ASTRA-B Другая 日本 🦊`. Mac readback checks declared NSPasteboardTypeString availability before stringForType, then explicitly converts UTF-8 data to base64; no pbpaste fallback or locale roundtrip. Failed reads are replaced by generic errors; equality is asserted as a boolean without printing private values. Parent process bounded to 55s (four selections); per-selection wait 6.5s; readback processes 5s. Missing gate skips before spawning or clipboard IO; missing explicit paths fails before IO. Acceptance requires these explicitly enabled CI invocations in isolated ephemeral OS login/clipboard sessions on both required platforms; never enable the gate in a shared developer login.

## Observed RED (A, before implementation)

A3 fixture hardening at `03ac3fe`: 49 targeted cases: **44 semantic failures, 4 fixture controls pass, 1 isolated native acceptance skipped**. Exact failure families:

- `R1: managed transport lacks semantic fullscreen native clipboard adapter: expected undefined to be type of 'function'`
- `R4/R5: managed transport lacks bounded stdin-only native writer: expected undefined to be type of 'function'`
- `R7: default managed extension never registers fullscreen clipboard lifecycle: expected false to be true`

Actual pinned Pi control renders text, receives SGR, returns scroll-view getSelectionBounds and emits exact Cyrillic/CJK/emoji OSC52; scrolled wide-cell forward/reverse control also passes. Third control confirms Pi 0.84.1 retains released selection after typing/paste/Esc/Ctrl+C, routes overlay Ctrl+C normally, and filters Kitty releases. Tests require retiring copy authority, not changing Pi's extraction or inventing editor key mappings.

A3 source witness: pinned `dist/modes/interactive/interactive-mode.js:1641+` disposes existing widgets in both placement maps before removal/replacement; `dist/index.js` exports `InteractiveMode`. Both R7 default-factory fixtures now retain components and dispose them with those semantics. A fourth passing fixture control proves replacement-before-factory ordering, placement transfer, and immediate probe removal uninstall a hook. Gated R8 uses the actual exported consumer method on a minimal receiver, including a positive disposal control; no full session is constructed. R8 was syntax-checked only, not executed. Tests are frozen for implementation handoff, uncommitted; production and read-only dependencies unchanged.

No production API additions in this follow-up. Fixture-only `extension(env, spawn?)` now permits substituting node spawn without passing clipboardIO; safeRequire allows genuine reads of the pinned package.json only and continues rejecting user filesystem writes/reads and network. Native bootstrap temporarily substitutes only execFileSync during the real managed factory, restores it before gestures/readback, and never substitutes native spawn or writer. Native acceptance remains gated and was NOT run. No import/fixture failure remains. Existing `windows-desktop-clipboard.test.ts` and `windows-desktop-clipboard-panel-regressions.test.ts`: **67 passed** (mocked IO only). Native probe TypeScript syntax checked with esbuild without executing it. Native acceptance is authored but NOT executed/verified on a real clipboard. No production change or commit. No coverage/mutation score claimed at RED; mutation verification is required after B.

## A4 final test-only freeze

A3 cases and native probe retained; injectable API unchanged. Source-verified pinned `dist/index.js:4` re-exports `VERSION` from `config.js`, where it is read from package metadata (`pkg.version`). The restricted agent fixture now exposes that genuine metadata value alongside `getPackageDir`; a passing control compares it with the actual config module export.

Added live-method assignment witnesses for R5: accessor setters reject and count assignments to the real memory terminal's `write` and real TUI's `flash`, including temporary assignment/restoration. Disposable inherited receivers remain permitted. A positive unmodified-Pi control proves guarded mouse extraction and actual flash work, and proves attempted live assignments throw. The contract case requires exact Unicode writer input, deferred success, inert ordinary OSC52 both idle and pending, and no assignment attempts through disposal.

Added R4 waiting cancellation: A is active, B is queued then aborted immediately (before queued microtasks/native listener setup), C follows; after A closes only C may spawn, B rejects generically without payload/abort reason, and C completes. Added a real isolated Node child at the spawn seam: consumes and verifies UTF-8 stdin, writes 4MiB stderr before its successful callback exit; only production can drain stderr. It has a 3.5s deadline, 5s test bound and SIGKILL/close cleanup. No clipboard command is launched. At RED this case stops at the missing writer export, so stderr behavior is authored but not yet verified against an implementation.

A4 command results: `cd desktop && npm test -- tests/pi-fullscreen-clipboard.test.ts tests/pi-native-clipboard.test.ts tests/pi-fullscreen-native-acceptance.test.ts` exits 1: **47 expected semantic failures, 6 fixture controls pass, 1 isolated native acceptance skipped (54 total)**. Failure families remain missing adapter, missing writer, missing default lifecycle; no fixture/import failures. Native probe `transformSync` syntax check (TS, ESM, node22) passes without execution; `git diff --check` passes. Native acceptance/clipboard IO was not executed. No production/workflow/dependency edits or commits. A4 tests frozen, uncommitted.

## A6 macOS Unicode/plain-type contract freeze (base `774e7e9`)

Measured local evidence: `MANPAGER=cat man pbcopy | col -b` states that pbcopy/pbpaste select encoding from locale (fallback C); pbcopy autodetects RTF/EPS headers; pbpaste falls back to other formats even with `-Prefer txt` and cannot request only one type. Thus reciprocal byte roundtrip is neither an OS Unicode oracle nor a plain-type witness. Architecture is now static JXA/Foundation/AppKit through absolute `/usr/bin/osascript`, explicit UTF-8 stdin decoding and NSPasteboardTypeString writing, using built-in system APIs, no optional npm module. Public writer/default/shape APIs and Windows writer assertions are unchanged.

Native process-plan cases cover C and missing locale settings, RTF/EPS literal payloads and Unicode, static argv/env across payloads, explicit decoder and plain-type setter. Earlier Mac executable expectations (including both no-clipboardIO default factories) now require osascript. R8 retains actual TuiAltScreen/ScrollView gestures, InteractiveMode widget lifecycle and unchanged managed/default factory with genuine native spawn. It selects RTF and multiline EPS before the original two Unicode markers, checks plain-type availability and exact full readback through independent AppKit, and reports count 4. No private readback is included in equality failures or propagated child errors.

Verification: `npm run lint` passes. Focused tests (`pi-fullscreen-clipboard`, `pi-native-clipboard`, `windows-desktop-clipboard`, `windows-desktop-clipboard-panel-regressions`) yield **5 expected semantic failures, 119 passed**: old Mac executable plan, two locale plans, CLI and desktop default-factory executable witnesses all receive pbcopy instead of osascript. No import/fixture failures; existing controls and Windows contracts pass. Native parent/probe TypeScript and embedded reader JavaScript syntax checks pass without execution; `git diff --check` passes. Native gate NOT executed, no actual clipboard accessed. Tests/spec/fixtures only, no production/workflow/dependency edits or commits. Frozen for coordinator CI/live-wrapper handoff; native correctness remains unverified until that authorized execution.

Mutation phase belongs after B's frozen implementation: remove native writing/change payload; early success; drop catch; parallelize; global OSC interception; omit default registration; remove NUL/size/timeout guards. No mutation score claimed before implementation.
