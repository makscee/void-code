import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import pins from '../scripts/resource-pins.json';
import packageJson from '../package.json';
import { asList, asMap, asText, parseWorkflow, type YamlMap, type YamlValue } from './workflow-yaml';
import { conditionHolds, BRANCH_EVENTS, PLAIN_TAG } from './workflow-expressions';

// The goal is a link a person can click. This file states the shape of the work
// that produces one for Windows: on every branch push, build the NSIS installer
// and leave it as a run artifact.
//
// The steps that do it are written down ONCE, in the reusable workflow
// .github/workflows/desktop-windows-app.yml, and both callers reach them
// through `uses:` -- test.yml on every branch push, release.yml behind its
// opt-in gate. They were briefly written down twice, one copy in each place,
// which meant CI checked one description of packaging while a release built
// from another; the two would have drifted, and the drift would have shown up
// in the installer nobody ran CI against. So this file pins the call on the
// test.yml side and the steps on the reusable side, each exactly once.
//
// It states just as firmly what the packaging must NOT become. Publishing
// installers and cutting releases are forbidden to us, and tags are Maksim's --
// and an installer is the single most publishable thing this repository
// produces. So the file holding the steps is reachable only by `workflow_call`
// -- no tag, no event of its own can start it -- and the absence of any release
// action, of `gh release`, of tag creation, and of write permission is
// asserted, not assumed. Whether release.yml's gate is really off by default is
// evaluated in release-desktop-optin-workflow.test.ts; here only its presence
// is pinned.
//
// What this file cannot do: it cannot run GitHub Actions. It fixes the FORM of
// the workflow -- that the job exists, where it lives, what it builds with,
// what it provisions first, what it refuses to publish. It does not prove the
// build passes on a windows-latest runner, and nobody here has watched it try.
// That remains unverified, deliberately and visibly.

// ---------------------------------------------------------------------------
// The workflow reader is shared: tests/workflow-yaml.ts. It is a reader, not a
// validator, so it is shown to read this fixture -- the shapes this file cares
// about, a Windows runner and a job-level shell among them -- correctly, before
// any claim is made about the real files.
// ---------------------------------------------------------------------------

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
//
// There is one description of Windows packaging and it lives in
// .github/workflows/desktop-windows-app.yml. test.yml and release.yml both
// reach it through `uses:`; neither repeats its steps. That is the whole point
// of the arrangement: the steps CI exercises on every branch push are, byte for
// byte, the steps a release builds with, because they are the same seven lines
// in the same file. Two copies would be free to drift, and the copy that drifts
// is always the one nobody runs.
//
// So the steps below are asserted once, against the reusable workflow, and
// test.yml is asserted to CALL it rather than to contain it.
// ---------------------------------------------------------------------------

const workflowText = (name: string) => readFileSync(new URL(`../../.github/workflows/${name}`, import.meta.url), 'utf8');
const workflow = (name: string) => parseWorkflow(workflowText(name));

const REUSABLE = 'desktop-windows-app.yml';
const MAC_REUSABLE = 'desktop-mac-app.yml';
const CALL = `./.github/workflows/${REUSABLE}`;

const testWorkflow = workflow('test.yml');
const reusableWorkflow = workflow(REUSABLE);

const stepsOf = (job: YamlValue) => asList(asMap(job).steps).map(asMap);
const packagesForWindows = (job: YamlValue) => stepsOf(job).some((step) => /\bpackage:win\b/.test(asText(step.run)));
// `uses: ./.github/workflows/x.yml` -- a ref may be appended, and is not what
// identifies the file.
const localCall = (job: YamlValue) => {
  const uses = asText(asMap(job).uses).split('@')[0];
  return uses.startsWith('./.github/workflows/') ? uses.slice('./.github/workflows/'.length) : '';
};
const callsTheReusableWorkflow = (job: YamlValue) => `./.github/workflows/${localCall(job)}` === CALL;

