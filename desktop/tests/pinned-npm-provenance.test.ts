import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import pins from '../scripts/resource-pins.json';

// The reconstructed Pi tree is hashed against a pin, and `npm ci` produces a
// different tree under a different npm. So the npm that restores it must come
// from the pinned Node distribution the assembly itself stages -- never from
// whatever happens to sit in the developer's or the runner's PATH.
//
// This file states that rule as a resolution question ("which npm, and which
// node under it, would actually run?") rather than as a search for a literal.
// Several different implementations satisfy it: invoking the staged node with
// npm's cli.js, or invoking the staged npm with PATH pointing at the staged
// bin directory first. All of them pass here; a bare `npm` inheriting the
// ambient PATH, and an absolute path to a machine-local npm, do not.

type Verdict = { readonly pinned: boolean; readonly reason: string };

// A path expression rooted at the assembly's own staging directory, inside the
// Node distribution it just unpacked: path.join(staging, 'node/...') and its
// spellings. Anything else -- a bare command name, an absolute machine path --
// is not something the assembly controls.
const stagedNodeTree = /(?:path\.join\(\s*staging\s*,\s*['"]node(?:['"/\\])|\$\{staging\}[/\\]node)/;
const machineAbsolute = /['"`](?:\/(?:usr|opt|home|Users|var|snap)\/|[A-Za-z]:\\)/;
const bareNpm = /^['"`]npm(?:\.cmd)?['"`]$/;

function callArguments(source: string, openParen: number) {
  let depth = 0;
  let quote = '';
  for (let index = openParen; index < source.length; index += 1) {
    const character = source[index];
    if (quote) {
      if (character === '\\') index += 1;
      else if (character === quote) quote = '';
      continue;
    }
    if (character === "'" || character === '"' || character === '`') { quote = character; continue; }
    if ('([{'.includes(character)) depth += 1;
    else if (')]}'.includes(character)) {
      depth -= 1;
      if (depth === 0) return source.slice(openParen + 1, index);
    }
  }
  throw new Error('unbalanced call expression');
}

function topLevelParts(text: string) {
  const parts: string[] = [];
  let depth = 0;
  let quote = '';
  let start = 0;
  for (let index = 0; index < text.length; index += 1) {
    const character = text[index];
    if (quote) {
      if (character === '\\') index += 1;
      else if (character === quote) quote = '';
      continue;
    }
    if (character === "'" || character === '"' || character === '`') { quote = character; continue; }
    if ('([{'.includes(character)) depth += 1;
    else if (')]}'.includes(character)) depth -= 1;
    else if (character === ',' && depth === 0) { parts.push(text.slice(start, index)); start = index + 1; }
  }
  parts.push(text.slice(start));
  return parts.map((part) => part.trim()).filter((part) => part.length > 0);
}

// `execFileSync(stagedNode, ...)` says as much as the inlined expression does,
// so a binding is followed to its definition before the expression is judged.
function inlineBinding(source: string, expression: string) {
  let current = expression.trim();
  for (let hop = 0; hop < 5 && /^[A-Za-z_$][\w$]*$/.test(current); hop += 1) {
    const definition = new RegExp(`\\b(?:const|let|var)\\s+${current}\\s*=\\s*([^\\n]+?);?\\s*$`, 'm').exec(source);
    if (!definition) break;
    current = definition[1].trim();
  }
  return current;
}

function pathOption(source: string, options: string) {
  const entry = /\bPATH\s*:/i.exec(options);
  if (!entry) return '';
  const value = topLevelParts(options.slice(entry.index + entry[0].length))[0] ?? '';
  return inlineBinding(source, value.replace(/\}\s*$/, '').trim());
}

// PATH decides only if the staged bin directory wins the lookup: it has to
// appear before the inherited PATH and before any machine-local directory.
function stagedNodeWinsPath(source: string, options: string) {
  const value = pathOption(source, options);
  const staged = value.search(stagedNodeTree);
  if (staged < 0) return false;
  return [/process\.env\.(?:PATH|Path)/, machineAbsolute]
    .map((rival) => value.search(rival))
    .every((position) => position < 0 || position > staged);
}

function npmProvenance(source: string): Verdict {
  const invocations: string[] = [];
  for (const spawn of source.matchAll(/\b(?:execFileSync|execFile|spawnSync|spawn)\s*\(/g)) {
    const open = spawn.index + spawn[0].length - 1;
    const text = callArguments(source, open);
    const parts = topLevelParts(text);
    const argv = parts[1] ?? '';
    if (/^\[/.test(argv) && topLevelParts(argv.slice(1, -1)).some((item) => /^['"`]ci['"`]$/.test(item))) invocations.push(text);
  }
  if (invocations.length !== 1) return { pinned: false, reason: `expected exactly one 'npm ci' invocation, found ${invocations.length}` };

  const parts = topLevelParts(invocations[0]);
  const command = inlineBinding(source, parts[0] ?? '');
  const firstArgument = inlineBinding(source, topLevelParts((parts[1] ?? '').slice(1, -1))[0] ?? '');
  const options = parts[2] ?? '';

  if (machineAbsolute.test(command)) return { pinned: false, reason: `npm is a hardcoded machine path: ${command}` };

  const commandStaged = stagedNodeTree.test(command);
  const argumentStaged = stagedNodeTree.test(firstArgument);
  const pathStaged = stagedNodeWinsPath(source, options);

  // The staged node running staged npm-cli.js: both halves pinned outright.
  if (commandStaged && argumentStaged) return { pinned: true, reason: 'staged node runs the staged npm cli' };
  // The staged npm shim: its `#!/usr/bin/env node` still resolves through PATH,
  // so it counts only when PATH puts the staged bin directory first.
  if (commandStaged && pathStaged) return { pinned: true, reason: 'staged npm with the staged bin directory first in PATH' };
  if (bareNpm.test(command) && pathStaged) return { pinned: true, reason: 'PATH resolves npm inside the staged Node distribution' };
  if (bareNpm.test(command)) return { pinned: false, reason: `npm is resolved from the ambient PATH: ${command}` };
  return { pinned: false, reason: `npm does not resolve inside the staged Node distribution: ${command}` };
}

const macAssembly = readFileSync(new URL('../scripts/assemble-resources.mjs', import.meta.url), 'utf8');
const windowsAssembly = readFileSync(new URL('../scripts/assemble-windows-resources.mjs', import.meta.url), 'utf8');

const preamble = `
const staging = '/tmp/staging';
const nodePath = path.join(staging, 'node/bin/node');
const stagedNpmCli = path.join(staging, 'node/lib/node_modules/npm/bin/npm-cli.js');
const stagedNpm = path.join(staging, 'node/bin/npm');
`;

describe('npm provenance rule', () => {
  // The rule is applied to real files below, so it is first shown to
  // distinguish the shapes it claims to distinguish -- otherwise a rule that
  // always says "no" would look like a passing test suite.
  const accepted: ReadonlyArray<readonly [string, string]> = [
    ['staged node running the staged npm cli', "execFileSync(nodePath, [stagedNpmCli, 'ci', '--offline'], { cwd: pi, env: process.env });"],
    ['inlined staged paths', "execFileSync(path.join(staging, 'node/bin/node'), [path.join(staging, 'node/lib/node_modules/npm/bin/npm-cli.js'), 'ci'], { cwd: pi });"],
    ['bare npm with the staged bin directory first in PATH', "execFileSync('npm', ['ci', '--offline'], { cwd: pi, env: { ...process.env, PATH: `${path.join(staging, 'node/bin')}:${process.env.PATH}` } });"],
    ['bare npm with a joined staged PATH', "execFileSync('npm', ['ci'], { cwd: pi, env: { ...process.env, PATH: [path.join(staging, 'node/bin'), '/usr/bin', '/bin'].join(path.delimiter) } });"],
    ['staged npm shim with the staged bin directory first in PATH', "execFileSync(stagedNpm, ['ci'], { cwd: pi, env: { ...process.env, PATH: `${path.join(staging, 'node/bin')}:/usr/bin:/bin` } });"],
  ];
  const rejected: ReadonlyArray<readonly [string, string, string]> = [
    ['bare npm inheriting the ambient PATH', "execFileSync('npm', ['ci', '--offline'], { cwd: pi, env: process.env });", 'ambient PATH'],
    ['bare npm with an untouched PATH in a rebuilt env', "execFileSync('npm', ['ci'], { cwd: pi, env: { ...process.env, npm_config_cache: cache } });", 'ambient PATH'],
    ['a hardcoded machine npm', "execFileSync('/opt/homebrew/bin/npm', ['ci'], { cwd: pi, env: process.env });", 'hardcoded machine path'],
    ['the staged bin directory appended after the ambient PATH', "execFileSync('npm', ['ci'], { cwd: pi, env: { ...process.env, PATH: `${process.env.PATH}:${path.join(staging, 'node/bin')}` } });", 'ambient PATH'],
    ['the staged npm shim left to find node through the ambient PATH', "execFileSync(stagedNpm, ['ci'], { cwd: pi, env: process.env });", 'does not resolve'],
  ];

  it.each(accepted)('accepts %s', (_name, invocation) => {
    expect(npmProvenance(preamble + invocation).pinned).toBe(true);
  });

  it.each(rejected)('rejects %s', (_name, invocation, reason) => {
    const verdict = npmProvenance(preamble + invocation);
    expect(verdict.pinned).toBe(false);
    expect(verdict.reason).toContain(reason);
  });
});

describe('resource assembly restores Pi with the pinned npm', () => {
  it('reconstructs the macOS Pi tree with npm from the staged Node distribution', () => {
    // Phrased as a string so the failure names the npm that would actually run.
    const verdict = npmProvenance(macAssembly);
    expect(verdict.pinned ? 'pinned' : `unpinned - ${verdict.reason}`).toBe('pinned');
  });

  it('reconstructs the Windows Pi tree with npm from the staged Node distribution', () => {
    expect(npmProvenance(windowsAssembly).pinned).toBe(true);
  });

  it('keeps the npm ci flags that the pinned tree was reconstructed with', () => {
    for (const flag of ['--ignore-scripts', '--no-audit', '--no-fund']) expect(macAssembly).toContain(flag);
    expect(macAssembly).toContain('--offline');
  });

  it('leaves the Pi tree pin untouched: the invocation is wrong, the pin is not', () => {
    expect(pins.pi.treeSha256).toBe('23c70701cca3e33a2d9139e784487d2f6e3b9dffb8cf87dd45a98c16348c04cc');
    expect(pins.pi.version).toBe('0.84.1');
    expect(pins.pi.packageLockSha256).toBe('213e7be737db598b7649792d539a697769e728d52889cec3a9447908674df314');
  });
});
