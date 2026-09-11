# Desktop Alt focus / Ctrl+Tab — 11.09.2026

User: «Почему то если нажимать alt потом нельзя писать в инпут в десктопном приложении. И давай сделаем переключение через ctrl+tab».

Base193eadb (working editor-key branch); deliberately NOT based on font candidate d3a57f1 which was rolled back. No font/runtime/auth changes. Platform of user's Alt symptom requested, not yet answered. Source main/index.ts leaves Electron default window menu attached; official Electron BrowserWindow docs describe Alt menu activation and distinguish hiding a menu from removing it. Native reproduction still required before claiming exact cause on user's machine.

## Contract

K1. On Windows/Linux, the desktop window must have no Alt-activatable application menubar, from before its first loading-page presentation onward. Hiding it temporarily is insufficient. Do not broadly swallow Alt, AltGr, Alt+letter or change Pi keys. Preserve macOS application menu/native editing accelerators. No forced refocus timer or key injection.
K2. Ctrl+Tab selects next open ACTIVE tab in visible order; Ctrl+Shift+Tab previous; wrap both ends. Mac also uses Control, NOT Cmd+Tab. Recent/archived tabs are never selected or resumed by cycling. Zero/one active tabs: no unnecessary select/resume/start. Missing selection: forward first/backward last; recovering/missing workspace inert.
K3. Reserve precisely Ctrl+Tab (optional Shift) before xterm handles it: no Tab/control bytes to Pi, no browser traversal. Both keydown and its matching keyup must be accounted for. Ordinary Tab/Shift+Tab, Alt/AltGr chords, Cmd+Tab, composing input and clipboard/Esc/history retain existing ownership. During inline title rename do not switch/drop/commit draft as a side effect of this new shortcut. Synthetic test input only; no inference.
K4. Rapid distinct/repeated keydowns advance coherently even while async workspace.select/resume is pending; compute subsequent target from current view when executed, including tabs removed/moved to Recent meanwhile. At most one navigation operation in flight. Rejection is caught and surfaced once without stranding the queue or switching to Recent. No speculative user-data writes or new sessions. Bounded/coalesced pending repeat policy acceptable if explicitly documented; no arbitrary unlimited key queue.
K5. Use normal selectChat/resume path, focus target terminal when usable, leave original terminal/editor draft untouched. One shortcut routes once through the actual renderer entry wiring, including when xterm textarea owns focus. No global shortcut registration or renderer-authority relaxation. Existing mouse selections/renames remain working.

## Evidence / limits

Independent Astra tests committed RED before separate Sol implementation. Test plan includes boundaries/negative ownership/async error/wiring, not just a pure index helper. Mutation checks: wrong direction/no wrap/include Recent, lost capture allowing PTY bytes, stale async index, swallowed rejection, mac menu removal, hide-instead-of-remove, disconnected main/renderer wiring. Actual Electron+xterm input probe with synthetic bridge where possible. A simulated win32 process or webContents.sendInputEvent is not physical Windows native-menu acceptance. Keep any native/lifecycle gap explicit, do not waive delivery gates.

No installation, live window restart, merge, release/tag or server deploy is authorized by this coding task. User unsent drafts/histories and existing working installs remain untouched.
