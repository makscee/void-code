import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { cp, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { assertPiSourcePins } from '../scripts/resource-assembly-lib.mjs';
import pins from '../scripts/resource-pins.json';

// The `desktop-windows-app` job died on a windows-latest runner with
//
//     Error: private Pi package.json hash mismatch
//         at assertPiSourcePins (desktop/scripts/resource-assembly-lib.mjs:124)
//
// on a checkout nobody had touched. scripts/resource-pins.json pins the bytes
// of files that live in this repository, and a checkout is not obliged to hand
// back the bytes that were committed: git converts line endings on the way out
// whenever `core.autocrlf` or `core.eol` says so, and the GitHub Windows runner
// image ships that conversion turned on. A pin taken over LF bytes then meets
// CRLF bytes, and the assembly refuses to build.
//
// So the rule is not about one file. Anything whose bytes this repository pins
// has to survive checkout unchanged on every platform -- and the set of such
// files is discovered here rather than listed, so the next pin someone adds is
// covered the day it lands.
//
// What this file cannot do: it cannot check out this repository on Windows.
// Nobody here has watched that happen. It models the checkout with git's own
// conversion pipeline (`git cat-file --filters`, which is what a checkout runs)
// under the configurations a Windows runner supplies. That is a faithful model
// of the step that failed, not a run of the job that failed.

const repo = execFileSync('git', ['rev-parse', '--show-toplevel'], { encoding: 'utf8' }).trim();
const sha256 = (bytes: Buffer) => createHash('sha256').update(bytes).digest('hex');

// Every 64-hex string anywhere in the pin file, whatever key holds it. Most of
// them name a download (the Node archives, vc.exe) and match nothing tracked;
// the ones that match a tracked file are the subject of this test.
function pinnedDigests(): Set<string> {
  const found = new Set<string>();
  const walk = (value: unknown) => {
    if (typeof value === 'string') { if (/^[a-f0-9]{64}$/.test(value)) found.add(value); return; }
    if (Array.isArray(value)) { for (const item of value) walk(item); return; }
    if (value && typeof value === 'object') { for (const item of Object.values(value as Record<string, unknown>)) walk(item); }
  };
  walk(pins);
  return found;
}

// The committed bytes of every tracked file, read out of the object store --
// never off disk. On a runner that has already converted the working tree, disk
// bytes would be the converted ones, and the discovery below would quietly find
// nothing and pass. The object store is the same on every platform.
function trackedBlobs(): { file: string; blob: string; bytes: Buffer }[] {
  const listed = execFileSync('git', ['ls-files', '-s', '-z'], { cwd: repo, maxBuffer: 64 * 1024 * 1024, encoding: 'utf8' })
    .split('\0')
    .filter(Boolean)
    .map((record) => {
      const [meta, file] = record.split('\t');
      return { file, blob: meta.split(' ')[1] };
    });

  const batch = execFileSync('git', ['cat-file', '--batch'], { cwd: repo, input: `${listed.map((entry) => entry.blob).join('\n')}\n`, maxBuffer: 512 * 1024 * 1024 });
  const contents: Buffer[] = [];
  let offset = 0;
  while (offset < batch.length) {
    const newline = batch.indexOf('\n', offset);
    const size = Number(batch.toString('utf8', offset, newline).split(' ')[2]);
    contents.push(batch.subarray(newline + 1, newline + 1 + size));
    offset = newline + 1 + size + 1;
  }
  expect(contents).toHaveLength(listed.length);
  return listed.map((entry, index) => ({ ...entry, bytes: contents[index] }));
}

const digests = pinnedDigests();
const pinnedFiles = trackedBlobs()
  .map((entry) => ({ ...entry, digest: sha256(entry.bytes) }))
  .filter((entry) => digests.has(entry.digest));

// git configurations a checkout can arrive with. `core.autocrlf=true` is the
// one the GitHub Windows runner image sets; `core.eol=crlf` is the other way to
// ask for the same conversion, and it catches a fix that marks these paths
// `text` without also fixing the ending (`text` alone still converts).
const hostileCheckouts = [
  { name: 'core.autocrlf=true', config: ['-c', 'core.autocrlf=true'] },
  { name: 'core.eol=crlf', config: ['-c', 'core.autocrlf=false', '-c', 'core.eol=crlf'] },
];

describe('files whose bytes are pinned survive checkout on every platform', () => {
  it('finds the pinned repository files by their pinned digest', () => {
    // Guards the discovery itself: were it to break, every assertion below
    // would iterate an empty list and report success.
    expect(pinnedFiles.map((entry) => entry.file).sort()).toEqual(
      expect.arrayContaining(['desktop/runtime/pi/package.json', 'desktop/runtime/pi/package-lock.json']),
    );
  });

  it('hands back the pinned bytes under a checkout that converts line endings', () => {
    const drift = pinnedFiles.flatMap((entry) =>
      hostileCheckouts.map((checkout) => {
        // `cat-file --filters` runs the conversion a checkout runs, for the
        // attributes and config in force. Verified against `checkout-index`:
        // both produce the same bytes for the same configuration.
        const delivered = sha256(execFileSync('git', [...checkout.config, 'cat-file', '--filters', `--path=${entry.file}`, entry.blob], { cwd: repo, maxBuffer: 512 * 1024 * 1024 }));
        return delivered === entry.digest ? '' : `${entry.file} under ${checkout.name}: checkout delivers ${delivered}, the pin holds ${entry.digest}`;
      }).filter(Boolean),
    );
    expect(drift).toEqual([]);
  });
});

const temporary: string[] = [];
afterEach(async () => { await Promise.all(temporary.splice(0).map((directory) => rm(directory, { recursive: true, force: true }))); });

// A copy of the pinned Pi source with one file rewritten the way a converting
// checkout rewrites it -- same text, CRLF endings, different bytes. This is the
// failure the runner hit, reproduced without a runner.
async function piSourceWithConvertedEndings(convert: 'package.json' | 'package-lock.json') {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'vc12-eol-'));
  temporary.push(directory);
  await cp(path.resolve('runtime/pi'), directory, { recursive: true, filter: (entry) => !entry.includes('node_modules') });
  const file = path.join(directory, convert);
  const converted = Buffer.from((await readFile(file, 'utf8')).replaceAll('\r\n', '\n').replaceAll('\n', '\r\n'), 'utf8');
  await writeFile(file, converted);
  return { directory, file, converted: sha256(converted) };
}

