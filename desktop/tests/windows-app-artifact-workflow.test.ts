import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import pins from '../scripts/resource-pins.json';
import packageJson from '../package.json';

// The goal is a link a person can click. CI now builds the macOS app
// (`desktop-mac-app` in test.yml) and leaves it as a run artifact; there is no
// Windows equivalent anywhere -- no job in any workflow runs on a `windows-*`
// runner. This file states the shape of the job that fixes that: on every
// branch push, build the Windows installer and leave it as a run artifact.
//
// It states just as firmly what the job must NOT become. Publishing installers
// and cutting releases are forbidden to us, and tags are Maksim's. So the job
// lives in test.yml -- which fires on branch pushes and pull requests -- and
// never in release.yml or canary-release.yml, which fire on tags. The absence
// of any release action, of `gh release`, of tag creation, and of write
// permission is asserted, not assumed.
//
// What this file cannot do: it cannot run GitHub Actions. It fixes the FORM of
// the workflow -- that the job exists, where it lives, what it builds with,
// what it provisions first, what it refuses to publish. It does not prove the
// build passes on a windows-latest runner, and nobody here has watched it try.
// That remains unverified, deliberately and visibly.

// ---------------------------------------------------------------------------
// A reader for the workflow subset these files are written in.
//
// js-yaml sits in node_modules only as a transitive dependency of eslint and
// electron-builder; a test that reaches for it would break the day either one
// drops it. So the subset is read here, and -- as with the npm provenance rule
// next door -- the reader is first shown to read a fixture correctly, before
// any claim is made about the real files.
//
// This is the same reader mac-app-artifact-workflow.test.ts carries. It is
// exported here so that copy can become `import { parseWorkflow, asMap, asList,
// asText } from './windows-app-artifact-workflow.test';` and the duplicate can
// go; that edit belongs to whoever owns the mac file, not to this one.
// ---------------------------------------------------------------------------

type YamlValue = string | YamlValue[] | { [key: string]: YamlValue };
type YamlMap = { [key: string]: YamlValue };

function stripComment(line: string) {
  let quote = '';
  for (let index = 0; index < line.length; index += 1) {
    const character = line[index];
    if (quote) {
      if (character === quote) quote = '';
      continue;
    }
    if (character === "'" || character === '"') { quote = character; continue; }
    if (character === '#' && (index === 0 || /\s/.test(line[index - 1]))) return line.slice(0, index);
  }
  return line;
}

const indentOf = (line: string) => line.length - line.trimStart().length;
const isBlank = (line: string) => stripComment(line).trim() === '';

function unquote(text: string) {
  const trimmed = text.trim();
  if (/^'[^]*'$/.test(trimmed)) return trimmed.slice(1, -1).replace(/''/g, "'");
  if (/^"[^]*"$/.test(trimmed)) return trimmed.slice(1, -1).replace(/\\(.)/g, '$1');
  return trimmed;
}

function flowSequence(text: string): YamlValue[] {
  const inner = text.trim().slice(1, -1).trim();
  return inner === '' ? [] : inner.split(',').map((item) => unquote(item));
}

// `run: |` and `cache-dependency-path: |`. Comment syntax does not apply inside
// a block scalar: a `#` line there is content, usually a shell comment.
function blockScalar(lines: string[], start: number, parentIndent: number, style: string): [string, number] {
  const collected: string[] = [];
  let bodyIndent = -1;
  let index = start;
  for (; index < lines.length; index += 1) {
    if (lines[index].trim() === '') { collected.push(''); continue; }
    if (indentOf(lines[index]) <= parentIndent) break;
    if (bodyIndent < 0) bodyIndent = indentOf(lines[index]);
    collected.push(lines[index].slice(bodyIndent));
  }
  while (collected.length > 0 && collected[collected.length - 1] === '') collected.pop();
  return [collected.join(style.startsWith('>') ? ' ' : '\n'), index];
}

function nextContent(lines: string[], from: number) {
  let index = from;
  while (index < lines.length && isBlank(lines[index])) index += 1;
  return index;
}

