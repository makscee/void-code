import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, readdirSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';
import pins from '../scripts/resource-pins.json';

// The desktop does not talk to a vc it merely trusts: it spawns one, with argv
// it writes itself, and reads what comes back on stdout. So the app and the
// binary it ships share a contract that is nowhere written down -- it exists
// only as an argv literal on one side and a cobra flag registration on the
// other. Nothing before this file compared the two.
//
// It cost a working sign-in. The v0.2.48 installer shipped vc v0.2.47 inside
// it, because scripts/assemble-windows-resources.mjs stages a vc.exe DOWNLOADED
// from a pinned past release while the app around it is built from this tree.
// `--json` on `vc login` landed in 89674b0 (#18) on 24.08; the pinned release
// was cut from 58497ca on 14.08. Every Windows user who pressed Sign in got
//
//     Error: unknown flag: --json
//
// and the app showed "Sign-in stopped unexpectedly". The macOS bundle never
// could have had this defect: assemble-resources.mjs runs `go build ./cmd/vc`
// over the very tree it is packaging.
//
// So the rule below is not "vc has a --json flag". It is: THE VC THE WINDOWS
// BUNDLE STAGES understands every argument the desktop spawns it with. Both
// halves are discovered, never typed -- the argv from src/main, the flags from
// the binary's own `--help` -- so a new subcommand, a new flag, or a moved
// spawn site is covered the day it lands.
//
// Where the shipped vc comes from is read off resource-pins.json, which is the
// disagreement itself: while a downloadable vc pin exists, the shipped CLI is
// that release's source; once it is gone, the shipped CLI is this tree. That is
// what makes this file red today and green when the Windows bundle builds vc
// from source.
//
// What this file cannot do: it cannot run a Windows binary, and it does not try.
// vc's flag surface is the same on every platform -- one cobra registration per
// flag, none of it behind a GOOS build tag -- so the surface is measured with a
// build for THIS machine from the same source. It also never runs a command for
// real: `--help` is the whole interaction, so nothing here reaches the network,
// and the child gets a throwaway HOME so nothing here reads or writes the real
// ~/.void-code.

const repo = execFileSync('git', ['rev-parse', '--show-toplevel'], { encoding: 'utf8' }).trim();
const scratch: string[] = [];
function temp(prefix: string) {
  const directory = mkdtempSync(path.join(os.tmpdir(), prefix));
  scratch.push(directory);
  return directory;
}

// ---------------------------------------------------------------------------
// Half one: the argv the desktop actually spawns.
//
// Read out of src/main rather than listed here. A list would be a second copy
// of the contract, and the copy nobody updates is the one that ships.
// ---------------------------------------------------------------------------

interface Invocation { readonly site: string; readonly command: string; readonly flags: readonly string[] }

