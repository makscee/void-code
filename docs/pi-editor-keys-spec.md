# VC Esc and prompt-history arrows (2026-09-10)

Approval: user asks «сделай ESC и стрелочки по сути это надо в pi прокидывать команды» after clarification that history means previous user inputs, not assistant answers. Basea08dbc3 retains the installed clipboard/scroll fixes. This is a separate behavior change, superseding the earlier clipboard spec's promise to leave these three keys unchanged **only in the scope below**.

## Contract

In a VC-launched Pi TUI main prompt editor, bare Up/Down select Pi's previous/next prompt-history entry, regardless of cursor position in a multiline draft. Pi owns history, draft snapshots, undo, cursor restoration, Unicode, and message storage; do not create another history implementation. Bare Esc runs Pi's native cancel behavior first; in an ordinary idle nonempty prompt where native Esc did not change the text, it clears the prompt through Pi's editor API. Never synthesize Ctrl+C or Enter. No session deletion, conversation navigation, inference, or process exit is introduced.

| ID | Requirement |
|---|---|
| K1 | Esc clears the complete idle draft, including multiline/Unicode/paste-expanded input; Pi's normal undo restores it. Clearing is deliberate user input, never an installation/startup side effect. Whitespace-only drafts also clear. |
| K2 | Keep Pi's cancellation priority: selectors/overlays/custom UI/autocomplete receive their own Esc/arrows; active agent/queued continuation preserves native abort/restore semantics without an extra clear. Native user-bash cancellation must still run before any idle clear. Temporary Pi onEscape overrides (retry/compaction/etc.) remain authoritative. Empty idle Esc retains Pi behavior, but never becomes Ctrl+C or quit. |
| K3 | Up/Down in the ordinary main editor use Pi's own previous/next history operation even in the middle of a multiline prompt. At the newest position, Pi restores the exact prior draft/cursor. Empty history and boundaries do not erase the draft or wrap around. History edits/undo follow Pi rather than a new algorithm. |
| K4 | Do not intercept arrows inside autocomplete, bracketed paste (including split chunks), character-jump mode, selector/custom-editor UI, or modifier shortcuts. Ignore Kitty release events; one press/repeat invokes at most one action. Preserve Left/Right, modified arrows, Enter, copy/paste and normal interrupt behavior. |
| K5 | Installed editor/history objects are retained. No editor replacement, global keybindings/keybindings.json rewrite, runtime package edit, prototype/global terminal interception or transport reimplementation. A narrow Pi0.84.1 version/shape-bound in-place input adapter delegating to native methods is allowed where no public action-dispatch API exists. Fail passive for unknown/custom editors and non-TUI/non-VC contexts. |
| K6 | Normal managed extension delivery reaches CLI and desktop/actual pi~BUN consumer. Reuse existing lifecycle/UI acquisition if practical. Restore only owned input hooks on disposal/reload; preserve later foreign changes; no duplicate handling. Clipboard selection authority must still retire on Esc/arrows/paste before subsequent Ctrl+C. |
| K7 | Prove key events against actual Pi Editor/CustomEditor, actual InteractiveMode key handlers and actual TUI reference, not a mock editor, direct setText assertion or source-string search. Add isolated actual interactive-CLI/PTY end-to-end evidence without user HOME, credentials, GUI, clipboard or inference. Qualify the actual bundled consumer as well; synthetic input only. |

## Implementation boundary

Keep delivery in `cmd/vc/pi_extension.go` (the transport extension actually passed by buildPiArgs/desktopSessionPlan). `pi_ui_extension.ts` is a legacy path and is not implicitly delivered. Prefer a small adapter on the existing native editor over new editor classes or global binding mutation. `CustomEditor` and `Editor` are Pi-owned; `navigateHistory` is private but present in pinned0.84.1, so guard it and pin compatibility rather than copy it. Pi `setText` already supports undo.

Important measured seams: widget factories receive `createInteractiveTuiReference`, whose getters bind methods; the `focusedComponent` itself is an object and is not method-bound. A raw TUI replacement in tests does not prove delivery. `ctx.isIdle()` tracks agent work, not an independently running `!bash`; delegate native Esc before the optional idle clear rather than stealing cancellation. Native completion/menu/paste handling must precede history navigation. Do not intercept a temporary native onEscape callback as if it were the idle handler.

## Explicit copy policy — owner update after trying the Mac desktop

User: «я чекнул по ESC не очищается инпут. Давай уберем авто копирование после выделения. Копирование по контрл ц на винде и команд ц на маке».

The running a08dbc3 desktop still lacks K1; its observed Esc behavior is the known RED, not a claimed regression in unshipped code. This update deliberately supersedes the earlier fullscreen-clipboard contract's mouse-release autocopy requirement:

| ID | Requirement |
|---|---|
| C1 | Selecting, dragging, double/triple selecting, releasing or auto-scrolling a Pi transcript selection MUST NOT write the native clipboard, emit live OSC52, or flash Copied. Keep the visible selection and Pi geometry unchanged. |
| C2 | Windows Ctrl+C copies a fresh Pi selection; no selection retains ordinary Pi interrupt behavior. Mac **desktop** Cmd+C copies a fresh Pi selection. Cmd+C with no selection must never clear input, interrupt, quit or submit. Keep the existing CLI Ctrl+C selection fallback for compatibility; Cmd+C intercepted by an external terminal cannot be promised to reach CLI Pi. |
| C3 | In the renderer, Mac Cmd+C with xterm-native selection uses the existing trusted Electron clipboard path once. Without xterm selection, route a copy-only key intent to Pi through the existing ordered input sink, never by substituting Ctrl+C. Pi's native key parser owns decoding (e.g. standard Kitty super+c); this must work under non-Latin keyboard layouts via physical KeyC. Do not steal copy from ordinary non-terminal input fields. |
| C4 | Preserve all previous snapshot/FIFO/bounds/native5s/kill-reap/plain-Unicode/error-redaction guards. Only an explicit copy gesture can start native writing. Mouse-only and ordinary model/tool output cannot acquire that authority. Repeated keydown and keyup must not duplicate frontend copy; separate intentional presses remain ordered. |
| C5 | Update independent existing clipboard acceptance to include explicit copy AFTER selection and assert no write before the key. Keep the four actual OS readbacks and actual-consumer/proxy provenance checks; do not hide Ctrl+C inside a helper still named drag or weaken native gates. Add a cross-seam test feeding the renderer's actual emitted Mac command into the real Pi consumer. |

No installed application/settings/GUI changes during implementation: the user is now using the Mac desktop. Prepare a new checked candidate before any separately announced update/restart. Keep a08dbc3 usable meanwhile.

## Verification/process

Independent A: tests and fixtures, RED committed. Independent B: implementation only. Coordinator verifies RED/GREEN, mutations (no clear, wrong history direction, skipping native cancellation/guards, duplicate handling, broken cleanup/clipboard coexistence). Test expectations come from Pi behavior and this contract, not B's source. Run repository tests, actual-consumer qualification, preflight (report RED/INCOMPLETE honestly), Sol/Terra review and independent Astra Pass2 before PR delivery. No merges/tags/releases/production services or real-user input in this task.
