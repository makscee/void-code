import { chmodSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { resolvePrivateRuntime, resolvePrivateRuntimeAsync, sha256File, treeSha256 } from '../src/main/resources';

// Why this test exists, in the words of the person who hit it: the app "starts very slowly and
// shows no loading screen". The splash was then built, and it was still blank -- because there was
// nothing to draw it with. resolvePrivateRuntime reads and hashes 19,068 files of the Pi tree with
// readFileSync, and for the whole of that the main thread is inside one call: the browser process
// never parses the page and never produces a frame. Windows agrees in its own words -- it marks the
// window as hung (IsHungAppWindow true from 6.5s to 12.9s), and the title on the screenshot is the
// constructor's "Void Code" rather than the document's "Starting Void Code", because the document
// never got read. Measured on a warm Mac: 1336 ms of validation, during which a timer expected to
// fire 27 times fired 0.
//
// So the property under test is not "the splash appears". It is the thing that makes the splash
// possible at all: while the runtime is being resolved, the event loop still runs.
//
// This deliberately does not care HOW. A worker thread and an fs/promises rewrite both satisfy it;
// an `await` wrapped around the same synchronous call does not, and must not (see the report: that
// fake was run against this test and produced 0 ticks).

const roots: string[] = [];

// The real tree is 19,068 files. This one is 4,000, which measured at ~300 ms of validation on the
// machine this was written on -- small enough to build in a test, long enough that a blocking
// implementation cannot get a timer in edgewise.
const PI_FILES = 4_000;

function fixture(): string {
  const root = path.join(os.tmpdir(), `startup-event-loop-${crypto.randomUUID()}`); roots.push(root);
  mkdirSync(path.join(root, 'vc/bin'), { recursive: true }); mkdirSync(path.join(root, 'node/bin'), { recursive: true }); mkdirSync(path.join(root, 'pi'), { recursive: true }); mkdirSync(path.join(root, 'fixture'), { recursive: true });
  for (const file of ['vc/bin/vc', 'node/bin/node', 'pi/cli.js', 'fixture/test.js']) { writeFileSync(path.join(root, file), file); chmodSync(path.join(root, file), 0o755); }
  for (let index = 0; index < PI_FILES; index += 1) {
    const directory = path.join(root, 'pi', `d${index % 40}`);
    mkdirSync(directory, { recursive: true });
    writeFileSync(path.join(directory, `f${index}.js`), `module.exports = ${index};\n`.repeat(20));
  }
  const manifest = { schema: 1, platform: process.platform === 'win32' ? 'win32-x64' : 'darwin-arm64', build: { version: '0.2.50', describe: 'v0.2.50' }, vc: { version: 'v', sourceCommit: 'c', path: 'vc/bin/vc', sha256: sha256File(path.join(root, 'vc/bin/vc')) }, node: { version: 'v', path: 'node/bin/node', sha256: sha256File(path.join(root, 'node/bin/node')) }, pi: { version: 'v', entry: 'pi/cli.js', treeSha256: treeSha256(path.join(root, 'pi')) }, fixture: { path: 'fixture/test.js', sha256: sha256File(path.join(root, 'fixture/test.js')) } };
  writeFileSync(path.join(root, 'manifest.json'), JSON.stringify(manifest));
  return root;
}

afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

// A timer is the plainest stand-in for the thing that actually matters: if a 5 ms interval cannot
// fire, neither can Chromium's compositor.
//
// What is recorded is the longest silence, not the number of ticks. Counting ticks alone accepts an
// implementation that blocks solidly and then idles -- the ticks arrive late, but they arrive. The
// longest gap between consecutive ticks is the stall itself, which is the quantity the person is
// actually looking at when the window stops repainting.
async function loopDuring<T>(work: () => Promise<T>): Promise<{ value: T; ticks: number; longestStallMs: number }> {
  const marks: number[] = [Date.now()];
  let ticks = 0;
  const timer = setInterval(() => { ticks += 1; marks.push(Date.now()); }, 5);
  try {
    const value = await work();
    marks.push(Date.now());
    let longestStallMs = 0;
    for (let index = 1; index < marks.length; index += 1) longestStallMs = Math.max(longestStallMs, marks[index] - marks[index - 1]);
    return { value, ticks, longestStallMs };
  } finally { clearInterval(timer); }
}

describe('resolving the private runtime leaves the main thread free', () => {
  it('lets timers keep firing while the runtime is being validated, and returns what the blocking resolver returned', async () => {
    const root = fixture();

    const started = Date.now();
    const expected = resolvePrivateRuntime(root);
    const blockingMs = Date.now() - started;
    // Guard, not decoration: if the fixture ever becomes cheap enough to validate in a few
    // milliseconds, the tick count below stops proving anything and this test would pass while
    // measuring nothing. Then it says so instead.
    expect(blockingMs, `the fixture validates in ${blockingMs} ms -- too fast to tell a blocked event loop from a free one`).toBeGreaterThanOrEqual(50);

    const { value, ticks, longestStallMs } = await loopDuring(() => resolvePrivateRuntimeAsync(root));

    expect(ticks, `the event loop was blocked for the whole of runtime validation: a 5 ms timer fired ${ticks} times across ${blockingMs} ms of work`).toBeGreaterThanOrEqual(3);
    // Half the blocking cost is the line between "the loop kept running" and "the loop was held".
    // Generous on purpose -- a garbage-collection pause is allowed to be tens of milliseconds; what
    // is not allowed is a stall on the order of the validation itself.
    expect(longestStallMs, `the event loop stalled for ${longestStallMs} ms in one stretch, against ${blockingMs} ms of blocking validation -- the work is still being done on the main thread`).toBeLessThan(blockingMs / 2);
    expect(value).toEqual(expected);
  });
});