function parseNode(lines: string[], start: number, indent: number): [YamlValue, number] {
  const index = nextContent(lines, start);
  if (index >= lines.length) return ['', index];
  return /^-(\s|$)/.test(stripComment(lines[index]).trim())
    ? parseSequence(lines, index, indent)
    : parseMapping(lines, index, indent);
}

function parseSequence(lines: string[], start: number, indent: number): [YamlValue[], number] {
  const items: YamlValue[] = [];
  let index = start;
  while (index < lines.length) {
    if (isBlank(lines[index])) { index += 1; continue; }
    const content = stripComment(lines[index]).trimEnd();
    if (indentOf(content) < indent) break;
    const bare = content.trimStart();
    if (!/^-(\s|$)/.test(bare)) break;
    const after = bare.slice(1);
    const column = indentOf(content) + 1 + (after.length - after.trimStart().length);
    const value = after.trim();
    if (value === '') {
      const probe = nextContent(lines, index + 1);
      const [nested, next] = parseNode(lines, probe, probe < lines.length ? indentOf(stripComment(lines[probe])) : column);
      items.push(nested);
      index = next;
      continue;
    }
    // `- uses: x` opens a mapping whose later keys sit under the dash, so the
    // dash is rewritten as padding and the mapping is read from that column.
    if (/^(?:"[^"]*"|'[^']*'|[A-Za-z0-9_.$-]+)\s*:(\s|$)/.test(value)) {
      const patched = lines.slice();
      patched[index] = ' '.repeat(column) + value;
      const [mapping, next] = parseMapping(patched, index, column);
      items.push(mapping);
      index = next;
      continue;
    }
    items.push(value.startsWith('[') ? flowSequence(value) : unquote(value));
    index += 1;
  }
  return [items, index];
}

function parseMapping(lines: string[], start: number, indent: number): [YamlMap, number] {
  const mapping: YamlMap = {};
  let index = start;
  while (index < lines.length) {
    if (isBlank(lines[index])) { index += 1; continue; }
    const content = stripComment(lines[index]).trimEnd();
    if (indentOf(content) < indent) break;
    if (indentOf(content) > indent) throw new Error(`unexpected indent on workflow line ${index + 1}: ${content}`);
    const bare = content.trimStart();
    if (/^-(\s|$)/.test(bare)) break;
    const entry = /^("[^"]*"|'[^']*'|[^:]+?)\s*:(?:\s+([^]*))?$/.exec(bare);
    if (!entry) throw new Error(`cannot read workflow line ${index + 1}: ${content}`);
    const key = unquote(entry[1]);
    const value = (entry[2] ?? '').trim();
    if (/^[|>][-+]?\d*$/.test(value)) {
      const [text, next] = blockScalar(lines, index + 1, indentOf(content), value);
      mapping[key] = text;
      index = next;
      continue;
    }
    if (value !== '') {
      mapping[key] = value.startsWith('[') ? flowSequence(value) : unquote(value);
      index += 1;
      continue;
    }
    const probe = nextContent(lines, index + 1);
    const childIndent = probe < lines.length ? indentOf(stripComment(lines[probe])) : -1;
    const childIsOwnSequence = childIndent === indentOf(content) && /^-(\s|$)/.test(stripComment(lines[probe]).trim());
    if (childIndent > indentOf(content) || childIsOwnSequence) {
      const [nested, next] = parseNode(lines, probe, childIndent);
      mapping[key] = nested;
      index = next;
      continue;
    }
    mapping[key] = '';
    index += 1;
  }
  return [mapping, index];
}

export function parseWorkflow(text: string): YamlMap {
  const [value] = parseMapping(text.replace(/\r\n/g, '\n').split('\n'), 0, 0);
  return value;
}

export const asMap = (value: YamlValue | undefined): YamlMap =>
  value === undefined || typeof value === 'string' || Array.isArray(value) ? {} : value;
export const asList = (value: YamlValue | undefined): YamlValue[] =>
  value === undefined ? [] : Array.isArray(value) ? value : [value];
export const asText = (value: YamlValue | undefined): string => (typeof value === 'string' ? value : '');

