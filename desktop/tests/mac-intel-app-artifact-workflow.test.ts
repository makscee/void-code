import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import packageJson from '../package.json';
import { asList, asMap, asText, parseWorkflow, type YamlMap, type YamlValue } from './workflow-yaml';

// desktop-mac-app.yml builds one bundle, for Apple silicon, and calls it
// `void-code-mac-arm64`. Somebody on an Intel Mac has nothing to download.
//
// The shape this file states is a matrix over architecture inside the one
// packaging job that already exists -- not a second job, and not a second
// reusable workflow. That choice is the same one the pins make next door in
// mac-intel-resource-pins.test.ts: the architecture is a VALUE the description
// is parameterised by, so a third one is a row rather than a rewrite. It also
// costs nothing already paid for: everything
// mac-app-artifact-workflow.test.ts pins about that job -- read-only
// permissions, no release action, no tag, `workflow_call` only, packaging
// through the npm script, an archive that keeps its symlinks, an upload that
// fails rather than uploading nothing -- goes on being pinned about it, once,
// for every architecture in the matrix. A second job would have needed every
// one of those rules restated, and restated rules drift.
//
// The one thing a matrix cannot make safe by itself is the path. electron-
// builder does not name the output directory after the architecture; it names
// it after the DIFFERENCE from the default architecture, so arm64 lands in
// `release/mac-arm64` and x64 lands in `release/mac` with no suffix at all. Type
// `release/mac-x64` into the archive step and the job goes red at best, uploads
// the wrong bundle at worst. So the directory is derived here from the build
// config, the way windows-app-artifact-workflow.test.ts derives the installer
// name, and the workflow is checked against the derivation.
//
// The runner stays macos-14, the Apple silicon image every other desktop job in
// this repository already uses. Whether GitHub still offers an Intel image is
// not something this file can check, and nothing here needs one: the Intel
// bundle is cross-built on the runner that is already paid for. Everything that
// follows from cross-building -- that no step may depend on RUNNING what it just
// produced -- is pinned in mac-intel-resource-pins.test.ts.
//
// What this file cannot do: it cannot run GitHub Actions, and it cannot start a
// bundle on an Intel Mac. It fixes the FORM -- what is built, from what, under
// what name. That the Intel bundle launches is unverified here, deliberately and
// visibly.

// ---------------------------------------------------------------------------
// The workflow reader is shared: tests/workflow-yaml.ts. It is a reader, not a
// validator, so it is shown to read this fixture -- a matrix job, which is the
// shape this file cares about and which no other test file exercises --
// correctly, before any claim is made about the real files.
// ---------------------------------------------------------------------------

const fixture = `
name: Fixture

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
            bundle: mac-arm64
          - arch: x64
            bundle: mac
    steps:
      - name: Package
        env:
          VOID_DESKTOP_MAC_ARCH: \${{ matrix.arch }}
        run: npm run package:mac
      - uses: actions/upload-artifact@abc123
        with:
          name: void-code-mac-\${{ matrix.arch }}
          path: desktop/release/void-code-mac-\${{ matrix.arch }}.zip
`;

describe('the workflow reader', () => {
  const parsed = parseWorkflow(fixture);
  const job = asMap(asMap(parsed.jobs).package);
  const matrix = asMap(asMap(job.strategy).matrix);

  it('reads a matrix given as a list of includes', () => {
    expect(asList(matrix.include).map(asMap).map((row) => asText(row.arch))).toEqual(['arm64', 'x64']);
    expect(asList(matrix.include).map(asMap).map((row) => asText(row.bundle))).toEqual(['mac-arm64', 'mac']);
  });

  it('reads a scalar that sits beside the matrix', () => {
    expect(asText(matrix['fail-fast']) || asText(asMap(job.strategy)['fail-fast'])).toBe('false');
  });

  it('reads a step env block and leaves the matrix expression in it alone', () => {
    const steps = asList(job.steps).map(asMap);
    expect(asText(asMap(steps[0].env).VOID_DESKTOP_MAC_ARCH)).toBe('${{ matrix.arch }}');
    expect(asText(asMap(steps[1].with).path)).toBe('desktop/release/void-code-mac-${{ matrix.arch }}.zip');
  });
});