// Any route from a job to the packaging steps, whether it holds them itself or
// calls a workflow in this repository that does. A rule that only read a job's
// own steps would answer "no packaging here" about a file whose whole desktop
// half is one `uses:` line.
const reachesPackaging = (job: YamlValue): boolean => {
  if (packagesForWindows(job)) return true;
  const called = localCall(job);
  return called !== '' && Object.values(asMap(workflow(called).jobs)).some(packagesForWindows);
};

const namesOf = (workflowFile: YamlMap, predicate: (job: YamlValue) => boolean) =>
  Object.entries(asMap(workflowFile.jobs)).filter(([, job]) => predicate(job)).map(([name]) => name);

// ---------------------------------------------------------------------------
// The reusable workflow: the only place the packaging steps are written down.
// ---------------------------------------------------------------------------

const packagingJobs = Object.entries(asMap(reusableWorkflow.jobs)).filter(([, job]) => packagesForWindows(job));

const missing = `no job in .github/workflows/${REUSABLE} runs \`npm run package:win\``;
const found = packagingJobs.length === 1;
const packagingJob = asMap(packagingJobs[0]?.[1]);
const steps = asList(packagingJob.steps).map(asMap);
const jobShell = asText(asMap(asMap(packagingJob.defaults).run).shell) || asText(asMap(asMap(reusableWorkflow.defaults).run).shell);
const transcript = steps.map((step) => `${asText(step.uses)}\n${asText(step.run)}`).join('\n \n');
const packagingIndex = steps.findIndex((step) => /\bpackage:win\b/.test(asText(step.run)));
const uploadIndex = steps.findIndex((step) => /actions\/upload-artifact/.test(asText(step.uses)));
const uploadStep = asMap(steps[uploadIndex]?.with);
const reason = (verdict: string) => (found ? verdict : missing);

// A reusable workflow may declare permissions on the job or on the file; the
// file-level block applies to every job in it, so either satisfies the rule and
// neither being present does not.
const effectivePermissions = Object.keys(asMap(packagingJob.permissions)).length > 0
  ? asMap(packagingJob.permissions)
  : asMap(reusableWorkflow.permissions);

