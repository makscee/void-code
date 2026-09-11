# Independent author A — K1–K5 RED plan

Scope: tests/fixtures only. No production edits, new dependencies, native app boot, Pi, filesystem workspace, clipboard or network.

- K1 P0: execute the actual AST-extracted createWindow factory from main/index.ts, with provenance assertion and real startup-lifecycle functions. Electron model starts with a default attached menu; visibility/auto-hide do not remove it. Observe menu at loadFile and show for loading and application pages on win32/linux; preserve darwin. Native Alt routing is NOT simulated or proven.
- K2/K5 P0: execute the entire renderer/index.ts via Vitest, real terminal-stack and clipboard wiring, replacing only DOM/native rendering and preload bridge. Cycle visible active order forward/back/wrap, exclude interleaved Recent; verify resume start/focus and untouched original synthetic draft.
- K3 P0: DOM capture/target/bubble dispatch with textarea ownership; adversarial target emits Tab through real xterm input/onData unless intercepted. Matching keyup (including Ctrl released first), exact modifiers/composition, ordinary controls and rename draft. This narrow target model is NOT Chromium/xterm keyboard-parser or native acceptance.
- K4 P0: explicitly deferred select and start boundaries, one operation in flight, rapid distinct/repeat progression; returned snapshot removes/moves tab before next operation; rejection notice once and queue recovery.
- K2 P1: zero/one active, missing workspace, recovery, missing selected forward first/backward last.
- K4 bounded policy: propose at most 8 pending directions, drop newest overflow (one in flight + 8 pending). Test checks this exact policy; coordinator may approve/change bound before implementation. No new helper API required: all behavior asserted through actual entry.
- Properties: forward/back inverse, cyclic wrap, active-only selection, uniqueness of dispatch, conservation of old draft; finite table cases (no fast-check installed; no install authorized).

Mutation targets for later GREEN qualification: hide vs remove, darwin removal, disconnected factory/entry, wrong direction/wrap/Recent, bubble-only interception, keyup leak, stale queued target, concurrent selects, ignored repeat, unbounded queue, swallowed failure. Baseline RED is not a mutation score. Native Windows/Linux Alt symptom remains unreproduced; hidden sendInputEvent would not close that gap. No native fixture launched in this suite.
