# Astra A2 explicit-copy RED — frozen

## Test plan (C1–C5)

1. Keep drag helpers mouse-only; assert selection/release has no native write, live OSC52 or Copied, then send an explicit copy key at the call site.
2. Preserve Pi extraction, Unicode, snapshot/FIFO/overload, cancellation, lifecycle/proxy ownership, native fallback and native-process guards. Adapt only clipboard setup in the existing K coexistence test.
3. Exercise Mac physical KeyC, native-xterm trusted write, copy-only ordered intent, repeat/release/modifier isolation and Windows/CLI interrupt compatibility. Feed actual renderer output into actual consumer reference/setupKeyHandlers plus real Pi CustomEditor/TUI.
4. Reuse the hidden Electron+xterm fixture for trusted DOM dispatch and ordinary input isolation; never use the host clipboard. Keep menu-accelerator acceptance separate.
5. Update gated R8: selection is silent, explicit platform key follows, all four independent OS readbacks remain mandatory. Do not run native acceptance on the user's session.

## Base/provenance

Worktree: `/Users/gever/GIT/maks-startup/.worktrees/pi-explicit-copy-tests`

HEAD: `c9f1f020068eef64838cc1ea316e89634dcc7b48`.
`git diff a08dbc3 HEAD -- cmd/vc/pi_extension.go desktop/src/renderer/clipboard-shortcuts.ts` is empty: tested production seams are unchanged a08.

Measured bundle: `/tmp/vc-diana-install-proxy/payload/resources/private-runtime/pi/agent/pi~BUN.mjs`
SHA256: `feee37a25fcd370b65391204ab8765a73f4b113e9239bb016bae03313dd5f403`.
Actual outer binding is `InteractiveMode = class _InteractiveMode`. Removed only the unsubstantiated optional `outerbinding_` prefix; retained extraMethods and constructor/factory provenance checks. Added an alias-rejection control.

## Results

All commands ran from the absolute worktree desktop directory with installed local tools; no dependency changes.

| Suite | PASS | RED | Gated/not selected |
|---|---:|---:|---:|
| pi-explicit-copy.test.ts | 6 | 12 | 0 |
| pi-fullscreen-clipboard.test.ts | 8 | 35 | 0 |
| pi-fullscreen-clipboard-proxy.test.ts | 15 | 11 | 0 |
| hidden trusted-copy Electron+xterm cases | 1 | 1 | 10 unrelated tests not selected |
| **Copy policy total** | **30** | **59** | |
| pi-editor-keys.test.ts, separately | 29 | **55 expected RED** | 2 PTY gated |
| native/private clipboard guard tests + windows-desktop-clipboard.test.ts | 81 | 0 | 0 |

New policy RED witnesses a08 mouse autocopy, absent Mac trusted-write/copy-only routing, and missing ordered Mac emission. Both CLI and actual-bundle AST cross-seams load successfully; failures are semantic, not the prior alias parser error. Hidden Windows remains PASS; hidden Mac returns trusted key events but fails the new copy behavior. On RED the renderer emits no intended Mac data, so downstream interoperability is specified and wired, not claimed GREEN.

Logs: `/tmp/astra-copy-red.{json,log}`, `/tmp/astra-hidden-copy-red.{json,log}`, `/tmp/astra-keyboard-red.{json,log}`, `/tmp/astra-copy-controls.{json,log}`.
Changed-file ESLint and `git diff --check`: PASS.

## Scope and remaining acceptance

- Only tests/fixtures changed. Drag implementation unchanged. Raw/native fallback after disposal and unsupported-context controls remain mouse-only.
- K semantics not rewritten; coexistence setup now explicitly sends Ctrl+C after selecting.
- R8 still has all four markers/readbacks, existing guard gating, native deadlines/process/provenance/UTF8 assertions. Added pre-key native-operation, OSC52 and success checks. **Actual OS R8 was not run.** Guard-suite PASS is not OS-readback proof.
- Hidden windows use `show: false`; no visible GUI or app activation calls. Mac fixture counts then cancels DOM copy defaults, including the ordinary-input control, to prevent OS clipboard access. Trusted bridge IO is injected memory-only.
- Platform application/menu accelerators and a real foreground Mac desktop session remain **manual GUI acceptance**, not proved by hidden webContents events. Actual bundle references/key handlers are extracted and executed, not a full bundled interactive process qualification.
- No production implementation, commits, release, network, user settings/data, dependency/install changes, or B-worktree access. Frozen uncommitted per explicit no-commits instruction; coordinator must capture a RED commit before B implementation.
