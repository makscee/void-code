# Permanent fullscreen selection clipboard fix (2026-09-09)

Owner approval: «Давай делаем постоянный фикс». Work branch `work/pi-fullscreen-clipboard`, based on `5c08071` to retain the scrollbar fix already installed on Diana's laptop. No release/tag, production deployment, automatic application replacement, user-data migration or keyboard redesign in this task.

## Contract

In VC-launched local macOS and Windows Pi fullscreen sessions, a real nonempty transcript selection must copy exactly Pi's selected plain text into the local OS clipboard on mouse release; Ctrl+C with an active Pi selection must copy without clearing the draft or interrupting inference. `Copied!` must follow successful native writing, never mere OSC 52 emission, failure, timeout or cancellation. Arbitrary terminal/tool/model output must not gain clipboard-write authority. Pi remains responsible for selection geometry/Unicode/scrollview extraction, and existing xterm-native selection/paste, bare Ctrl+C, Esc, history, menus and scrolling remain unchanged. The implementation must be delivered by normal VC reconciliation in both CLI and desktop, including the actual `pi~BUN.mjs` consumer; not a hand edit in installed node_modules.

## Architecture boundary

Use the existing managed transport extension delivery (`cmd/vc/pi_extension.go`): it is reconciled/passed to both `runSpawn` and `desktopSessionPlan`. Keep the clipboard portion separately named internally and independent of provider networking. No extra global resource discovery or mutable settings are necessary. A source-tree build patch alone does NOT fix CLI: `install.sh:npm_install_managed_pi` installs its own managed npm tree independently of desktop resource assembly.

Pi 0.84.1 has no documented selection-copy callback. A small, explicitly version-bound compatibility adapter around its semantic fullscreen selection action is acceptable. It must reuse the actual Pi extraction rather than duplicate its slicing/ANSI/wide-character algorithm. Intercepting output is permitted ONLY inside the synchronous original semantic copy invocation on a disposable receiver/sink; never patch ProcessTerminal/stdout globally or register a generic OSC52 handler. Do not overwrite user runtime modules, replace the editor, or disable mouse reporting/fullscreen. Qualify the actual pinned class and bundled virtual module, not a lookalike. Fail visibly (without breaking provider startup) on unsupported Pi/internal shape rather than silently claiming clipboard integration.

Use safe local native writing with no optional package assumed present: absolute macOS system clipboard executable and absolute Windows system PowerShell/WinForms Unicode clipboard path are acceptable; clipboard text goes through stdin with explicit encoding, never command source/argv/environment/temp files/logs. Shell metacharacters must remain text. No clipboard read is needed in production. Test readback is independent. Native errors must not echo selected text. Remote SSH sessions and platforms other than local macOS/Windows retain existing behavior; desktop-minted sessions use the local desktop clipboard. `/copy` is a distinct existing path, not redesigned here.

## R1–R8 / acceptance and test plan (auto mode)

| ID | Priority | Observable contract |
|---|---|---|
| R1 | P0 regression | Drive actual Pi press/drag/release; selected Cyrillic/CJK/emoji/multiline text, not old sentinel, reaches writer; no native xterm selection is needed. |
| R2 | P0 | Success only after native completion; rejected/thrown/timed-out/cancelled writes have no success flash. Empty selections cause no write or success. Failure cannot guarantee undo of an OS write already committed: do not invent rollback. |
| R3 | P0 | Serialize admitted copies: slow A cannot overwrite newer B after B completes. Snapshot text at gesture time. Bound payload (8 MiB UTF-8 maximum) and queue (at most 8 pending); overload is a visible failure, not silent truncation. Reject embedded NUL, which Windows text clipboard cannot faithfully represent. |
| R4 | P0 | Native process has a 5-second lifetime bound, killed/reaped before a following write starts. Cleanup/cancellation does not leave late writers or success callbacks. No endless promise blocks subsequent healthy copy after child exit. |
| R5 | P0 security | Emitting OSC52/pseudo clipboard notices from ordinary output, including while a real copy is pending, never calls native writer. No global terminal sink interception. No payload in subprocess argv, command script, env, error text, telemetry or files. |
| R6 | P0 gestures | Ctrl+C with a fresh Pi selection is consumed and copies; no selection retains normal interrupt/clear. Ordinary input/paste/Esc or focus loss retires that selection's copy authority: a previous copy must not intercept Ctrl+C forever while the user edits or cancels work. Overlay/menu handling and key-release behavior remain intact. Esc/up/down/ordinary input keep their existing editor behavior. Successful native xterm Shift-selection copy and paste shortcuts are unchanged. |
| R7 | P0 delivery | Real default managed extension registers/installs the adapter for both VC launch modes; duplicate loading/reload does not stack writes, shutdown restores only owned hooks and cancels resources. Unsupported/non-VC/non-TUI contexts do not break providers or acquire clipboard authority. |
| R8 | P0 native | Mac unbundled and Windows actual bundle: synthetic real Pi selection -> actual OS clipboard -> independent readback of two distinct Unicode markers. Must run on isolated clipboard/CI login session, not overwrite a user's live clipboard. Mocks/notifications are not this proof. |

Additional controls: forward/reverse drags, styled/OSC8 text and scroll-view rather than screen-only selection; quotes/newlines/PowerShell syntax; native spawn/exit/stdin errors and timeout boundaries; actual pinned Pi package compatibility; loop-free bounded admission and reload ownership. Preserve Pi's current extraction semantics (including its trimming/wrapping), not a new normalization policy.

## Process

Independent A authors tests/fixtures only; coordinator verifies semantic RED and commits it separately. Independent B implements against committed tests without changing tests. Mutations against frozen B code in disposable checkout: omit native write/replace text; remove rejection handling; success before completion; bypass serial ordering; broaden to arbitrary OSC52; omit production adapter registration; remove size/NUL limit or native timeout. Any survivor goes back to A in a separate test commit. Full repository CI definition + actual consumer smoke, then Sol/Terra panel and independent Astra Pass-2. No claim of installed fix until a separately authorized deployment and live acceptance.