describe(`${REUSABLE} builds the Windows installer and leaves it as a run artifact`, () => {
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

  it('provisions the pinned Windows Node runtime before it packages', () => {
    const before = steps.slice(0, packagingIndex < 0 ? 0 : packagingIndex).map((step) => asText(step.run)).filter(Boolean);
    const provisioning = before.filter((script) => /resource-pins\.json/.test(script));
    expect(reason(packagingIndex < 0 ? 'no `npm run package:win` step'
      : provisioning.length > 0 ? 'provisioned first'
        : 'nothing fetches the pinned Windows Node archive before packaging'))
      .toBe('provisioned first');
  });

  it('does not retype the pinned Node digest', () => {
    const text = `${workflowText('test.yml')}\n${workflowText(REUSABLE)}`;
    expect(reason(text.includes(pins.windows.node.sourceArchiveSha256) ? pins.windows.node.sourceArchiveSha256 : 'no digest copied into the workflow'))
      .toBe('no digest copied into the workflow');
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
    // The word "desktop" appearing somewhere in the script is not the same as
    // running there: the step has to declare the directory, cd into it, or hand
    // it to npm.
    const directory = asText(asMap(step)['working-directory']).trim();
    const script = asText(asMap(step).run);
    const located = directory === 'desktop'
      || /\bcd\s+desktop\b/.test(script)
      || /\bnpm\b[^\n]*\s(?:--prefix|-C)\s+desktop\b/.test(script);
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
  // The name is derived from desktop/package.json, never retyped. Two spellings
  // keep that promise: the derived path itself, and the same path with the
  // VERSION -- and only the version -- replaced by a glob, so a version bump
  // does not turn into a job that uploads nothing. Everything else about the
  // name has to agree with the config character for character, which is what
  // makes a renamed artifact a red test rather than an empty download.
  const derived = `desktop/${installerPath}`;
  const versionGlobbed = derived.replace(packageJson.version, '*');

  it('uploads the NSIS installer at the path the build config derives', () => {
    const path = asText(uploadStep.path);
    expect(reason(path === '' ? 'no actions/upload-artifact step'
      : path === derived || path === versionGlobbed ? 'the path the build config derives'
        : `${path}, where the config derives ${derived}`))
      .toBe('the path the build config derives');
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
    // artifact -- the failure mode that wastes a day before anyone notices. It
    // is what makes the globbed spelling above safe: a glob that matched
    // nothing fails the job instead of publishing an empty artifact.
    expect(reason(asText(uploadStep['if-no-files-found']) || 'if-no-files-found is unset, so a missing installer only warns')).toBe('error');
  });

  it('names the artifact so a person can tell what they are downloading', () => {
    const name = asText(uploadStep.name);
    expect(reason(/win/i.test(name) ? 'named for Windows' : `artifact name does not say what platform it is: ${name || '(unset)'}`)).toBe('named for Windows');
  });

  it('does not collide with the macOS artifact name', () => {
    // Both reusable workflows are called from the same run of test.yml and from
    // the same release run; two uploads under one name is one artifact and a
    // lost build.
    const macWorkflow = workflow(MAC_REUSABLE);
    const macJob = Object.values(asMap(macWorkflow.jobs)).find((job) => stepsOf(job).some((step) => /\bpackage:mac\b/.test(asText(step.run))));
    const macName = asText(asMap(stepsOf(asMap(macJob)).find((step) => /actions\/upload-artifact/.test(asText(step.uses)))?.with).name);
    expect(reason(macName === '' ? `no upload step in ${MAC_REUSABLE} to compare against`
      : asText(uploadStep.name) === macName ? `both workflows upload an artifact called ${macName}`
        : 'distinct artifact names'))
      .toBe('distinct artifact names');
  });

  it('declares no condition of its own, so the caller alone decides when it runs', () => {
    // release.yml gates its CALL behind the opt-in variable. Any condition in
    // here would be a second switch, invisible from either caller -- so the
    // check is the absence of one, not a judgement about what it says. `if:`
    // anything, even `${{ true }}`, is a finding.
    const condition = asText(packagingJob.if).trim();
    expect(reason(condition === '' ? 'no condition' : `the job carries \`if: ${condition}\``)).toBe('no condition');
  });
});

// ---------------------------------------------------------------------------
// One description, two callers.
// ---------------------------------------------------------------------------

const callingJobs = namesOf(testWorkflow, callsTheReusableWorkflow);
const inlinePackagers = namesOf(testWorkflow, packagesForWindows);
const callingJob = asMap(Object.entries(asMap(testWorkflow.jobs)).find(([, job]) => callsTheReusableWorkflow(job))?.[1]);

describe('test.yml checks the same description of packaging that release.yml builds from', () => {
  it(`reaches Windows packaging by calling ${REUSABLE}`, () => {
    expect(callingJobs.join(', ') || `no job in .github/workflows/test.yml has \`uses: ${CALL}\``)
      .not.toBe(`no job in .github/workflows/test.yml has \`uses: ${CALL}\``);
    expect(callingJobs).toHaveLength(1);
  });

  it('does not repeat the packaging steps inline as well', () => {
    // The defect this replaces: seven steps in test.yml and the same seven in
    // the reusable workflow, so CI checked one description of packaging while
    // the release built from the other.
    expect(inlinePackagers.join(', ') || 'nothing inline')
      .toBe('nothing inline');
  });

  it('runs on every event test.yml fires on, evaluated and not pattern-matched', () => {
    // The condition is worked out, not read for suspicious words. `if: ${{
    // false }}` names neither a ref nor a variable and would have passed a rule
    // that looked for those, while the app was never built on any branch at
    // all. An expression the evaluator cannot read raises instead of coming out
    // false, so an unreadable gate cannot pass for an absent one either.
    const condition = asText(callingJob.if);
    const off = BRANCH_EVENTS.filter(([, context]) => !conditionHolds(condition, context)).map(([event]) => event);
    expect(callingJobs.length === 0 ? `no job in test.yml calls ${REUSABLE}`
      : off.length > 0 ? `\`if: ${condition}\` comes out false on ${off.join(', ')}` : 'runs on all of them')
      .toBe('runs on all of them');
  });

  it('release.yml calls the very same file', () => {
    // Not a similar one, and not a copy: the same path. This is what makes the
    // green run on a branch evidence about the release build.
    const callers = namesOf(workflow('release.yml'), callsTheReusableWorkflow);
    expect(callers.join(', ') || `no job in .github/workflows/release.yml has \`uses: ${CALL}\``)
      .not.toBe(`no job in .github/workflows/release.yml has \`uses: ${CALL}\``);
  });

  it(`${REUSABLE} can be started only by a caller, never by an event of its own`, () => {
    // A file that packages an installer must not also be a file a tag can
    // start. Its only trigger is workflow_call, so the decision to run it is
    // always taken in test.yml or release.yml, where the gates are visible.
    expect(Object.keys(asMap(reusableWorkflow.on))).toEqual(['workflow_call']);
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
    const writes = Object.entries(effectivePermissions).filter(([, value]) => asText(value) === 'write');
    expect(reason(Object.keys(effectivePermissions).length === 0 ? 'neither the job nor the workflow declares a permissions block' : writes.map(([scope]) => scope).join(', ') || 'read-only'))
      .toBe('read-only');
    expect(reason(asText(effectivePermissions.contents) || 'contents is unset')).toBe('read');
  });

  it.each(forbidden)('neither test.yml nor the workflow it calls contains %s', (_name, pattern) => {
    // Both files, because either one could smuggle a publish into the same run.
    expect([`test.yml: ${pattern.test(workflowText('test.yml'))}`, `${REUSABLE}: ${pattern.test(workflowText(REUSABLE))}`])
      .toEqual([`test.yml: ${false}`, `${REUSABLE}: ${false}`]);
  });
});

describe('Windows packaging stays out of the tag-triggered workflows', () => {
  it('test.yml fires on branch pushes and pull requests, never on a tag', () => {
    const triggers = asMap(testWorkflow.on);
    expect(Object.keys(triggers).sort()).toEqual(['pull_request', 'push']);
    expect(asMap(triggers.push).tags).toBeUndefined();
    expect(asList(asMap(triggers.push)['branches-ignore'])).toContain('main');
  });

  it('canary-release.yml stays tag-triggered and reaches no Windows packaging at all', () => {
    const canary = workflow('canary-release.yml');
    expect(asList(asMap(asMap(canary.on).push).tags).length).toBeGreaterThan(0);
    expect(namesOf(canary, reachesPackaging)).toEqual([]);
    expect(namesOf(canary, (job) => /^windows-/.test(asText(asMap(job)['runs-on'])))).toEqual([]);
  });

  it('release.yml stays tag-triggered, and every route it has to packaging is off with nothing set', () => {
    // What it may NOT have is packaging steps of its own, or a Windows runner
    // of its own -- either would be a third description, unreachable from any
    // branch push. What it may have is a call, and every call has to carry a
    // condition. Whether that condition is really off by default is EVALUATED
    // next door, in release-desktop-optin-workflow.test.ts; only its presence
    // is pinned here.
    const released = workflow('release.yml');
    expect(asList(asMap(asMap(released.on).push).tags).length).toBeGreaterThan(0);
    expect(namesOf(released, packagesForWindows)).toEqual([]);
    expect(namesOf(released, (job) => /^windows-/.test(asText(asMap(job)['runs-on'])))).toEqual([]);
    // Every route it has to packaging must be OFF under a plain tag with
    // nothing set -- evaluated, so a condition that gates nothing counts as no
    // gate. That the gate can also be turned ON, and that a dead one therefore
    // cannot pass for a live one, is evaluated next door in
    // release-desktop-optin-workflow.test.ts.
    const routes = Object.entries(asMap(released.jobs)).filter(([, job]) => reachesPackaging(job));
    const on = routes.filter(([, job]) => conditionHolds(asText(asMap(job).if), PLAIN_TAG)).map(([name]) => name);
    expect(routes.length === 0 ? 'release.yml has no route to packaging at all'
      : on.length > 0 ? `${on.join(', ')} runs on an ordinary tag`
        : 'every route is off with nothing set')
      .toBe('every route is off with nothing set');
  });
});
