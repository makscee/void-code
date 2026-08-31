import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { asList, asMap, asText, parseWorkflow, type YamlMap, type YamlValue } from './workflow-yaml';
import { conditionHolds, interpolate, PLAIN_TAG, type Context } from './workflow-expressions';

// release.yml's `desktop-attach` is the job that puts a desktop build into a
// release. It has never run. It is gated on a repository variable that does not
// exist, so every tag so far has skipped it, and nothing about it has ever been
// exercised by anything but the file next door -- which pins that it stays off,
// not what it does when it is switched on. That is the state this file is
// written in, and it is the reason the following was found by reading rather
// than by a red run.
//
// The job downloads two artifacts BY NAME:
//
//     void-code-mac-arm64
//     void-code-windows-x64-installer
//
// and signs provenance for two explicit paths. Meanwhile desktop-mac-app.yml
// has become a matrix over architecture and now produces `void-code-mac-arm64`
// AND `void-code-mac-x64`. The names are declared once in the matrix and typed
// out a second time in release.yml, and the second list did not learn about the
// first one's new row. The consequence is not a red run: it is a green one that
// builds the Intel bundle for twenty minutes of runner time, uploads it as a
// run artifact nobody can reach, and publishes a release that still has nothing
// on it for an Intel Mac.
//
// So the rule this file states is not "attach the Intel build". It is:
//
//   1. Every artifact the release run produces from a desktop build reaches the
//      release. Not a list of today's three -- whatever the build workflows
//      produce, derived from them.
//   2. Everything so attached is provenance-signed. Signing a subset is worse
//      than signing none, because the release then carries attestations that
//      look complete.
//   3. The attaching job stages what it attaches and attaches only what it
//      staged. `gh release upload` on a path no artifact contains fails the job;
//      a path that quietly matches nothing publishes nothing.
//   4. And the one that makes the other three hold tomorrow: THE LIST LIVES IN
//      ONE PLACE. This is stated as a mutation rather than as a style rule.
//      A third architecture is added to the mac matrix, release.yml is left
//      untouched byte for byte, and rules 1 and 2 are asked again; then the
//      arm64 row is REMOVED and rule 3 is asked again. An implementation that
//      enumerates artifact names in release.yml fails both directions, which is
//      exactly how today's arrangement fails. An implementation that derives
//      them -- a download pattern, a glob over the staging directory -- passes
//      both without being told what the new row is called.
//
// What this file cannot do: it cannot push a tag, it cannot run GitHub Actions,
// and it cannot make `desktop-attach` run even once. It fixes the FORM of the
// workflow: which names are produced, which are downloaded, which reach the
// release, which are signed. That a real run would upload real bytes is
// unverified here, by anyone, and a green suite must not be read as saying so.
//
// Everything the neighbouring release-desktop-optin-workflow.test.ts pins --
// off by default, a failed desktop build not withholding the CLI, the triggers
// not widening, exactly one release -- is that file's business and is not
// restated here. The one thing restated is the plain-tag census, from this
// file's own machinery, because a rule about what a switched-ON run attaches is
// worth very little without the matching claim that a switched-off one attaches
// nothing.

// ---------------------------------------------------------------------------
// Reading a release run: what it produces, what it stages, what it attaches.
//
// tests/workflow-yaml.ts is a reader, not a validator, so none of what follows
// is trusted on the real files until it has been shown answering correctly --
// and answering DIFFERENTLY -- on the synthetic workflows further down.
// ---------------------------------------------------------------------------

type Read = (workflow: string) => string;

const LOCAL = './.github/workflows/';

