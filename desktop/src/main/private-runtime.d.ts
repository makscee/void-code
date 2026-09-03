// Types for private-runtime.js. They live apart from the implementation because the implementation
// has to stay loadable by a worker thread (see the header of private-runtime.js); this file is what
// keeps the rest of the app type-checked against it.
//
// The platform list is spelled here and in private-runtime.js, and the compiler keeps only one of
// the two directions honest: a platform in the .js and missing here fails to compile, while one
// declared here and missing there compiles everywhere and refuses on the machine it names.
// tests/runtime-platform-list.test.ts compares the two as sets, which is what closes that side.
export type RuntimePlatform = 'darwin-arm64' | 'darwin-x64' | 'win32-x64';

// The array the running code reads, exported so the two spellings can be compared at runtime.
export const RUNTIME_PLATFORMS: readonly RuntimePlatform[];

export interface RuntimeManifest {
  schema: 1;
  platform: RuntimePlatform;
  build: { version: string; describe: string };
  vc: { version: string; sourceCommit: string; path: string; sha256: string };
  node: { version: string; path: string; sha256: string };
  pi: { version: string; entry: string; treeSha256: string };
  fixture: { path: string; sha256: string };
}
export interface PrivateRuntime { root: string; vc: string; node: string; piEntry: string; fixture: string; manifest: RuntimeManifest }

export function expectedRuntimePlatform(platform: string, arch: string): RuntimePlatform;
export function sha256File(file: string): string;
export function treeSha256(root: string): string;
export function resolvePrivateRuntime(root: string): PrivateRuntime;
