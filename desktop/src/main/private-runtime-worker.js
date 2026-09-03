'use strict';
// Entry point of the runtime-validation worker. Plain JS on purpose, and at the same relative path
// in src/main and dist/main: node in a worker reads neither TypeScript nor a file that only exists
// after a build, and the suite imports src/main directly.
//
// It carries no logic of its own. The validation is the same synchronous call the main thread used
// to make; the only change is which thread it blocks. Failures cross back as a message rather than
// as a worker error, so the caller can rebuild the Error with the message the checks produced.
const { parentPort, workerData } = require('node:worker_threads');
const { resolvePrivateRuntime } = require('./private-runtime');

try {
  parentPort.postMessage({ runtime: resolvePrivateRuntime(workerData.root) });
} catch (error) {
  parentPort.postMessage({ error: error && error.message ? String(error.message) : String(error) });
}
