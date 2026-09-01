// Types for private-runtime.js. They live apart from the implementation because the implementation
// has to stay loadable by a worker thread (see the header of private-runtime.js); this file is what
// keeps the rest of the app type-checked against it.
//
// The platform list is spelled here and in private-runtime.js, and nothing mechanical keeps the two
// in step: a platform added to one and not the other is caught by neither compiler nor test.
export type RuntimePlatform = 'darwin-arm64' | 'darwin-x64' | 'win32-x64';

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