const readerFixture = `
name: Fixture
# a whole-line comment

on:
  push:
    branches-ignore: ['main', "release"]
  pull_request:

jobs:
  build:
    runs-on: windows-latest
    permissions:
      contents: read
    defaults:
      run:
        shell: bash
    steps:
      - uses: actions/checkout@abc123 # v4
        with:
          fetch-depth: 0
      - name: Two things
        run: |
          echo one
          # not a yaml comment
          echo two
      - uses: actions/setup-node@def456
        with:
          node-version: '22.23.1'
          cache-dependency-path: |
            desktop/package-lock.json
            desktop/runtime/pi/package-lock.json
`;

describe('the workflow reader', () => {
  const parsed = parseWorkflow(readerFixture);
  const job = asMap(asMap(parsed.jobs).build);
  const steps = asList(job.steps).map(asMap);

  it('reads triggers, including a flow sequence and a valueless key', () => {
    expect(asList(asMap(asMap(parsed.on).push)['branches-ignore'])).toEqual(['main', 'release']);
    expect(Object.keys(asMap(parsed.on))).toEqual(['push', 'pull_request']);
  });

  it('reads a job, its runner, its permissions and its step count', () => {
    expect(asText(job['runs-on'])).toBe('windows-latest');
    expect(asText(asMap(job.permissions).contents)).toBe('read');
    expect(steps).toHaveLength(3);
  });

  it('reads the shell a job defaults its run steps to', () => {
    expect(asText(asMap(asMap(job.defaults).run).shell)).toBe('bash');
  });

  it('drops trailing comments but keeps a "#" line inside a block scalar', () => {
    expect(asText(steps[0].uses)).toBe('actions/checkout@abc123');
    expect(asText(steps[1].run)).toBe('echo one\n# not a yaml comment\necho two');
  });

  it('reads a nested with: block and a multi-line path list', () => {
    expect(asText(asMap(steps[0].with)['fetch-depth'])).toBe('0');
    expect(asText(asMap(steps[2].with)['node-version'])).toBe('22.23.1');
    expect(asText(asMap(steps[2].with)['cache-dependency-path']).split('\n'))
      .toEqual(['desktop/package-lock.json', 'desktop/runtime/pi/package-lock.json']);
  });
});

// ---------------------------------------------------------------------------
// Where the installer lands, derived rather than guessed.
//
// The mac job uploads a path a human typed. On Windows the name is assembled by
// electron-builder from the build config, and typing today's spelling into the
// workflow is how a renamed artifact turns into a green run with nothing in it.
// So the expected path is computed here from desktop/package.json:
//
//   directories.output   release
//   win.target           nsis                     -> extension exe
//   nsis.artifactName    Void-Code-${version}-windows-${arch}.${ext}
//   version              0.1.0
//   the --x64 in the package:win script            -> ${arch} is x64
//
// One subtlety decides whether `${arch}` survives. electron-builder normally
// drops the arch of a default-arch build (platformPackager.js:547), but only
// when the pattern is its own default; a pattern the config supplies sets
// `isUserForced` and the arch is always substituted. This config supplies one,
// so x64 stays in the name.
// ---------------------------------------------------------------------------

const extensionForTarget: Record<string, string> = { nsis: 'exe', 'nsis-web': 'exe', portable: 'exe', msi: 'msi', appx: 'appx', zip: 'zip', '7z': '7z' };

type BuildConfig = {
  version?: string;
  scripts?: Record<string, string>;
  build?: {
    directories?: { output?: string };
    win?: { target?: string; artifactName?: string };
    nsis?: { artifactName?: string };
    artifactName?: string;
    productName?: string;
    [key: string]: unknown;
  };
};

