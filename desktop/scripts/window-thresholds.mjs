// The floor that says "this is the application window", used by scripts/packaged-window-check.mjs.
//
// It was born to tell two windows apart: the app opened a separate 460x340 splash, the census names
// one window per process, and nothing but the coincidence of 460 < 500 kept the check measuring the
// right one. There is one window now (docs/superpowers/specs/2026-09-01-single-window-loading.md),
// so it no longer separates anything -- what it still does is reject a window nobody could work in,
// which is what the check waits for while the window is still being built. That is worth a name and
// this paragraph rather than a bare number twice in a 90-line script: the bare number is what let
// the splash size drift toward it unnoticed in the first place.
export const mainWindowMinimumEdge = 500;
