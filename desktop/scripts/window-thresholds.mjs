// The one number that tells this app's windows apart by size.
//
// `packaged-window-check.mjs` has no way to ask which window the census handed it: macOS names the
// first on-screen layer-0 window of the process, Windows names MainWindowHandle, and neither says
// "main" or "splash". It tells them apart by size — so the size of the startup splash and the floor
// the check measures against are one decision, not two numbers that happen not to collide today.
//
// It sits between the two windows the app opens: the main window is 1100x760
// (`splashWindowOptions` in src/main/splash-window.ts sizes the splash; `createWindow` in
// src/main/index.ts sizes the main one). A window this app opens must be plainly on one side or
// the other of this value; tests/splash-window.test.ts fails if the splash ever reaches it.
export const mainWindowMinimumEdge = 500;