describe('a pin that fails says what the check saw', () => {
  it('names the file it hashed, the digest it got and the digest it wanted (package.json)', async () => {
    const { directory, file, converted } = await piSourceWithConvertedEndings('package.json');
    const error = await assertPiSourcePins(directory, pins.pi).then(() => undefined, (reason: Error) => reason);
    expect(error).toBeInstanceOf(Error);
    const message = String(error?.message);
    expect(message).toContain(file);
    expect(message).toContain(converted);
    expect(message).toContain(pins.pi.packageJsonSha256);
  });

  it('names the file it hashed, the digest it got and the digest it wanted (package-lock.json)', async () => {
    const { directory, file, converted } = await piSourceWithConvertedEndings('package-lock.json');
    const error = await assertPiSourcePins(directory, pins.pi).then(() => undefined, (reason: Error) => reason);
    expect(error).toBeInstanceOf(Error);
    const message = String(error?.message);
    expect(message).toContain(file);
    expect(message).toContain(converted);
    expect(message).toContain(pins.pi.packageLockSha256);
  });

  it('still refuses bytes that differ only in their line endings', async () => {
    // The repair belongs to the checkout, not to the check: a pin that forgave
    // a line-ending difference would forgive every other edit that survives
    // whatever normalisation was added to reach that forgiveness. Both files
    // are exercised, each while the other stays pinned.
    for (const convert of ['package.json', 'package-lock.json'] as const) {
      const { directory } = await piSourceWithConvertedEndings(convert);
      await expect(assertPiSourcePins(directory, pins.pi)).rejects.toThrow();
    }
  });
});