// ---------------------------------------------------------------------------
// Where the bundle lands, derived rather than guessed.
//
// electron-builder computes the unpacked directory as the platform key plus a
// suffix, and the suffix is empty when the architecture IS the default one
// (platformPackager's getArchSuffix, whose default default is x64). So today's
// arm64 build writes `release/mac-arm64`, and an x64 build writes `release/mac`
// -- no `-x64` anywhere. If the config ever sets `build.mac.defaultArch`, the
// two swap over, which is why the derivation reads the config instead of
// carrying a table of today's answers.
// ---------------------------------------------------------------------------

type BuildConfig = {
  version?: string;
  scripts?: Record<string, string>;
  build?: {
    directories?: { output?: string };
    productName?: string;
    mac?: { defaultArch?: string; [key: string]: unknown };
    [key: string]: unknown;
  };
};

const MAC_ARCHITECTURES = ['x64', 'arm64', 'universal'];

function macBundlePath(config: BuildConfig, arch: string): string {
  if (!MAC_ARCHITECTURES.includes(arch)) throw new Error(`electron-builder builds no macOS architecture called ${arch}`);
  const build = config.build ?? {};
  const productName = build.productName;
  if (productName === undefined) throw new Error('package.json build declares no productName, so the bundle has no name');
  const defaultArch = build.mac?.defaultArch ?? 'x64';
  const suffix = arch === defaultArch ? '' : `-${arch}`;
  return `${build.directories?.output ?? 'dist'}/mac${suffix}/${productName}.app`;
}

describe('the bundle-path derivation', () => {
  const synthetic: BuildConfig = { build: { directories: { output: 'out' }, productName: 'Some Thing' } };

  it('gives the default architecture no suffix and every other one its own', () => {
    expect(macBundlePath(synthetic, 'x64')).toBe('out/mac/Some Thing.app');
    expect(macBundlePath(synthetic, 'arm64')).toBe('out/mac-arm64/Some Thing.app');
    expect(macBundlePath(synthetic, 'universal')).toBe('out/mac-universal/Some Thing.app');
  });

  it('follows a configured default architecture, so the suffix moves with it', () => {
    const flipped: BuildConfig = { build: { ...synthetic.build, mac: { defaultArch: 'arm64' } } };
    expect(macBundlePath(flipped, 'arm64')).toBe('out/mac/Some Thing.app');
    expect(macBundlePath(flipped, 'x64')).toBe('out/mac-x64/Some Thing.app');
  });

  const refusals: ReadonlyArray<readonly [string, BuildConfig, string, string]> = [
    ['an architecture electron-builder has no macOS target for', synthetic, 'ppc64', 'no macOS architecture'],
    ['a config with no product name', { build: { directories: { output: 'out' } } }, 'x64', 'no productName'],
  ];

  it.each(refusals)('refuses %s rather than inventing a path', (_name, config, arch, reason) => {
    expect(() => macBundlePath(config, arch)).toThrow(reason);
  });
});

describe('the derivation agrees with the arm64 build that exists today', () => {
  it('names the directory the current workflow already archives', () => {
    // An independent witness: the path a human typed into desktop-mac-app.yml
    // when there was one architecture, and which is known to have produced a
    // real bundle. If the derivation disagrees with it, the derivation is wrong
    // and every path below it is wrong too.
    expect(macBundlePath(packageJson as BuildConfig, 'arm64')).toBe('release/mac-arm64/Void Code.app');
  });
});

// ---------------------------------------------------------------------------
// The real workflow.
// ---------------------------------------------------------------------------

const REUSABLE = 'desktop-mac-app.yml';
const workflowText = (name: string) => readFileSync(new URL(`../../.github/workflows/${name}`, import.meta.url), 'utf8');
const reusableWorkflow = parseWorkflow(workflowText(REUSABLE));

const stepsOf = (job: YamlValue) => asList(asMap(job).steps).map(asMap);
const packagesTheApp = (job: YamlValue) => stepsOf(job).some((step) => /\bpackage:mac\b/.test(asText(step.run)));