function windowsInstallerPath(config: BuildConfig, scriptName: string): string {
  const build = config.build ?? {};
  const script = config.scripts?.[scriptName];
  if (script === undefined) throw new Error(`package.json has no ${scriptName} script`);
  const arch = /--(x64|arm64|ia32|armv7l)\b/.exec(script)?.[1];
  if (arch === undefined) throw new Error(`${scriptName} names no architecture: ${script}`);
  const target = build.win?.target;
  if (target === undefined) throw new Error('package.json build.win declares no target');
  const extension = extensionForTarget[target];
  if (extension === undefined) throw new Error(`unknown Windows target, so the artifact extension is unknown: ${target}`);
  const pattern = build[target as 'nsis']?.artifactName ?? build.win?.artifactName ?? build.artifactName;
  if (pattern === undefined) throw new Error(`no artifactName pattern for the ${target} target`);
  const productName = build.productName ?? '';
  const expanded = pattern.replace(/\$\{(\w+)\}/g, (_whole, macro: string) => {
    const value: Record<string, string | undefined> = {
      version: config.version,
      arch,
      ext: extension,
      os: 'win',
      productName,
      name: productName.replace(/\s+/g, '-'),
    }[macro];
    if (value === undefined) throw new Error(`unsupported artifactName macro \${${macro}}`);
    return value;
  });
  return `${build.directories?.output ?? 'dist'}/${expanded}`;
}

describe('the installer-path derivation', () => {
  it('expands a pattern from a synthetic config, arch and extension included', () => {
    const synthetic: BuildConfig = {
      version: '9.9.9',
      scripts: { 'package:win': 'npm run build && electron-builder --win --arm64' },
      build: { directories: { output: 'out' }, productName: 'Some Thing', win: { target: 'nsis' }, nsis: { artifactName: '${productName}-${version}-${arch}.${ext}' } },
    };
    expect(windowsInstallerPath(synthetic, 'package:win')).toBe('out/Some Thing-9.9.9-arm64.exe');
  });

  it('falls back from the target block to win.artifactName', () => {
    const synthetic: BuildConfig = {
      version: '1.0.0',
      scripts: { 'package:win': 'electron-builder --win --x64' },
      build: { directories: { output: 'release' }, productName: 'App', win: { target: 'portable', artifactName: '${name}-${arch}.${ext}' } },
    };
    expect(windowsInstallerPath(synthetic, 'package:win')).toBe('release/App-x64.exe');
  });

  const refusals: ReadonlyArray<readonly [string, BuildConfig, string]> = [
    ['a script that names no architecture', { version: '1.0.0', scripts: { 'package:win': 'electron-builder --win' }, build: { win: { target: 'nsis' }, nsis: { artifactName: 'a.${ext}' } } }, 'names no architecture'],
    ['a target whose extension is unknown', { version: '1.0.0', scripts: { 'package:win': 'electron-builder --win --x64' }, build: { win: { target: 'squirrel' } } }, 'unknown Windows target'],
    ['a macro nothing here can expand', { version: '1.0.0', scripts: { 'package:win': 'electron-builder --win --x64' }, build: { win: { target: 'nsis' }, nsis: { artifactName: '${channel}.${ext}' } } }, 'unsupported artifactName macro'],
    ['a missing script', { version: '1.0.0', scripts: {}, build: { win: { target: 'nsis' } } }, 'no package:win script'],
  ];

  it.each(refusals)('refuses %s rather than inventing a name', (_name, config, reason) => {
    expect(() => windowsInstallerPath(config, 'package:win')).toThrow(reason);
  });
});

const installerPath = windowsInstallerPath(packageJson as BuildConfig, 'package:win');