// Only arrays handed to a vc spawn count, and only those shaped like argv: a
// subcommand followed by flags. `stdio: ['ignore', 'pipe', 'pipe']` lives in the
// same call and is not argv, so the shape is what excludes it -- not its
// position, which a reformat would move.
function argvLists(source: string): string[][] {
  const found: string[][] = [];
  const marker = /\bspawn\(\s*(?:vcPath|runtime\.vc)\s*,/g;
  for (let match = marker.exec(source); match !== null; match = marker.exec(source)) {
    const open = source.indexOf('(', match.index);
    let depth = 0;
    let end = open;
    for (; end < source.length; end += 1) {
      const character = source[end];
      if (character === '(' || character === '[' || character === '{') depth += 1;
      else if (character === ')' || character === ']' || character === '}') {
        depth -= 1;
        if (depth === 0) break;
      }
    }
    for (const array of source.slice(open, end + 1).matchAll(/\[([^[\]]*)\]/g)) {
      const items = [...array[1].matchAll(/'([^']*)'/g)].map((item) => item[1]);
      const [command, ...rest] = items;
      if (command === undefined || !/^[a-z][a-z0-9-]*$/.test(command)) continue;
      if (!rest.every((item) => item.startsWith('--'))) continue;
      if (!rest.some((item) => item.length > 2)) continue;
      found.push(items);
    }
  }
  return found;
}

describe('the argv reader', () => {
  // Applied to real files below, so it is first shown to tell argv from the
  // other arrays a spawn call carries. A reader that found nothing would make
  // every claim in this file vacuously true.
  const fixture = `
    const child = spawn(vcPath, ask ? ['access-request', '--ask', '--json'] : ['access-request', '--json']);
    const other = spawn(nodePath, ['whatever', '--json']);
    return spawn(runtime.vc, ['desktop-session', '--node', runtime.node, '--', ...lifecycle], {
      stdio: ['ignore', 'pipe', 'pipe'],
      env: { ...process.env },
    });
  `;

  it('reads both branches of a conditional argv', () => {
    expect(argvLists(fixture)).toContainEqual(['access-request', '--ask', '--json']);
    expect(argvLists(fixture)).toContainEqual(['access-request', '--json']);
  });

  it('reads an argv whose values are expressions, keeping the flags', () => {
    expect(argvLists(fixture)).toContainEqual(['desktop-session', '--node', '--']);
  });

  it('ignores the stdio array in the same call', () => {
    expect(argvLists(fixture).flat()).not.toContain('ignore');
  });

  it('ignores a spawn of something that is not vc', () => {
    expect(argvLists(fixture).map(([command]) => command)).not.toContain('whatever');
  });
});

const invocations: Invocation[] = readdirSync(path.join(repo, 'desktop/src/main'))
  .filter((entry) => entry.endsWith('.ts'))
  .flatMap((entry) => argvLists(readFileSync(path.join(repo, 'desktop/src/main', entry), 'utf8'))
    .map(([command, ...rest]) => ({ site: `src/main/${entry}`, command, flags: rest.filter((item) => item !== '--') })));

describe('what the desktop spawns vc with', () => {
  it('finds the sign-in invocation the Windows installer broke on', () => {
    // The scan going quiet is the failure mode that would make this whole file
    // pass while proving nothing, so the invocation that actually broke is
    // named once, here, and nowhere else.
    expect(invocations.map(({ command, flags }) => [command, ...flags].join(' ')))
      .toContain('login --json');
  });

  it('finds every vc subcommand the app drives', () => {
    expect([...new Set(invocations.map(({ command }) => command))].sort())
      .toEqual(['access-request', 'desktop-session', 'login', 'status']);
  });
});

// ---------------------------------------------------------------------------
// Half two: the flags the shipped vc has.
//
// Asked of a binary, not of the source. A registration this file could not
// parse -- one added in a loop, or by a helper -- would be a flag vc HAS and
// this file says it has not; `--help` is cobra's own answer and cannot disagree
// with the binary it came from.
// ---------------------------------------------------------------------------

// While resource-pins.json pins a downloadable vc for Windows, the CLI inside
// the installer is that release's, not this tree's -- the pin says so itself,
// in cliSourceCommit. Once the pin is gone the assembly builds from the tree,
// and the tree is the answer.
const windowsPin = (pins as { windows?: { vc?: { cliSourceCommit?: string; releaseTag?: string } } }).windows?.vc;

const shippedVcSource = windowsPin === undefined
  ? { origin: 'this tree', checkout: repo }
  : { origin: `the pinned release ${windowsPin.releaseTag} (cliSourceCommit ${windowsPin.cliSourceCommit})`, checkout: '' };

function checkoutOfShippedVc(): string {
  if (windowsPin === undefined) return repo;
  const commit = windowsPin.cliSourceCommit;
  if (commit === undefined) throw new Error('resource-pins.json pins a Windows vc but does not say which CLI revision it contains');
  const tree = temp('vc-shipped-source-');
  execFileSync('git', ['worktree', 'add', '--detach', tree, commit], { cwd: repo, stdio: 'pipe' });
  return tree;
}

function buildVc(checkout: string): string {
  const output = path.join(temp('vc-shipped-build-'), 'vc');
  try {
    execFileSync('go', ['build', '-trimpath', '-buildvcs=false', '-o', output, './cmd/vc'], {
      cwd: checkout,
      // GOPROXY=off keeps the build offline, as every test here must be. The
      // module cache already holds what this repository depends on.
      env: { ...process.env, CGO_ENABLED: '0', GOPROXY: 'off' },
      stdio: 'pipe',
    });
  } catch (error) {
    const failure = error as { stderr?: Buffer; stdout?: Buffer };
    throw new Error(`could not build vc from ${shippedVcSource.origin}: ${failure.stderr?.toString() ?? ''}${failure.stdout?.toString() ?? ''}`);
  }
  return output;
}

// One build, reused by every case: the flag surface does not change between
// them, and a build per case would be the slowest thing in the suite.
const shippedVc = buildVc(checkoutOfShippedVc());

// A throwaway HOME, because a test may not read or write the real ~/.void-code.
const home = temp('vc-shipped-home-');

// A subcommand the binary does not have is not an error to throw: it is the
// same defect one step further along, and it has to arrive as a sentence in the
// same list. `vc <unknown> --help` does not print help -- it falls through to
// the welcome gate, which tries to authenticate and exits non-zero -- so the
// two outcomes are told apart here rather than at the call site.
type Surface = { readonly known: true; readonly flags: readonly string[] } | { readonly known: false };

function surfaceOf(binary: string, command: string): Surface {
  let help: string;
  try {
    help = execFileSync(binary, [command, '--help'], {
      encoding: 'utf8',
      env: { ...process.env, HOME: home, USERPROFILE: home },
      timeout: 30_000,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
  } catch {
    return { known: false };
  }
  if (!new RegExp(`^Usage:\\s*\\n\\s*vc ${command}\\b`, 'm').test(help)) return { known: false };
  return { known: true, flags: [...help.matchAll(/(?:^|\s)(--[a-z][a-z0-9-]*)/g)].map((match) => match[1]) };
}

const flagsOf = (command: string) => surfaceOf(shippedVc, command);

afterAll(() => {
  for (const directory of scratch.splice(0)) {
    try { execFileSync('git', ['worktree', 'remove', '--force', directory], { cwd: repo, stdio: 'pipe' }); } catch { /* not a worktree */ }
    rmSync(directory, { recursive: true, force: true });
  }
});

describe(`the vc the Windows bundle stages -- built from ${shippedVcSource.origin}`, () => {
  it('has every subcommand the desktop spawns', () => {
    const missing = [...new Set(invocations.map(({ command }) => command))]
      .filter((command) => !flagsOf(command).known);
    expect(missing.join(', ') || 'every subcommand exists').toBe('every subcommand exists');
  });

  it('accepts every flag the desktop passes, so no spawn dies on `unknown flag`', () => {
    // Phrased as a list of "<site>: vc <command> <flag>" so the failure names
    // the spawn that would break and the flag the shipped binary has never
    // heard of -- which is the whole of the incident report.
    const unknown = invocations.flatMap(({ site, command, flags }) => {
      const surface = flagsOf(command);
      if (!surface.known) return [`${site}: vc ${command} — the shipped vc has no such subcommand`];
      const known = new Set(surface.flags);
      return flags.filter((flag) => !known.has(flag)).map((flag) => `${site}: vc ${command} ${flag}`);
    });
    expect(unknown.join('\n') || 'every flag is accepted').toBe('every flag is accepted');
  });
});

// ---------------------------------------------------------------------------
// Why the two halves must be one build.
//
// Below is the same measurement against THIS tree. It passes today: the flags
// are all here, in cmd/vc, on main. That is exactly the shape of the bug -- the
// source was right and the shipped binary was not -- so this case is here to
// localise a failure, not to prove the contract. When the case above is red and
// this one is green, the source is fine and the staging is wrong.
// ---------------------------------------------------------------------------

describe('this tree', () => {
  const treeVc = windowsPin === undefined ? shippedVc : buildVc(repo);

  it('carries every subcommand and flag the desktop passes', () => {
    const unknown = invocations.flatMap(({ command, flags }) => {
      const surface = surfaceOf(treeVc, command);
      if (!surface.known) return [`vc ${command} — this tree has no such subcommand`];
      const known = new Set(surface.flags);
      return flags.filter((flag) => !known.has(flag)).map((flag) => `vc ${command} ${flag}`);
    });
    expect(unknown.join('\n') || 'every flag is accepted').toBe('every flag is accepted');
  });
});