const packagingJobs = Object.entries(asMap(reusableWorkflow.jobs)).filter(([, job]) => packagesTheApp(job));
const missing = `no job in .github/workflows/${REUSABLE} runs \`npm run package:mac\``;
const found = packagingJobs.length === 1;
const packagingJob = asMap(packagingJobs[0]?.[1]);
const steps = asList(packagingJob.steps).map(asMap);
const reason = (verdict: string) => (found ? verdict : missing);

// A matrix is either a list of includes or a set of axes. Both are read; a
// single-axis matrix becomes one row per value, which is the spelling a job that
// needs nothing but the architecture would use.
function matrixRows(job: YamlMap): YamlMap[] {
  const matrix = asMap(asMap(job.strategy).matrix);
  const included = asList(matrix.include).map(asMap);
  const axes = Object.entries(matrix).filter(([key]) => key !== 'include' && key !== 'exclude');
  if (axes.length === 1) {
    const [name, values] = axes[0];
    const rows = asList(values).map((value) => ({ [name]: asText(value) }));
    return [...rows, ...included];
  }
  return included;
}

// `${{ matrix.x }}` is the only expression this file resolves. Anything else --
// a `vars.`, a `github.`, a function call -- is left in place, and shows up in
// the assertion that reads the substituted text, which is where it belongs: an
// architecture worked out at run time is an architecture nothing here can check.
function substitute(text: string, row: YamlMap): string {
  return text.replace(/\$\{\{\s*matrix\.([A-Za-z0-9_-]+)\s*\}\}/g, (whole, key: string) => {
    const value = row[key];
    return typeof value === 'string' ? value : whole;
  });
}

const stepText = (step: YamlMap, row: YamlMap) => substitute([
  asText(step.run),
  ...Object.values(asMap(step.env)).map(asText),
  ...Object.values(asMap(step.with)).map(asText),
].join('\n'), row);

// The architecture a row builds: the value that IS an architecture, not a value
// that merely contains one -- `mac-arm64` is a directory, `arm64` is the answer.
function archOf(row: YamlMap): string {
  const named = Object.values(row).map(asText).filter((value) => MAC_ARCHITECTURES.includes(value));
  return named.length === 1 ? named[0] : '';
}

const rows = found ? matrixRows(packagingJob) : [];
const architectures = rows.map(archOf);

// Every rule below that reads the matrix row by row says nothing at all when
// there are no rows: `[].every(...)` is true, `[].filter(...)` is empty, and a
// suite of them would go green against the single-architecture job this file
// exists to replace. So a missing matrix is a finding in its own right, and it
// is the finding each of those rules reports.
const perRow = (verdict: string) => (found ? (rows.length === 0 ? `the packaging job in ${REUSABLE} has no architecture matrix` : verdict) : missing);

