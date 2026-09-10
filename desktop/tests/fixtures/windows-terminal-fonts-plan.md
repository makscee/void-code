# Astra independent RED plan (base ca6c0ca; installed reference 193eadb)

Only test/fixture changes. Proposed modest API in terminal-stack.ts:
`prepareTerminalFonts(): Promise<{status: 'loaded' | 'degraded'}>` reads this document's FontFaceSet, caches its settled choice, waits at most 5000ms, warns on degradation. createProductTerminal consumes that choice. Tests look up the export dynamically so absence is an assertion failure. No dependency-injection API required: globals/timers are injected by Vitest.

Behavior cases: all owned normal 400/700 subsets (Latin already ready, deferred Cyrillic/ext), unrelated hung/error faces ignored, absent API/faces/weight, rejection, timeout and late success/rejection, timer cleanup/cache, real xterm options on Windows/Mac/Linux. Startup coverage must execute index.ts, not merely find helper references. A compiled/extracted harness must preserve executable startup and call sites; the portable real-renderer fixture must never overwrite options or preload faces.

Implementation latitude: initialization may be gated before UI subscriptions (no top-level await required in CommonJS TS), or individual launches may await with lifecycle revalidation. No initialization API is prescribed. Readiness tests inject iterable FontFaceSet/FontFace.load and fake timers; normal owned faces are loaded directly, not via a default-sample load or document.fonts.ready. Italic and unrelated failed/hung faces must be ignored. Only prepareTerminalFonts is proposed as a new export.

The startup harness imports the entire actual index.ts through Vitest and uses the real Terminal constructor. It spies only native rendering boundaries (open/fit/activation/write/focus) and supplies inert DOM/bridge objects. F4 invalidates the synthetic bridge view while readiness is pending, then dispatches two real registered new-chat listeners. It is not an end-to-end workspace IPC or native DOM test. F5 evidence executes the AST-extracted actual integrationFacts declaration, with injected DOM measurement/check boundaries; keep that evidence function executable independently, or adapt this narrow harness if its dependencies change. No copied production function body or import-name-only wiring assertion.

Native qualification is separate, opt-in and must report actual platform/DPR (Mac is not Windows). Fixture writes synthetic regular/bold Ж/ж only; no PTY, Pi, clipboard, keyboard or user state. Native Windows DPR1.5 pixel qualification remains coordinator-owned.

## Commands and RED result

From the worktree, Node commands are run with the pinned runtime and explicit desktop cwd:

```sh
cd desktop
$HOME/.void-code/runtime/node/bin/node node_modules/vitest/vitest.mjs run tests/windows-terminal-fonts.test.ts tests/terminal-scrollbar-theme.test.ts tests/renderer-packaging.test.ts --no-cache --configLoader native
$HOME/.void-code/runtime/node/bin/node node_modules/eslint/bin/eslint.js tests/windows-terminal-fonts.test.ts tests/fixtures/windows-terminal-fonts-renderer.ts
```

RED: 14 new cases, 12 failed / 2 passed (Mac/Linux actual-option controls). Seven missing-readiness-export assertion failures; one Windows spacing 0 instead of 1; one false-positive Latin-only production evidence; two startup-before-readiness failures (ordinary/probe); one pending lifecycle early-open failure. No module import/syntax failures or unhandled rejections. Existing scrollbar/packaging controls: 6/6 passed. Combined 20 cases: 12 RED, 8 controls GREEN. Output: /tmp/vc-font-fix/astra-red.txt. Selected lint passed.

## Portable native fixture

Build without touching dist, node_modules or shared runtime caches:

```sh
mkdir -p /tmp/vc-font-fix/astra-renderer
cd desktop
$HOME/.void-code/runtime/node/bin/node node_modules/esbuild/bin/esbuild tests/fixtures/windows-terminal-fonts-renderer.ts --bundle --format=esm --platform=browser --target=chrome130 --loader:.woff2=file --loader:.woff=file '--asset-names=assets/[name]-[hash]' --outfile=/tmp/vc-font-fix/astra-renderer/windows-terminal-fonts-renderer.js
cp tests/fixtures/windows-terminal-fonts-renderer.html /tmp/vc-font-fix/astra-renderer/
```

Build verified. Load that HTML only in a coordinator-owned isolated Electron BrowserWindow, nodeIntegration false, contextIsolation true, no product preload/bridge. Use a unique temporary userData/session/cache directory. No GUI/native run was performed by Astra. The fixture calls candidate readiness then constructs/opens/fits/activates the actual product terminal, never assigns terminal options or loads fonts itself. Read window.windowsTerminalFontFixture.report after title windows-terminal-font-fixture-ready; absent API produces a clear fixture-failed title. Capture the isolated window separately for regular/bold Ж/ж pixel-edge measurements; report includes pre-open face statuses/options, real renderer activation, canvas count, xterm cell dimensions, buffer cells, actual platform and DPR. This helper-using fixture is NOT the startup-wiring test; the full-index Vitest cases provide that independent guard. No native-Windows claim follows from running this on Mac.
