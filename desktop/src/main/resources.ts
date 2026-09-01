import path from 'node:path';
import { Worker } from 'node:worker_threads';
import { expectedRuntimePlatform, resolvePrivateRuntime, sha256File, treeSha256 } from './private-runtime';
import type { PrivateRuntime } from './private-runtime';

export { expectedRuntimePlatform, resolvePrivateRuntime, sha256File, treeSha256 };
export type { PrivateRuntime, RuntimeManifest, RuntimePlatform } from './private-runtime';

// Where the worker entry is, in both trees the app runs from: next to this module in dist/main
// (put there by scripts/copy-static.mjs) and next to it in src/main under the suite.
//
// In a packaged app it must be the unpacked copy: a worker thread starts before Electron's asar
// support exists in it, so an entry addressed inside app.asar is a file the worker cannot read.
// package.json unpacks both this entry and the implementation it requires.
function workerEntry(): string {
  const entry = path.join(__dirname, 'private-runtime-worker.js');
  const packaged = `app.asar${path.sep}`;
  return entry.includes(packaged) ? entry.replace(packaged, `app.asar.unpacked${path.sep}`) : entry;
}

/**
 * The same validation as `resolvePrivateRuntime`, run somewhere it cannot stop the app from drawing.
 *
 * The checks are unchanged and still synchronous -- they are simply synchronous on another thread.
 * That distinction is the whole point: reading the Pi tree with `readFileSync` took the main thread
 * for the entire cold start (measured: 19,068 files, 1336 ms, a 5 ms timer fired 0 times), and for
 * all of it the browser process could not parse the splash page or paint a frame. Windows called the
 * window hung, which it was.
 *
 * Rewriting the checks on `fs/promises` would free the thread too, and would widen the gap between
 * the `lstat` before a read and the `lstat` after it -- the pair those checks are built on. A worker
 * keeps that gap exactly as wide as it was.
 */
export function resolvePrivateRuntimeAsync(root: string): Promise<PrivateRuntime> {
  return new Promise<PrivateRuntime>((resolve, reject) => {
    const worker = new Worker(workerEntry(), { workerData: { root } });
    let answered = false;
    worker.once('message', (message: { runtime?: PrivateRuntime; error?: string }) => {
      answered = true;
      if (message.error !== undefined) reject(new Error(message.error));
      else if (message.runtime !== undefined) resolve(message.runtime);
      else reject(new Error('runtime validation worker answered with neither a runtime nor an error'));
      void worker.terminate();
    });
    // A worker that dies without answering must not leave the caller waiting: startup would hang
    // behind a splash forever, which is worse than the failure it is hiding.
    worker.once('error', (error) => { answered = true; reject(error); });
    worker.once('exit', (code) => { if (!answered) reject(new Error(`runtime validation worker exited without validating (code ${code})`)); });
  });
}