describe(`${REUSABLE} builds the app for both Mac architectures`, () => {
  it('still describes packaging in exactly one job, now parameterised', () => {
    // A second job would be a second description of everything the first one
    // already says: the runner, the checkout, the toolchains, the read-only
    // token, the refusal to publish. The matrix is what keeps that at one copy.
    expect(packagingJobs.map(([name]) => name).join(', ') || missing).not.toBe(missing);
    expect(packagingJobs).toHaveLength(1);
  });

  it('lists both architectures, each row saying which one it is', () => {
    expect(perRow(architectures.includes('') ? `a matrix row names no architecture: ${JSON.stringify(rows[architectures.indexOf('')] ?? {})}`
      : architectures.join(', ')))
      .toBe('arm64, x64');
  });

  it('builds on the Apple silicon runner it already uses, so Intel is cross-built', () => {
    // The same image the job uses today, and the same one test.yml's pinned Pi
    // smoke job uses. This line is why nothing in the build may depend on
    // executing what it produced -- see mac-intel-resource-pins.test.ts.
    expect(reason(asText(packagingJob['runs-on']))).toBe('macos-14');
  });

  it('does not stop the other architecture when one of them fails', () => {
    // Both bundles are wanted. With fail-fast at its default the first red row
    // cancels the second, and a person chasing an Intel-only break loses the
    // arm64 evidence that would have told them whether it is Intel-only.
    const failFast = asText(asMap(packagingJob.strategy)['fail-fast']);
    expect(reason(failFast || 'fail-fast is unset, so one red architecture cancels the other')).toBe('false');
  });

  it('tells the packaging step which architecture the row is for', () => {
    // The single failure this catches is the one a matrix invites: rows that
    // differ in name only, every one of them running the same arm64 build, and
    // two identical bundles uploaded under two names.
    const packaging = steps.find((step) => /\bpackage:mac\b/.test(asText(step.run)));
    const seen = rows.map((row) => ({ arch: archOf(row), text: stepText(asMap(packaging), row) }));
    const blind = seen.filter((row) => !row.text.includes(row.arch)).map((row) => row.arch);
    expect(perRow(packaging === undefined ? 'no `npm run package:mac` step'
      : blind.length > 0 ? `the packaging step says nothing about ${blind.join(', ')}; every row would build the same thing`
        : 'each row names its architecture'))
      .toBe('each row names its architecture');
    expect(perRow(new Set(seen.map((row) => row.text)).size === rows.length ? 'the rows differ' : 'two rows run the identical command'))
      .toBe('the rows differ');
  });

  it('provisions the runtime for the architecture the row builds, not for the runner', () => {
    // The pinned Node archive the assembly stages is architecture-specific and
    // is fetched before packaging. A provisioning step that ignores the matrix
    // fetches the arm64 archive twice and the Intel row dies looking for an
    // archive nobody downloaded.
    const packagingIndex = steps.findIndex((step) => /\bpackage:mac\b/.test(asText(step.run)));
    const before = steps.slice(0, packagingIndex < 0 ? 0 : packagingIndex);
    const blind = rows.filter((row) => !before.some((step) => stepText(step, row).includes(archOf(row)))).map(archOf);
    expect(perRow(packagingIndex < 0 ? 'no `npm run package:mac` step'
      : blind.length > 0 ? `nothing before packaging is told the architecture for ${blind.join(', ')}`
        : 'provisioned per architecture'))
      .toBe('provisioned per architecture');
  });
});