const lines = (text: string) => text.split('\n').map((line) => line.trim()).filter((line) => line !== '');
const basename = (path: string) => path.split('/').filter((part) => part !== '').pop() ?? path;
const normalize = (path: string) => path.replace(/^\.\//, '').replace(/\/{2,}/g, '/');

// A glob as GitHub and the shell mean it: `*` stands for a run of characters
// within one path segment and never crosses a `/`. Both sides may be globs --
// the download step here asks for `void-code-*`, the prefix every desktop
// upload carries -- so the candidate's own `*` is first made into a plausible
// concrete character run and then matched. (The Windows installer itself is no
// longer globbed: its name carries no version, so desktop-windows-app.yml
// uploads it by its exact name.)
const escapeRegExp = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
const asRegExp = (pattern: string) => new RegExp(`^${pattern.split('*').map(escapeRegExp).join('[^/]*')}$`);
const covers = (pattern: string, candidate: string) => asRegExp(normalize(pattern)).test(normalize(candidate).replace(/\*/g, 'x'));

// A matrix is either a list of includes or a single axis of values; both are
// read, and a job without one is a single row. Two or more axes are a product
// this file does not model, and it says so rather than reporting one row and
// letting every rule below pass on a job it did not understand.
function matrixRows(job: YamlMap, where: string): YamlMap[] {
  const matrix = asMap(asMap(job.strategy).matrix);
  const included = asList(matrix.include).map(asMap);
  const axes = Object.entries(matrix).filter(([key]) => key !== 'include' && key !== 'exclude');
  if (axes.length > 1) throw new Error(`${where} has a matrix of ${axes.length} axes, which this test does not model`);
  const spread = axes.length === 1 ? asList(axes[0][1]).map((value) => ({ [axes[0][0]]: asText(value) })) : [];
  const rows = [...spread, ...included];
  return rows.length > 0 ? rows : [{}];
}

// `${{ matrix.x }}` is resolved per row; everything else is left for
// interpolate(), which knows the run's context. Resolving the row first matters:
// `matrix.arch` belongs to a context the evaluator knows, so it would otherwise
// come back as the empty string and two rows would look like one.
const substitute = (text: string, row: YamlMap) =>
  text.replace(/\$\{\{\s*matrix\.([A-Za-z0-9_-]+)\s*\}\}/g, (whole, key: string) => {
    const value = row[key];
    return typeof value === 'string' ? value : whole;
  });

const stepsOf = (job: YamlMap) => asList(job.steps).map(asMap);
const transcriptOf = (job: YamlMap) => stepsOf(job).map((step) => `${asText(step.uses)}\n${asText(step.run)}`).join('\n \n');

// The same question the neighbouring files ask: does this job package the
// desktop app? Answered from what it runs, so a renamed job or a third reusable
// workflow is still recognised.
const PACKAGES_DESKTOP = /\bpackage:mac\b|\bpackage:win\b|(?:^|[\s/])electron-builder\s/;

const RELEASE_ACTION = /softprops\/action-gh-release|ncipollo\/release-action|actions\/create-release/;

type Artifact = {
  readonly workflow: string;
  readonly job: string;
  readonly name: string;
  // What the artifact will contain, at its root. upload-artifact given a single
  // file puts that file at the root, which is what every desktop upload does.
  readonly files: readonly string[];
  readonly desktop: boolean;
};

const jobsOf = (read: Read, workflow: string): [string, YamlMap][] =>
  Object.entries(asMap(parseWorkflow(read(workflow)).jobs)).map(([name, value]) => [name, asMap(value)]);

function uploadsIn(read: Read, workflow: string, context: Context): Artifact[] {
  const artifacts: Artifact[] = [];
  for (const [jobName, job] of jobsOf(read, workflow)) {
    const desktop = PACKAGES_DESKTOP.test(transcriptOf(job));
    for (const row of matrixRows(job, `${workflow}:${jobName}`)) {
      for (const step of stepsOf(job)) {
        if (!/actions\/upload-artifact/.test(asText(step.uses))) continue;
        const settings = asMap(step.with);
        const resolve = (value: YamlValue | undefined) => interpolate(substitute(asText(value), row), context);
        artifacts.push({
          workflow,
          job: jobName,
          name: resolve(settings.name),
          files: lines(resolve(settings.path)).map(basename),
          desktop,
        });
      }
    }
  }
  return artifacts;
}

// Everything in the run's artifact store: what release.yml's own jobs upload,
// plus what every reusable workflow it calls uploads -- the desktop builds being
// exactly that. Jobs whose condition is false in this context contribute
// nothing, which is how a plain tag comes out with the CLI alone.
function producedInRun(read: Read, context: Context): Artifact[] {
  const own = uploadsIn(read, 'release.yml', context);
  const produced: Artifact[] = [];
  for (const [name, job] of jobsOf(read, 'release.yml')) {
    if (!conditionHolds(asText(job.if), context)) continue;
    const called = asText(job.uses);
    if (called.startsWith(LOCAL)) produced.push(...uploadsIn(read, called.slice(LOCAL.length).split('@')[0], context));
    else produced.push(...own.filter((artifact) => artifact.job === name));
  }
  return produced;
}

type Download = {
  readonly path: string;
  readonly name: string;
  readonly pattern: string;
  readonly merged: boolean;
};

type Attacher = {
  readonly job: string;
  // The job that CREATES the release publishes files it made itself --
  // version.json and SHA256SUMS exist only inside it -- so the "attaches only
  // what it staged" rule is not asked of it. It is asked of every other job
  // that adds assets to a release, which is where a desktop build arrives.
  readonly creates: boolean;
  readonly downloads: readonly Download[];
  readonly uploads: readonly string[];
  readonly subjects: readonly string[];
};

function attachersIn(read: Read, context: Context): Attacher[] {
  const attachers: Attacher[] = [];
  for (const [job, body] of jobsOf(read, 'release.yml')) {
    if (!conditionHolds(asText(body.if), context)) continue;
    const downloads: Download[] = [];
    const uploads: string[] = [];
    const subjects: string[] = [];
    let creates = false;
    for (const step of stepsOf(body).filter((step) => conditionHolds(asText(step.if), context))) {
      const uses = asText(step.uses);
      const settings = asMap(step.with);
      const resolve = (value: YamlValue | undefined) => interpolate(asText(value), context);
      if (/actions\/download-artifact/.test(uses)) {
        downloads.push({
          path: resolve(settings.path) || '.',
          name: resolve(settings.name),
          pattern: resolve(settings.pattern),
          merged: resolve(settings['merge-multiple']) === 'true',
        });
      }
      if (RELEASE_ACTION.test(uses)) {
        creates = true;
        uploads.push(...lines(resolve(settings.files)));
      }
      if (/actions\/attest-build-provenance/.test(uses)) subjects.push(...lines(resolve(settings['subject-path'])));
      const script = resolve(step.run);
      for (const call of script.matchAll(/\bgh\s+release\s+(upload|create)\s+\S+([^\n]*)/g)) {
        if (call[1] === 'create') creates = true;
        uploads.push(...call[2].split(/\s+/).filter((word) => word !== '' && !word.startsWith('-')));
      }
    }
    if (downloads.length + uploads.length + subjects.length > 0) attachers.push({ job, creates, downloads, uploads, subjects });
  }
  return attachers;
}

// `member` is the file's name inside the artifact and `file` is where it lands
// on the runner. Both are needed and they are not the same string: the rules
// ask about the first ("was this build published?") by matching globs against
// the second ("does dist/* find it?").
type Staged = { readonly artifact: Artifact; readonly member: string; readonly file: string };

// Where a downloaded artifact's files actually land, which is the detail that
// decides whether a glob over the staging directory finds them. Asked for BY
// NAME, an artifact is unpacked at the download path. Asked for by pattern or
// not at all, each artifact gets a directory of its own named after it --
// unless `merge-multiple` is set, which is the switch that flattens them. A
// pattern download without it stages `dist/void-code-mac-x64/...`, and
// `dist/*` then matches nothing at all.
function stagedBy(attacher: Attacher, produced: readonly Artifact[]): Staged[] {
  const staged: Staged[] = [];
  for (const download of attacher.downloads) {
    for (const artifact of produced) {
      const selected = download.name !== '' ? artifact.name === download.name
        : download.pattern !== '' ? covers(download.pattern, artifact.name)
          : true;
      if (!selected) continue;
      const into = download.name !== '' || download.merged ? download.path : `${download.path}/${artifact.name}`;
      for (const member of artifact.files) staged.push({ artifact, member, file: normalize(`${into}/${member}`) });
    }
  }
  return staged;
}

type Report = {
  readonly trouble: string;
  readonly produced: readonly Artifact[];
  readonly desktopBuilds: readonly string[];
  readonly attachers: readonly Attacher[];
  readonly unattached: readonly string[];
  readonly unsigned: readonly string[];
  readonly phantom: readonly string[];
  readonly strayCli: readonly string[];
};

const EMPTY: Report = {
  trouble: '', produced: [], desktopBuilds: [], attachers: [], unattached: [], unsigned: [], phantom: [], strayCli: [],
};

const identify = (artifact: Artifact, file: string) => `${artifact.name}/${file}`;

function report(read: Read, context: Context): Report {
  let produced: Artifact[];
  let attachers: Attacher[];
  try {
    produced = producedInRun(read, context);
    attachers = attachersIn(read, context);
  } catch (error) {
    // Never the pleasant answer. A workflow this cannot read is reported as
    // unread, because "no missing artifacts were found" and "no artifacts were
    // looked at" are the same green otherwise.
    return { ...EMPTY, trouble: `the release run could not be read: ${(error as Error).message}` };
  }

  const attached = new Set<string>();
  const signed = new Set<string>();
  const phantom: string[] = [];
  const strayCli: string[] = [];

  for (const attacher of attachers) {
    const staged = stagedBy(attacher, produced);
    for (const item of staged) {
      if (attacher.uploads.some((upload) => covers(upload, item.file))) {
        attached.add(identify(item.artifact, item.member));
        if (!attacher.creates && !item.artifact.desktop) {
          strayCli.push(`${attacher.job} re-attaches ${item.file}, which the release job already published`);
        }
      }
      if (attacher.subjects.some((subject) => covers(subject, item.file))) signed.add(identify(item.artifact, item.member));
    }
    if (attacher.creates) continue;
    const unmatched = [
      ...attacher.uploads.map((path) => ['attaches', path] as const),
      ...attacher.subjects.map((path) => ['signs', path] as const),
    ].filter(([, path]) => !staged.some((item) => covers(path, item.file)));
    phantom.push(...unmatched.map(([verb, path]) => `${attacher.job} ${verb} ${path}, which nothing in this run stages`));
  }

  const desktopFiles = produced.filter((artifact) => artifact.desktop)
    .flatMap((artifact) => artifact.files.map((file) => identify(artifact, file)));

  return {
    trouble: '',
    produced,
    desktopBuilds: desktopFiles,
    attachers,
    // Sorted, so a failure reads the same however the matrix rows are ordered.
    unattached: desktopFiles.filter((file) => !attached.has(file)).sort(),
    unsigned: desktopFiles.filter((file) => attached.has(file) && !signed.has(file)).sort(),
    phantom: phantom.sort(),
    strayCli: strayCli.sort(),
  };
}

// ---------------------------------------------------------------------------
// The mutation: a third architecture, and a removed one.
//
// Textual, on the build workflow only, so that what is being asked is exactly
// what would be asked of a real edit: release.yml is not touched, and the
// question is whether it needed to be.
// ---------------------------------------------------------------------------

function editMatrixRow(text: string, value: string, replacement: string | null): string {
  const all = text.replace(/\r\n/g, '\n').split('\n');
  const start = all.findIndex((line) => new RegExp(`^\\s*-\\s+[A-Za-z0-9_-]+:\\s*${escapeRegExp(value)}\\s*$`).test(line));
  if (start < 0) throw new Error(`no matrix row in this workflow has a value of exactly ${value}`);
  const dent = all[start].length - all[start].trimStart().length;
  let end = start + 1;
  while (end < all.length && (all[end].trim() === '' || all[end].length - all[end].trimStart().length > dent)) end += 1;
  const row = all.slice(start, end);
  if (replacement === null) return [...all.slice(0, start), ...all.slice(end)].join('\n');
  const copy = row.map((line) => line.replace(new RegExp(`\\b${escapeRegExp(value)}\\b`, 'g'), replacement));
  return [...all.slice(0, end), ...copy, ...all.slice(end)].join('\n');
}

// `universal` is a macOS architecture electron-builder really builds, so the
// added row is the shape a third one would actually have -- and its artifact
// name is whatever the matrix already spells it, not a name this file invents
// and an implementation could be told about.
const withEditedRow = (read: Read, workflow: string, value: string, replacement: string | null): Read =>
  (name) => (name === workflow ? editMatrixRow(read(workflow), value, replacement) : read(name));

// ---------------------------------------------------------------------------
// The machinery, shown answering on workflows written for the purpose.
//
// Four of them: the arrangement release.yml has today (names typed twice), the
// arrangement that derives them, the same derivation with the one flag missing
// that decides where downloaded files land, and one that attaches without
// signing. Each rule below is asked of all four, so a rule that cannot fail is
// visible here rather than in a release.
// ---------------------------------------------------------------------------

const CLI_AND_MAC = `
name: Fixture
on:
  push:
    tags:
      - 'v*.*.*'

jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - artifact: cli-linux
    steps:
      - name: Build
        run: go build -o \${{ matrix.artifact }} ./cmd/vc
      - uses: actions/upload-artifact@abc123
        with:
          name: \${{ matrix.artifact }}
          path: \${{ matrix.artifact }}

  release:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/download-artifact@abc123
        with:
          path: dist
          merge-multiple: true
      - uses: softprops/action-gh-release@v2
        with:
          files: |
            dist/cli-linux

  desktop-mac-app:
    if: \${{ vars.DESKTOP == 'true' }}
    uses: ./.github/workflows/mac.yml

  attach:
    needs: [release, desktop-mac-app]
    if: \${{ vars.DESKTOP == 'true' }}
    runs-on: ubuntu-latest
    steps:
`;

const MAC = `
name: Mac
on:
  workflow_call:

jobs:
  package:
    runs-on: macos-14
    strategy:
      fail-fast: false
      matrix:
        include:
          - arch: arm64
          - arch: x64
    steps:
      - name: Package
        run: npm run package:mac
      - uses: actions/upload-artifact@abc123
        with:
          name: app-mac-\${{ matrix.arch }}
          path: desktop/release/app-mac-\${{ matrix.arch }}.zip
`;

const BY_NAME = `      - uses: actions/download-artifact@abc123
        with:
          name: app-mac-arm64
          path: dist
      - uses: actions/attest-build-provenance@v2
        with:
          subject-path: |
            dist/app-mac-arm64.zip
      - name: Attach
        run: gh release upload \${{ github.ref_name }} dist/app-mac-arm64.zip --clobber
`;

const DERIVED = `      - uses: actions/download-artifact@abc123
        with:
          pattern: app-*
          merge-multiple: true
          path: dist
      - uses: actions/attest-build-provenance@v2
        with:
          subject-path: |
            dist/*
      - name: Attach
        run: gh release upload \${{ github.ref_name }} dist/* --clobber
`;

// The same derivation with the flag that flattens the download removed, and
// the same one with the provenance step removed. Written out rather than cut
// out of DERIVED, so what each fixture is missing is on the page.
const UNFLATTENED = `      - uses: actions/download-artifact@abc123
        with:
          pattern: app-*
          path: dist
      - uses: actions/attest-build-provenance@v2
        with:
          subject-path: |
            dist/*
      - name: Attach
        run: gh release upload \${{ github.ref_name }} dist/* --clobber
`;

const UNSIGNED = `      - uses: actions/download-artifact@abc123
        with:
          pattern: app-*
          merge-multiple: true
          path: dist
      - name: Attach
        run: gh release upload \${{ github.ref_name }} dist/* --clobber
`;

// A matrix of two axes: a shape this file does not model, and must say so
// about rather than reading as a single row.
const TWO_AXES = `
name: Mac
on:
  workflow_call:

jobs:
  package:
    runs-on: macos-14
    strategy:
      matrix:
        arch: [arm64, x64]
        node: [20, 22]
    steps:
      - name: Package
        run: npm run package:mac
      - uses: actions/upload-artifact@abc123
        with:
          name: app-mac-\${{ matrix.arch }}
          path: desktop/release/app-mac-\${{ matrix.arch }}.zip
`;

const world = (attachSteps: string): Read => (name) => {
  if (name === 'release.yml') return CLI_AND_MAC + attachSteps;
  if (name === 'mac.yml') return MAC;
  throw new Error(`the fixture has no workflow called ${name}`);
};

const FIXTURE_ON: Context = { ...PLAIN_TAG, 'vars.DESKTOP': 'true' };

describe('the derivation, on workflows written to exercise it', () => {
  it('finds every artifact a matrix row produces, through a reusable workflow', () => {
    const produced = report(world(BY_NAME), FIXTURE_ON).produced;
    expect(produced.map((artifact) => artifact.name)).toEqual(['cli-linux', 'app-mac-arm64', 'app-mac-x64']);
    expect(produced.filter((artifact) => artifact.desktop).map((artifact) => artifact.files.join(',')))
      .toEqual(['app-mac-arm64.zip', 'app-mac-x64.zip']);
  });

  it('produces nothing desktop when the switch is off', () => {
    const off = report(world(BY_NAME), PLAIN_TAG);
    expect(off.produced.map((artifact) => artifact.name)).toEqual(['cli-linux']);
    expect(off.desktopBuilds).toEqual([]);
    expect(off.attachers.map((attacher) => attacher.job)).toEqual(['release']);
  });

  it('names the artifact a second list forgot', () => {
    const named = report(world(BY_NAME), FIXTURE_ON);
    expect(named.unattached).toEqual(['app-mac-x64/app-mac-x64.zip']);
    expect(named.phantom).toEqual([]);
  });

  it('is satisfied by a download pattern and a glob, with nothing enumerated', () => {
    const derived = report(world(DERIVED), FIXTURE_ON);
    expect(derived.trouble || derived.unattached.join(' | ')).toBe('');
    expect(derived.unsigned).toEqual([]);
    expect(derived.phantom).toEqual([]);
    expect(derived.strayCli).toEqual([]);
  });

  it('catches the flattening flag a pattern download needs, rather than reporting it attached', () => {
    // Without merge-multiple each artifact lands in a directory named after
    // itself, `dist/*` matches nothing, and the job publishes an empty release.
    const unflattened = report(world(UNFLATTENED), FIXTURE_ON);
    expect(unflattened.unattached).toEqual(['app-mac-arm64/app-mac-arm64.zip', 'app-mac-x64/app-mac-x64.zip']);
    expect(unflattened.phantom.join(' | ')).toContain('nothing in this run stages');
  });

  it('separates "attached" from "signed"', () => {
    const unsigned = report(world(UNSIGNED), FIXTURE_ON);
    expect(unsigned.unattached).toEqual([]);
    expect(unsigned.unsigned).toEqual(['app-mac-arm64/app-mac-arm64.zip', 'app-mac-x64/app-mac-x64.zip']);
  });

  it('sees a CLI asset re-attached by a job that did not create the release', () => {
    const greedy = world(DERIVED.replace('pattern: app-*', 'pattern: \'*\''));
    expect(report(greedy, FIXTURE_ON).strayCli.join(' | ')).toContain('re-attaches dist/cli-linux');
  });

  it('reports a workflow it could not read instead of finding nothing wrong', () => {
    const twoAxes: Read = (name) => (name === 'mac.yml' ? TWO_AXES : world(DERIVED)(name));
    expect(report(twoAxes, FIXTURE_ON).trouble).toContain('does not model');
  });
});

describe('the mutation, on the same fixtures', () => {
  const third = (read: Read) => withEditedRow(read, 'mac.yml', 'arm64', 'universal');
  const without = (read: Read) => withEditedRow(read, 'mac.yml', 'arm64', null);

  it('really does add a row, and the row really is a new artifact', () => {
    const produced = report(third(world(DERIVED)), FIXTURE_ON).produced.map((artifact) => artifact.name);
    expect(produced.slice().sort()).toEqual(['app-mac-arm64', 'app-mac-universal', 'app-mac-x64', 'cli-linux']);
  });

  it('really does remove one', () => {
    const produced = report(without(world(DERIVED)), FIXTURE_ON).produced.map((artifact) => artifact.name);
    expect(produced.slice().sort()).toEqual(['app-mac-x64', 'cli-linux']);
  });

  it('fails the enumerated arrangement in both directions', () => {
    expect(report(third(world(BY_NAME)), FIXTURE_ON).unattached)
      .toEqual(['app-mac-universal/app-mac-universal.zip', 'app-mac-x64/app-mac-x64.zip']);
    expect(report(without(world(BY_NAME)), FIXTURE_ON).phantom.join(' | '))
      .toContain('app-mac-arm64.zip, which nothing in this run stages');
  });

  it('leaves the derived arrangement clean in both directions', () => {
    for (const mutant of [third(world(DERIVED)), without(world(DERIVED))]) {
      const mutated = report(mutant, FIXTURE_ON);
      expect(mutated.trouble || [...mutated.unattached, ...mutated.unsigned, ...mutated.phantom].join(' | ')).toBe('');
    }
  });
});

// ---------------------------------------------------------------------------
// The real workflows.
// ---------------------------------------------------------------------------

const readReal: Read = (name) => readFileSync(new URL(`../../.github/workflows/${name}`, import.meta.url), 'utf8');

// Whatever release.yml's own gate is spelled, this is the run where it is open.
// Derived from the file rather than named here, so the choice of switch stays
// where release-desktop-optin-workflow.test.ts left it: the implementer's.
const DESKTOP_ON: Context = {
  ...PLAIN_TAG,
  ...Object.fromEntries(Array.from(
    new Set(Array.from(readReal('release.yml').matchAll(/\bvars\.([A-Za-z_][A-Za-z_0-9]*)/g), (found) => found[1])),
    (name) => [`vars.${name}`, 'true'],
  )),
};

const enabled = report(readReal, DESKTOP_ON);
const plain = report(readReal, PLAIN_TAG);

const nothingBuilt = 'no reusable workflow that release.yml calls packages the desktop app';
const verdict = (found: Report, answer: string) =>
  found.trouble !== '' ? found.trouble : found.desktopBuilds.length === 0 ? nothingBuilt : answer;

describe('a release run attaches every desktop build it produced', () => {
  it('produces at least the builds the desktop workflows describe', () => {
    // Every rule under this one reads a list, and a list that came out empty
    // would satisfy all of them at once. This is that list, named out loud.
    expect(enabled.trouble || enabled.desktopBuilds.join(', ') || nothingBuilt).toContain('void-code-mac-arm64.zip');
    expect(enabled.desktopBuilds.length).toBeGreaterThanOrEqual(3);
  });

  it('loses none of them on the way to the release', () => {
    // The Intel bundle is built by desktop-mac-app.yml's matrix and named
    // nowhere in release.yml, so it is produced, uploaded as a run artifact,
    // and dropped.
    expect(verdict(enabled, enabled.unattached.join(' | ') || 'every desktop build is attached'))
      .toBe('every desktop build is attached');
  });

  it('signs provenance for every one it attaches, not for the ones it remembered', () => {
    expect(verdict(enabled, enabled.unsigned.join(' | ') || 'every attached desktop build is signed'))
      .toBe('every attached desktop build is signed');
  });

  it('attaches nothing it did not stage, and stages nothing it cannot name', () => {
    // `gh release upload dist/whatever` where no artifact contains `whatever`
    // fails the job; a subject-path matching nothing signs nothing and says so
    // to no one.
    expect(verdict(enabled, enabled.phantom.join(' | ') || 'attaches what it staged'))
      .toBe('attaches what it staged');
  });

  it('leaves the CLI assets to the job that created the release', () => {
    expect(verdict(enabled, enabled.strayCli.join(' | ') || 'the desktop builds only')).toBe('the desktop builds only');
  });

  it('does the attaching in a job of its own, not in the one that creates the release', () => {
    // The release must exist whether or not the desktop half ran; attaching
    // from inside the creating job would make the two one failure.
    const attaching = enabled.attachers.filter((attacher) => !attacher.creates && attacher.uploads.length > 0);
    expect(enabled.trouble || attaching.map((attacher) => attacher.job).join(', ') || 'no job attaches anything to an existing release')
      .not.toBe('no job attaches anything to an existing release');
  });
});

describe('a third architecture tomorrow needs no edit to release.yml', () => {
  // The whole point. release.yml is read unchanged; only the build workflow is
  // mutated, exactly as a real edit would.
  const macWorkflow = 'desktop-mac-app.yml';
  const third = report(withEditedRow(readReal, macWorkflow, 'arm64', 'universal'), DESKTOP_ON);
  const without = report(withEditedRow(readReal, macWorkflow, 'arm64', null), DESKTOP_ON);

  it('the mutation lands: one more artifact, named by the matrix and not by this test', () => {
    const added = third.produced.map((artifact) => artifact.name).filter((name) => !enabled.produced.some((old) => old.name === name));
    expect(third.trouble || added.join(', ')).toMatch(/universal/);
    expect(third.produced.length).toBe(enabled.produced.length + 1);
  });

  it('and the new build reaches the release anyway', () => {
    expect(verdict(third, third.unattached.join(' | ') || 'every desktop build is attached'))
      .toBe('every desktop build is attached');
  });

  it('and is signed anyway', () => {
    expect(verdict(third, third.unsigned.join(' | ') || 'every attached desktop build is signed'))
      .toBe('every attached desktop build is signed');
  });

  it('and a row that goes away leaves no path pointing at a file nobody builds', () => {
    // The other direction of the same fault: a name written twice is a name
    // that can outlive what it named. `gh release upload` on a missing file
    // fails the job, so the release loses the builds that did survive.
    expect(without.trouble || without.phantom.join(' | ') || 'nothing points at a build that is gone')
      .toBe('nothing points at a build that is gone');
  });
});

describe('and an ordinary tag still attaches nothing desktop', () => {
  it('runs no job that adds assets to a release the release job did not add', () => {
    const extra = plain.attachers.filter((attacher) => !attacher.creates && attacher.uploads.length > 0);
    expect(plain.trouble || extra.map((attacher) => attacher.job).join(', ') || 'the release job only').toBe('the release job only');
  });

  it('builds no desktop artifact to attach in the first place', () => {
    expect(plain.trouble || plain.desktopBuilds.join(', ') || 'nothing desktop is built').toBe('nothing desktop is built');
  });
});