describe('the derived installer path agrees with the rest of the repository', () => {
  it('matches the path scripts/windows-package-check.mjs hashes after a local Windows build', () => {
    // An independent witness, written by hand against a real build on a real
    // Windows machine. If the derivation and that literal ever disagree, one of
    // the two is wrong and the workflow would upload nothing.
    const check = readFileSync(new URL('../scripts/windows-package-check.mjs', import.meta.url), 'utf8');
    const literal = /path\.join\(release,\s*'([^']+\.exe)'\)/.exec(check)?.[1];
    expect(literal ?? 'scripts/windows-package-check.mjs no longer names an installer file').toBe(installerPath.replace(/^release\//, ''));
  });
});

// ---------------------------------------------------------------------------
// The shell trap.
//
// On a Windows runner the default shell is PowerShell, not bash, and PowerShell
// does not stop at the first failing command. GitHub appends
// `if ((Test-Path -LiteralPath variable:\LASTEXITCODE)) { exit $LASTEXITCODE }`
// to the script, so a multi-command `run:` block reports the exit code of its
// LAST command and swallows every failure before it. A provisioning step whose
// download failed would go green and the packaging step would then die on a
// digest mismatch, or worse, not die at all.
//
// `shell: bash` is run as `bash --noprofile --norc -eo pipefail`, which stops at
// the first failure. So the rule below: a step running more than one command on
// a Windows runner must declare bash; a single-command step is safe anywhere,
// because its exit code is the step's exit code under every shell.
//
// Exercised against both tables before it is pointed at the real job, so a rule
// that always answered "fine" could not masquerade as a passing suite.
// ---------------------------------------------------------------------------

type ShellVerdict = { readonly safe: boolean; readonly reason: string };

// Quoted runs are blanked before separators are counted, so a `;` or `&&`
// inside an argument does not read as a second command.
function commandCount(script: string) {
  const skeleton = script
    .replace(/'[^'\n]*'/g, "''")
    .replace(/"[^"\n]*"/g, '""')
    .replace(/\\\n\s*/g, ' ')
    .replace(/`\n\s*/g, ' ');
  return skeleton
    .split(/\n|&&|\|\||;/)
    .map((command) => command.trim())
    .filter((command) => command !== '' && !command.startsWith('#')).length;
}

function shellVerdict(shell: string, script: string): ShellVerdict {
  const commands = commandCount(script);
  if (commands === 0) return { safe: true, reason: 'nothing to run' };
  if (commands === 1) return { safe: true, reason: 'a single command, so its exit code is the step exit code under any shell' };
  const named = shell === '' ? 'powershell (the Windows default)' : shell;
  if (/^(?:bash|sh)$/.test(shell)) return { safe: true, reason: `${commands} commands under ${shell}, which stops at the first failure` };
  return { safe: false, reason: `${commands} commands under ${named}, which reports only the last exit code and swallows earlier failures` };
}

describe('the Windows shell rule', () => {
  const accepted: ReadonlyArray<readonly [string, string, string]> = [
    ['a single command under the default shell', '', 'npm run package:win'],
    ['a single command with a quoted separator', '', 'node scripts/pin.mjs --note "download; then verify"'],
    ['a wrapped single command under the default shell', '', 'node scripts/fetch.mjs \\\n  --out runtime/cache'],
    ['two commands under bash', 'bash', 'npm ci\nnpm run package:win'],
    ['a chain under bash', 'bash', 'npm ci && npm run package:win'],
    ['a comment above a single command under the default shell', '', '# provision first\nnpm run package:win'],
  ];
  const rejected: ReadonlyArray<readonly [string, string, string]> = [
    ['two commands under the default shell', '', 'npm ci\nnpm run package:win'],
    ['a chain under the default shell', '', 'npm ci && npm run package:win'],
    ['two commands under pwsh', 'pwsh', 'npm ci\nnpm run package:win'],
    ['two commands under powershell', 'powershell', 'npm ci\nnpm run package:win'],
    ['two commands under cmd', 'cmd', 'npm ci\nnpm run package:win'],
  ];

  it.each(accepted)('accepts %s', (_name, shell, script) => {
    expect(shellVerdict(shell, script).safe).toBe(true);
  });

  it.each(rejected)('rejects %s', (_name, shell, script) => {
    const verdict = shellVerdict(shell, script);
    expect(verdict.safe).toBe(false);
    expect(verdict.reason).toContain('swallows earlier failures');
  });
});

// ---------------------------------------------------------------------------
// The real workflows.
// ---------------------------------------------------------------------------

const workflowText = (name: string) => readFileSync(new URL(`../../.github/workflows/${name}`, import.meta.url), 'utf8');
const workflow = (name: string) => parseWorkflow(workflowText(name));

const testWorkflow = workflow('test.yml');
const packagesForWindows = (job: YamlValue) =>
  asList(asMap(job).steps).map(asMap).some((step) => /\bpackage:win\b/.test(asText(step.run)));
const packagingJobs = Object.entries(asMap(testWorkflow.jobs)).filter(([, job]) => packagesForWindows(job));

const missing = 'no job in .github/workflows/test.yml runs `npm run package:win`';
const found = packagingJobs.length === 1;
const packagingJob = asMap(packagingJobs[0]?.[1]);
const steps = asList(packagingJob.steps).map(asMap);
const jobShell = asText(asMap(asMap(packagingJob.defaults).run).shell) || asText(asMap(asMap(testWorkflow.defaults).run).shell);
const transcript = steps.map((step) => `${asText(step.uses)}\n${asText(step.run)}`).join('\n \n');
const packagingIndex = steps.findIndex((step) => /\bpackage:win\b/.test(asText(step.run)));
const uploadIndex = steps.findIndex((step) => /actions\/upload-artifact/.test(asText(step.uses)));
const uploadStep = asMap(steps[uploadIndex]?.with);
const reason = (verdict: string) => (found ? verdict : missing);

describe('test.yml builds the Windows installer and leaves it as a run artifact', () => {
  it('has exactly one job that packages the desktop app for Windows', () => {
    expect(packagingJobs.map(([name]) => name).join(', ') || missing).not.toBe(missing);
    expect(packagingJobs).toHaveLength(1);
  });

  it('builds on a Windows runner, because electron-builder makes an NSIS installer only there', () => {
    // scripts/assemble-windows-resources.mjs refuses to run anywhere else:
    // `if (process.platform !== 'win32' ...) throw`.
    expect(reason(asText(packagingJob['runs-on']))).toBe('windows-latest');
  });

  it('checks out the repository', () => {
    const uses = steps.map((step) => asText(step.uses));
    expect(reason(uses.some((step) => step.startsWith('actions/checkout@')) ? 'checked out' : 'no actions/checkout step')).toBe('checked out');
  });

  it('installs the Node version the Windows pin names', () => {
    // The pin moves; a literal here would rot silently. The workflow has to
    // follow resource-pins.json, not a copy of today's value.
    const node = asMap(steps.find((step) => asText(step.uses).startsWith('actions/setup-node@'))?.with);
    expect(reason(asText(node['node-version']) || 'no actions/setup-node step')).toBe(pins.windows.node.version.replace(/^v/, ''));
  });

  it('caches npm against both lockfiles the assembly installs from', () => {
    const node = asMap(steps.find((step) => asText(step.uses).startsWith('actions/setup-node@'))?.with);
    expect(reason(asText(node.cache) || 'no actions/setup-node step')).toBe('npm');
    expect(asText(node['cache-dependency-path']).split('\n').map((line) => line.trim()).filter(Boolean))
      .toEqual(['desktop/package-lock.json', 'desktop/runtime/pi/package-lock.json']);
  });

  it('pins every action it uses to a commit SHA, as the rest of this file does', () => {
    const floating = steps
      .map((step) => asText(step.uses))
      .filter((uses) => uses !== '' && !/@[0-9a-f]{40}$/.test(uses));
    expect(reason(floating.join(', ') || 'all pinned')).toBe('all pinned');
  });

  it('installs dependencies from the lockfile, never resolving them afresh', () => {
    // `npm run build` needs desktop/node_modules and nothing else provides it.
    // `npm install` would be free to pick different versions than the lock.
    const scripts = steps.map((step) => asText(step.run));
    const loose = scripts.filter((script) => /\bnpm\s+(?:i|install|add)\b/.test(script) && !/--prefix\s+runtime\/pi/.test(script));
    expect(reason(loose.join(' | ') || 'no loose install')).toBe('no loose install');
    expect(reason(scripts.some((script) => /\bnpm\s+ci\b/.test(script)) ? 'installs from the lockfile' : 'nothing runs `npm ci`, so desktop/node_modules is never created'))
      .toBe('installs from the lockfile');
  });

  it('provisions the pinned Windows resources before it packages', () => {
    // assemble-windows-resources.mjs reads two files that no checkout carries
    // and no `npm ci` creates: runtime/cache/vc/vc.exe and
    // runtime/cache/node/node-<pin>-win-x64.zip. Each is compared against its
    // sha256 in resource-pins.json and the assembly throws on a mismatch, so a
    // job that goes straight from installing to packaging cannot succeed.
    // `npm run setup` does not help: it fetches the darwin archive.
    const before = steps.slice(0, packagingIndex < 0 ? 0 : packagingIndex).map((step) => asText(step.run)).filter(Boolean);
    const provisioning = before.filter((script) => !/^\s*npm\s+(?:ci|run\s+build)\b/.test(script.trim()) || commandCount(script) > 1);
    expect(reason(packagingIndex < 0 ? 'no `npm run package:win` step'
      : provisioning.length > 0 ? 'provisioned first'
        : 'nothing between the install and the packaging fetches the pinned vc.exe and the pinned Windows Node archive'))
      .toBe('provisioned first');
  });

  it('does not retype a pinned digest that resource-pins.json already holds', () => {
    // Two copies of a hash drift, and the copy in the workflow is the one
    // nobody updates. Whatever the provisioning step is, it has to read the pin.
    const text = workflowText('test.yml');
    const copied = [pins.windows.vc.sha256, pins.windows.node.sourceArchiveSha256].filter((digest) => text.includes(digest));
    expect(reason(copied.join(', ') || 'no digest copied into the workflow')).toBe('no digest copied into the workflow');
  });

  it('packages through `npm run package:win`, never electron-builder by hand', () => {
    // package:win is build + assemble-windows-resources + electron-builder
    // --win --x64. Calling electron-builder directly skips the assembly, and
    // the private runtime the app needs is exactly what the assembly stages.
    const scripts = steps.map((step) => asText(step.run));
    const byHand = scripts.filter((script) => /(?:^|[\s/])electron-builder\s/.test(script));
    expect(reason(byHand.join(' | ') || 'through the npm script')).toBe('through the npm script');
    expect(reason(scripts.some((script) => /npm\s+run\s+(?:--\S+\s+)*package:win\b/.test(script)) ? 'through the npm script' : 'no `npm run package:win`'))
      .toBe('through the npm script');
  });

  it('runs the packaging step inside desktop/, where that script lives', () => {
    const step = asMap(steps[packagingIndex]);
    const located = /(?:^|\W)desktop(?:\W|$)/.test(`${asText(step['working-directory'])} ${asText(step.run)}`);
    expect(reason(located ? 'in desktop/' : 'runs from the repository root, where package:win does not exist')).toBe('in desktop/');
  });

  it('runs every multi-command step under a shell that stops at the first failure', () => {
    const unsafe = steps
      .map((step) => ({ name: asText(step.name) || asText(step.uses), verdict: shellVerdict(asText(step.shell) || jobShell, asText(step.run)) }))
      .filter((step) => !step.verdict.safe);
    expect(reason(unsafe.map((step) => `${step.name}: ${step.verdict.reason}`).join(' | ') || 'every step stops on failure'))
      .toBe('every step stops on failure');
  });
});

describe('the Windows artifact is the installer electron-builder actually writes', () => {
  it('uploads the NSIS installer at the path the build config derives', () => {
    expect(reason(asText(uploadStep.path) || 'no actions/upload-artifact step')).toBe(`desktop/${installerPath}`);
  });

  it('does not upload the unpacked application directory', () => {
    // release/win-unpacked is the staging tree electron-builder packs into the
    // installer. Uploading it hands a person a folder they cannot install, and
    // costs several hundred megabytes to say so.
    const path = asText(uploadStep.path);
    expect(reason(/win-unpacked/.test(path) ? `uploads the unpacked tree: ${path}` : 'uploads the installer')).toBe('uploads the installer');
  });

  it('packages first and uploads second', () => {
    expect(reason(uploadIndex < 0 ? 'no actions/upload-artifact step'
      : packagingIndex < 0 ? 'no `npm run package:win` step'
        : packagingIndex < uploadIndex ? 'package, then upload' : 'uploads before packaging'))
      .toBe('package, then upload');
  });

  it('fails the run rather than uploading nothing when the installer is not there', () => {
    // Without this, a renamed output path turns into a green run with an empty
    // artifact -- the failure mode that wastes a day before anyone notices.
    expect(reason(asText(uploadStep['if-no-files-found']) || 'if-no-files-found is unset, so a missing installer only warns')).toBe('error');
  });

  it('names the artifact so a person can tell what they are downloading', () => {
    const name = asText(uploadStep.name);
    expect(reason(/win/i.test(name) ? 'named for Windows' : `artifact name does not say what platform it is: ${name || '(unset)'}`)).toBe('named for Windows');
  });

  it('does not collide with the macOS artifact name', () => {
    const macJob = Object.values(asMap(testWorkflow.jobs)).find((job) => asList(asMap(job).steps).map(asMap).some((step) => /\bpackage:mac\b/.test(asText(step.run))));
    const macName = asText(asMap(asList(asMap(macJob).steps).map(asMap).find((step) => /actions\/upload-artifact/.test(asText(step.uses)))?.with).name);
    expect(reason(asText(uploadStep.name) === macName ? `both jobs upload an artifact called ${macName}` : 'distinct artifact names')).toBe('distinct artifact names');
  });

  it('runs on every branch push, not behind a ref condition', () => {
    const condition = asText(packagingJob.if);
    expect(reason(/github\.(?:ref|event_name)/.test(condition) ? `gated on a ref: ${condition}` : 'unconditional')).toBe('unconditional');
  });
});

describe('the Windows artifact job publishes nothing', () => {
  // Release, installer publication and tag creation are forbidden to us without
  // a separate gate. The job may leave a build behind for the run; it may not
  // put anything where a stranger can reach it. An installer is the single most
  // publishable thing this repository produces, so the rule matters most here.
  const forbidden: ReadonlyArray<readonly [string, RegExp]> = [
    ['a GitHub release action', /softprops\/action-gh-release|ncipollo\/release-action|actions\/create-release/],
    ['`gh release`', /\bgh\s+release\b/],
    ['a release call through the API', /\bgh\s+api\b[^\n]*releases/],
    ['tag creation', /\bgit\s+tag\b|\brefs\/tags\//],
    ['a push of tags', /\bgit\s+push\b[^\n]*(?:--tags|--follow-tags|tag)/],
    ['a package publish', /\bnpm\s+publish\b|\bdocker\s+push\b/],
    ['an electron-builder publish', /--publish(?:=|\s+)(?:always|onTag|onTagOrDraft)|\bepublish\b/],
  ];

  it.each(forbidden)('the job contains no %s', (_name, pattern) => {
    expect(reason(pattern.test(transcript) ? `found ${pattern}` : 'absent')).toBe('absent');
  });

  it('asks for read-only permissions, so publishing is refused by the token and not merely by us', () => {
    const permissions = asMap(packagingJob.permissions);
    const writes = Object.entries(permissions).filter(([, value]) => asText(value) === 'write');
    expect(reason(Object.keys(permissions).length === 0 ? 'the job declares no permissions block' : writes.map(([scope]) => scope).join(', ') || 'read-only'))
      .toBe('read-only');
    expect(reason(asText(permissions.contents) || 'contents is unset')).toBe('read');
  });

  it.each(forbidden)('the whole of test.yml contains no %s', (_name, pattern) => {
    expect(pattern.test(workflowText('test.yml'))).toBe(false);
  });
});

describe('Windows packaging stays out of the tag-triggered workflows', () => {
  it('test.yml fires on branch pushes and pull requests, never on a tag', () => {
    const triggers = asMap(testWorkflow.on);
    expect(Object.keys(triggers).sort()).toEqual(['pull_request', 'push']);
    expect(asMap(triggers.push).tags).toBeUndefined();
    expect(asList(asMap(triggers.push)['branches-ignore'])).toContain('main');
  });

  it.each(['release.yml', 'canary-release.yml'])('%s stays tag-triggered and gains no Windows packaging', (name) => {
    const released = workflow(name);
    expect(asList(asMap(asMap(released.on).push).tags).length).toBeGreaterThan(0);
    expect(Object.entries(asMap(released.jobs)).filter(([, job]) => packagesForWindows(job)).map(([job]) => job)).toEqual([]);
    expect(Object.entries(asMap(released.jobs)).filter(([, job]) => /^windows-/.test(asText(asMap(job)['runs-on']))).map(([job]) => job)).toEqual([]);
  });
});