describe('each architecture is archived and uploaded as its own artifact', () => {
  const archiveStep = steps.find((step) => /\bditto\b|\btar\b|\bzip\b/.test(asText(step.run)));
  const uploadStep = steps.find((step) => /actions\/upload-artifact/.test(asText(step.uses)));

  it('archives the bundle directory electron-builder writes for that architecture', () => {
    // Derived, never typed. `release/mac-x64` is the spelling everyone reaches
    // for and electron-builder writes no such directory.
    const wrong = rows.map((row) => {
      const arch = archOf(row);
      const script = substitute(asText(asMap(archiveStep).run), row);
      const expected = macBundlePath(packageJson as BuildConfig, arch);
      return script.includes(expected) ? '' : `${arch}: archives ${script.trim() || '(nothing)'}, where the build config derives ${expected}`;
    }).filter(Boolean);
    expect(perRow(archiveStep === undefined ? 'no step archives the bundle' : wrong.join(' | ') || 'each row archives its own bundle'))
      .toBe('each row archives its own bundle');
  });

  it('does not archive the arm64 bundle while calling it Intel', () => {
    // The specific accident: a path left literal, so both rows pack
    // release/mac-arm64 and the Intel artifact is an Apple silicon app.
    const intel = rows.filter((row) => archOf(row) === 'x64');
    expect(perRow(intel.length === 1 ? 'one Intel row' : `${intel.length} rows build Intel`)).toBe('one Intel row');
    const crossed = intel.filter((row) => /mac-arm64/.test(substitute(asText(asMap(archiveStep).run), row)));
    expect(perRow(crossed.length > 0 ? 'the Intel row still names release/mac-arm64' : 'no row packs another row\'s bundle'))
      .toBe('no row packs another row\'s bundle');
  });

  it('uploads one artifact per architecture, under names that differ', () => {
    const names = rows.map((row) => substitute(asText(asMap(asMap(uploadStep).with).name), row));
    expect(perRow(uploadStep === undefined ? 'no actions/upload-artifact step'
      : new Set(names).size === rows.length ? 'one name per architecture'
        : `two architectures share the artifact name ${names.join(', ')}`))
      .toBe('one name per architecture');
    const silent = rows.filter((row) => !substitute(asText(asMap(asMap(uploadStep).with).name), row).includes(archOf(row))).map(archOf);
    expect(perRow(silent.length > 0 ? `the artifact name does not say ${silent.join(', ')}` : 'each name says its architecture'))
      .toBe('each name says its architecture');
  });

  it('leaves the Apple silicon artifact called exactly what it is called today', () => {
    // release.yml's desktop-attach downloads `void-code-mac-arm64` by name, and
    // anyone who has already been handed a link expects that file. Adding an
    // architecture is adding one; it is not renaming the other.
    const arm64 = rows.find((row) => archOf(row) === 'arm64') ?? {};
    const upload = asMap(asMap(uploadStep).with);
    expect(reason(substitute(asText(upload.name), arm64) || 'no artifact name')).toBe('void-code-mac-arm64');
    expect(reason(substitute(asText(upload.path), arm64) || 'no artifact path')).toBe('desktop/release/void-code-mac-arm64.zip');
  });

  it('uploads the archive each row actually wrote', () => {
    const wrong = rows.map((row) => {
      const arch = archOf(row);
      const written = substitute(asText(asMap(archiveStep).run), row);
      const uploaded = substitute(asText(asMap(asMap(uploadStep).with).path), row);
      const file = uploaded.replace(/^desktop\//, '');
      return written.includes(file) ? '' : `${arch}: uploads ${uploaded}, which no command in the archive step writes`;
    }).filter(Boolean);
    expect(perRow(uploadStep === undefined || archiveStep === undefined ? 'no archive step, no upload step, or neither'
      : wrong.join(' | ') || 'each row uploads what it archived'))
      .toBe('each row uploads what it archived');
  });

  it('still fails the run rather than uploading nothing', () => {
    expect(reason(asText(asMap(asMap(uploadStep).with)['if-no-files-found']) || 'if-no-files-found is unset, so a missing archive only warns')).toBe('error');
  });
});

// ---------------------------------------------------------------------------
// The npm side.
//
// Whatever the workflow says, `npm run package:mac` typed by a person with
// nothing set has to go on producing the arm64 bundle: it is what `npm run
// check`, `check:mac-pty`, `check:mac-tabs` and `check:production-terminal` all
// run, and what every local smoke script expects to find in release/mac-arm64.
// Adding an architecture is not moving the default.
// ---------------------------------------------------------------------------

describe('desktop/package.json', () => {
  const scripts = (packageJson as BuildConfig).scripts ?? {};

  // `package:mac` may delegate; what matters is the command that eventually
  // reaches electron-builder.
  function resolved(name: string, seen = new Set<string>()): string {
    if (seen.has(name)) return '';
    seen.add(name);
    const script = scripts[name] ?? '';
    return script.replace(/npm\s+run\s+([A-Za-z0-9:_-]+)/g, (whole, referenced: string) => resolved(referenced, seen) || whole);
  }

  it('still packages Apple silicon when nobody says otherwise', () => {
    const script = resolved('package:mac');
    expect(script === '' ? 'there is no package:mac script' : /\barm64\b/.test(script) ? 'arm64' : `names no default architecture: ${script}`).toBe('arm64');
    expect(/\bx64\b/.test(script) ? `package:mac also names x64: ${script}` : 'only arm64').toBe('only arm64');
  });

  it('does not build both architectures in one invocation', () => {
    // `electron-builder --mac --arm64 --x64` is a legal command that produces
    // two bundles and takes twice as long, and it is what appending a flag to
    // the existing script would quietly turn it into.
    const doubled = Object.entries(scripts)
      .filter(([name]) => /^package:mac/.test(name))
      .filter(([name]) => /\barm64\b/.test(resolved(name)) && /\bx64\b/.test(resolved(name)))
      .map(([name]) => name);
    expect(doubled.join(', ') || 'one architecture per invocation').toBe('one architecture per invocation');
  });
});
