import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import pins from '../scripts/resource-pins.json';
import { asList, asMap, asText, parseWorkflow, type YamlMap, type YamlValue } from './workflow-yaml';
import { conditionHolds, BRANCH_EVENTS, PLAIN_TAG } from './workflow-expressions';

// A person needs a link, a download, and a working app. Today CI produces the
// CLI for six platforms and never once invokes electron-builder, so there is
// nothing to hand over. This file states the shape of the work that fixes that:
// on every branch push, build the macOS app and leave it as a run artifact.
//
// The steps that do it are written down ONCE, in the reusable workflow
// .github/workflows/desktop-mac-app.yml, and both callers reach them through
// `uses:` -- test.yml on every branch push, release.yml behind its opt-in gate.
// They were briefly written down twice, one copy in each place, which meant CI
// checked one description of packaging while a release built from another; the
// two would have drifted, and the drift would have shown up in the installer
// nobody ran CI against. So this file pins the call on the test.yml side and
// the steps on the reusable side, each exactly once.
//
// It states just as firmly what the packaging must NOT become. Publishing
// installers and cutting releases are forbidden to us, and tags are Maksim's.
// So the file holding the steps is reachable only by `workflow_call` -- no tag,
// no event of its own can start it -- and the absence of any release action, of
// `gh release`, of tag creation, and of write permission is asserted, not
// assumed. Whether release.yml's gate is really off by default is evaluated in
// release-desktop-optin-workflow.test.ts; here only its presence is pinned.
//
// What this file cannot do: it cannot run GitHub Actions. It fixes the FORM of
// the workflow -- that the job exists, where it lives, what it builds with,
// what it refuses to publish. It does not prove the build passes on a runner.
// That remains unverified here, deliberately and visibly.

// ---------------------------------------------------------------------------
// The workflow reader is shared: tests/workflow-yaml.ts. It is a reader, not a
// validator, so it is shown to read this fixture -- the shapes this file cares
// about -- correctly, before any claim is made about the real files.
// ---------------------------------------------------------------------------

const fixture = `
name: Fixture
# a whole-line comment

on:
  push:
    branches-ignore: ['main', "release"]
  pull_request:

jobs:
  build:
    runs-on: macos-14
    permissions:
      contents: read
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
  const parsed = parseWorkflow(fixture);
  const steps = asList(asMap(asMap(parsed.jobs).build).steps).map(asMap);

  it('reads triggers, including a flow sequence and a valueless key', () => {
    expect(asList(asMap(asMap(parsed.on).push)['branches-ignore'])).toEqual(['main', 'release']);
    expect(Object.keys(asMap(parsed.on))).toEqual(['push', 'pull_request']);
  });

  it('reads a job, its runner, its permissions and its step count', () => {
    expect(asText(asMap(asMap(parsed.jobs).build)['runs-on'])).toBe('macos-14');
    expect(asText(asMap(asMap(asMap(parsed.jobs).build).permissions).contents)).toBe('read');
    expect(steps).toHaveLength(3);
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
// The symlink trap.
//
// A macOS .app is a directory whose Contents/Frameworks holds symlinks --
// `Versions/Current` pointing at `Versions/A`, and the framework binaries and
// Resources linked back out of it. An artifact upload transports files; a
// directory handed to it straight cannot be relied on to carry links across as
// links, and a bundle whose links arrived as copies -- or did not arrive -- is
// not an app that launches.
//
// The fix is to stop depending on the upload's behaviour at all: pack the
// bundle first, with a tool that stores a symlink as a symlink, and upload the
// single archive file. Three tools qualify on a macOS runner, each with its own
// way of being got wrong:
//
//   ditto -c -k   creates a zip, keeps symlinks -- but WITHOUT --keepParent it
//                 zips the CONTENTS of the .app and the bundle wrapper is lost.
//   zip -r        follows symlinks and inflates copies unless given -y.
//   tar -c        stores symlinks by default, and resolves them under -h.
//
// The rule below accepts those three shapes and rejects their broken spellings.
// It is exercised against both tables before it is pointed at the real job, so
// a rule that always answered "no" could not masquerade as a passing suite.
// ---------------------------------------------------------------------------

type ArchiveVerdict = { readonly preserved: boolean; readonly reason: string };

const dereferences = /(?:^|\s)(?:-[a-zA-Z]*h[a-zA-Z]*|--dereference)(?:\s|$)/;
const storesSymlinks = /(?:^|\s)(?:-[a-zA-Z]*y[a-zA-Z]*|--symlinks)(?:\s|$)/;

function archiveVerdict(script: string): ArchiveVerdict {
  const commands = script
    .replace(/\\\n\s*/g, ' ')
    .split(/\n|&&|\|\||;/)
    .map((command) => command.trim())
    .filter((command) => command !== '');
  const packing = commands.filter((command) => /\.app\b/.test(command) && /\b(?:ditto|zip|tar|cp|rsync|mv)\b/.test(command));
  if (packing.length === 0) return { preserved: false, reason: 'nothing packs the .app bundle into an archive before the upload' };

  for (const command of packing) {
    if (/\bditto\b/.test(command)) {
      if (!/(?:^|\s)-[a-zA-Z]*c/.test(command) || !/(?:^|\s)-[a-zA-Z]*k/.test(command)) {
        return { preserved: false, reason: `ditto copies rather than archives here: ${command}` };
      }
      if (!/--keepParent\b/.test(command)) {
        return { preserved: false, reason: `ditto without --keepParent archives the contents and drops the .app wrapper: ${command}` };
      }
      return { preserved: true, reason: 'ditto -c -k --keepParent stores the bundle with its symlinks' };
    }
    if (/\btar\b/.test(command)) {
      if (dereferences.test(command)) return { preserved: false, reason: `tar resolves the symlinks into copies here: ${command}` };
      if (!/(?:^|\s)-[a-zA-Z]*c/.test(command)) return { preserved: false, reason: `tar does not create an archive here: ${command}` };
      return { preserved: true, reason: 'tar stores symlinks as symlinks' };
    }
    if (/\bzip\b/.test(command)) {
      if (!storesSymlinks.test(command)) return { preserved: false, reason: `zip follows the symlinks instead of storing them, -y is missing: ${command}` };
      return { preserved: true, reason: 'zip -y stores symlinks as symlinks' };
    }
    return { preserved: false, reason: `the bundle is copied, not archived: ${command}` };
  }
  return { preserved: false, reason: 'nothing packs the .app bundle into an archive before the upload' };
}

describe('the symlink-preserving archive rule', () => {
  const accepted: ReadonlyArray<readonly [string, string]> = [
    ['ditto with --keepParent', 'ditto -c -k --sequesterRsrc --keepParent "release/mac-arm64/Void Code.app" release/void-code-mac-arm64.zip'],
    ['ditto with bundled flags', 'ditto -ck --keepParent "release/mac-arm64/Void Code.app" out.zip'],
    ['zip -y', 'zip -y -r out.zip "Void Code.app"'],
    ['zip --symlinks', 'zip --symlinks -r out.zip "Void Code.app"'],
    ['tar without -h', 'tar -czf out.tar.gz -C release/mac-arm64 "Void Code.app"'],
  ];
  const rejected: ReadonlyArray<readonly [string, string, string]> = [
    ['nothing at all', 'npm run package:mac', 'nothing packs'],
    ['ditto without --keepParent', 'ditto -c -k "release/mac-arm64/Void Code.app" out.zip', '--keepParent'],
    ['ditto as a plain copy', 'ditto "release/mac-arm64/Void Code.app" staging/Void Code.app', 'copies rather than archives'],
    ['zip without -y', 'zip -r out.zip "Void Code.app"', '-y is missing'],
    ['tar told to dereference', 'tar -chzf out.tar.gz -C release/mac-arm64 "Void Code.app"', 'resolves the symlinks'],
    ['a copy into an upload directory', 'cp -R "release/mac-arm64/Void Code.app" upload/', 'copied, not archived'],
  ];

  it.each(accepted)('accepts %s', (_name, command) => {
    expect(archiveVerdict(command).preserved).toBe(true);
  });

  it.each(rejected)('rejects %s', (_name, command, reason) => {
    const verdict = archiveVerdict(command);
    expect(verdict.preserved).toBe(false);
    expect(verdict.reason).toContain(reason);
  });
});

// ---------------------------------------------------------------------------
// The real workflows.
//
// There is one description of macOS packaging and it lives in
// .github/workflows/desktop-mac-app.yml. test.yml and release.yml both reach it
// through `uses:`; neither repeats its steps. That is the whole point of the
// arrangement: the steps CI exercises on every branch push are, byte for byte,
// the steps a release builds with, because they are the same seven lines in the
// same file. Two copies would be free to drift, and the copy that drifts is
// always the one nobody runs.
//
// So the steps below are asserted once, against the reusable workflow, and
// test.yml is asserted to CALL it rather than to contain it.
// ---------------------------------------------------------------------------

const workflowText = (name: string) => readFileSync(new URL(`../../.github/workflows/${name}`, import.meta.url), 'utf8');
const workflow = (name: string) => parseWorkflow(workflowText(name));

const REUSABLE = 'desktop-mac-app.yml';
const CALL = `./.github/workflows/${REUSABLE}`;

const testWorkflow = workflow('test.yml');
const reusableWorkflow = workflow(REUSABLE);

const stepsOf = (job: YamlValue) => asList(asMap(job).steps).map(asMap);
const packagesTheApp = (job: YamlValue) => stepsOf(job).some((step) => /\bpackage:mac\b/.test(asText(step.run)));
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
  if (packagesTheApp(job)) return true;
  const called = localCall(job);
  return called !== '' && Object.values(asMap(workflow(called).jobs)).some(packagesTheApp);
};

const namesOf = (workflowFile: YamlMap, predicate: (job: YamlValue) => boolean) =>
  Object.entries(asMap(workflowFile.jobs)).filter(([, job]) => predicate(job)).map(([name]) => name);

// ---------------------------------------------------------------------------
// The reusable workflow: the only place the packaging steps are written down.
// ---------------------------------------------------------------------------

const packagingJobs = Object.entries(asMap(reusableWorkflow.jobs)).filter(([, job]) => packagesTheApp(job));

const missing = `no job in .github/workflows/${REUSABLE} runs \`npm run package:mac\``;
const found = packagingJobs.length === 1;
const packagingJob = asMap(packagingJobs[0]?.[1]);
const steps = asList(packagingJob.steps).map(asMap);
// Step order matters twice over, and both orders read off one transcript.
const transcript = steps.map((step) => `${asText(step.uses)}\n${asText(step.run)}`).join('\n \n');
const uploadIndex = steps.findIndex((step) => /actions\/upload-artifact/.test(asText(step.uses)));
const uploadStep = asMap(steps[uploadIndex]?.with);
const reason = (verdict: string) => (found ? verdict : missing);

// A reusable workflow may declare permissions on the job or on the file; the
// file-level block applies to every job in it, so either satisfies the rule and
// neither being present does not.
const effectivePermissions = Object.keys(asMap(packagingJob.permissions)).length > 0
  ? asMap(packagingJob.permissions)
  : asMap(reusableWorkflow.permissions);

describe(`${REUSABLE} builds the macOS app and leaves it as a run artifact`, () => {
  it('has exactly one job that packages the desktop app', () => {
    expect(packagingJobs.map(([name]) => name).join(', ') || missing).not.toBe(missing);
    expect(packagingJobs).toHaveLength(1);
  });

  it('builds on the macOS runner the smoke job already uses', () => {
    expect(reason(asText(packagingJob['runs-on']))).toBe('macos-14');
  });

  it('checks out the repository and installs Go, because the assembly shells out to `go build`', () => {
    // scripts/assemble-resources.mjs:55 runs `go build ./cmd/vc`; without a
    // toolchain the packaging step dies inside `npm run assemble`.
    const uses = steps.map((step) => asText(step.uses));
    expect(reason(uses.some((step) => step.startsWith('actions/checkout@')) ? 'checked out' : 'no actions/checkout step')).toBe('checked out');
    const go = asMap(steps.find((step) => asText(step.uses).startsWith('actions/setup-go@'))?.with);
    expect(reason(asText(go['go-version-file']) || 'no actions/setup-go step')).toBe('.go-version');
  });

  it('installs the Node version that resource-pins.json pins the runtime to', () => {
    // The pin moves; a literal here would rot silently. The workflow has to
    // follow the pin, not a copy of today's value.
    const node = asMap(steps.find((step) => asText(step.uses).startsWith('actions/setup-node@'))?.with);
    expect(reason(asText(node['node-version']) || 'no actions/setup-node step')).toBe(pins.node.version.replace(/^v/, ''));
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

  it('provisions the pinned Pi runtime before it packages', () => {
    const provision = transcript.indexOf('provision-pinned-pi-smoke.sh');
    const packaged = transcript.indexOf('package:mac');
    expect(reason(provision < 0 ? 'no provision-pinned-pi-smoke.sh step' : provision < packaged ? 'provisioned first' : 'packaged before provisioning'))
      .toBe('provisioned first');
  });

  it('packages through `npm run package:mac`, never electron-builder by hand', () => {
    // package:mac is build + assemble + electron-builder --dir --mac --arm64.
    // Calling electron-builder directly skips the assembly, and the private
    // runtime the app needs is exactly what the assembly stages.
    const scripts = steps.map((step) => asText(step.run));
    const byHand = scripts.filter((script) => /(?:^|[\s/])electron-builder\s/.test(script));
    expect(reason(byHand.join(' | ') || 'through the npm script')).toBe('through the npm script');
    expect(reason(scripts.some((script) => /npm\s+run\s+(?:--\S+\s+)*package:mac\b/.test(script)) ? 'through the npm script' : 'no `npm run package:mac`'))
      .toBe('through the npm script');
  });

  it('runs the packaging step inside desktop/, where that script lives', () => {
    const step = steps.find((candidate) => /\bpackage:mac\b/.test(asText(candidate.run)));
    // The word "desktop" appearing somewhere in the script is not the same as
    // running there: the step has to declare the directory, cd into it, or hand
    // it to npm.
    const directory = asText(asMap(step)['working-directory']).trim();
    const script = asText(asMap(step).run);
    const located = directory === 'desktop'
      || /\bcd\s+desktop\b/.test(script)
      || /\bnpm\b[^\n]*\s(?:--prefix|-C)\s+desktop\b/.test(script);
    expect(reason(located ? 'in desktop/' : 'runs from the repository root, where package:mac does not exist')).toBe('in desktop/');
  });

  it('archives the bundle with its symlinks intact before uploading it', () => {
    const verdict = archiveVerdict(steps.map((step) => asText(step.run)).join('\n'));
    expect(reason(verdict.preserved ? 'symlinks preserved' : verdict.reason)).toBe('symlinks preserved');
  });

  it('archives first and uploads second', () => {
    const archived = steps.findIndex((step) => archiveVerdict(asText(step.run)).preserved);
    expect(reason(uploadIndex < 0 ? 'no actions/upload-artifact step' : archived < 0 ? 'no archiving step' : archived < uploadIndex ? 'archive, then upload' : 'uploads before archiving'))
      .toBe('archive, then upload');
  });

  it('uploads the archive file and not the bundle directory', () => {
    const path = asText(uploadStep.path);
    const verdict = path === '' ? 'no upload path'
      : /\.app\b(?!\.(?:zip|tar\.gz|tgz))/.test(path) ? `uploads the bundle directory: ${path}`
        : /\.(?:zip|tar\.gz|tgz)\b/.test(path) ? 'uploads an archive'
          : `uploads something that is not an archive: ${path}`;
    expect(reason(verdict)).toBe('uploads an archive');
  });

  it('fails the run rather than uploading nothing when the archive is not there', () => {
    // Without this, a renamed output path turns into a green run with an empty
    // artifact -- the failure mode that wastes a day before anyone notices.
    expect(reason(asText(uploadStep['if-no-files-found']) || 'if-no-files-found is unset, so a missing archive only warns')).toBe('error');
  });

  it('names the artifact so a person can tell what they are downloading', () => {
    const name = asText(uploadStep.name);
    expect(reason(/mac/i.test(name) ? 'named for macOS' : `artifact name does not say what platform it is: ${name || '(unset)'}`)).toBe('named for macOS');
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
const inlinePackagers = namesOf(testWorkflow, packagesTheApp);
const callingJob = asMap(Object.entries(asMap(testWorkflow.jobs)).find(([, job]) => callsTheReusableWorkflow(job))?.[1]);

describe('test.yml checks the same description of packaging that release.yml builds from', () => {
  it(`reaches macOS packaging by calling ${REUSABLE}`, () => {
    expect(callingJobs.join(', ') || `no job in .github/workflows/test.yml has \`uses: ${CALL}\``)
      .not.toBe(`no job in .github/workflows/test.yml has \`uses: ${CALL}\``);
    expect(callingJobs).toHaveLength(1);
  });

  it('does not repeat the packaging steps inline as well', () => {
    // The defect this replaces: seven steps in test.yml and the same seven in
    // the reusable workflow, so CI checked one description and the release
    // built from the other.
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
    // A file that packages must not also be a file a tag can start. Its only
    // trigger is workflow_call, so the decision to run it is always taken in
    // test.yml or release.yml, where the gates are visible.
    expect(Object.keys(asMap(reusableWorkflow.on))).toEqual(['workflow_call']);
  });
});

describe('the artifact job publishes nothing', () => {
  // Release, installer publication and tag creation are forbidden to us without
  // a separate gate. The job may leave a build behind for the run; it may not
  // put anything where a stranger can reach it.
  const forbidden: ReadonlyArray<readonly [string, RegExp]> = [
    ['a GitHub release action', /softprops\/action-gh-release|ncipollo\/release-action|actions\/create-release/],
    ['`gh release`', /\bgh\s+release\b/],
    ['a release call through the API', /\bgh\s+api\b[^\n]*releases/],
    ['tag creation', /\bgit\s+tag\b|\brefs\/tags\//],
    ['a push of tags', /\bgit\s+push\b[^\n]*(?:--tags|--follow-tags|tag)/],
    ['a package publish', /\bnpm\s+publish\b|\bdocker\s+push\b/],
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

describe('packaging stays out of the tag-triggered workflows', () => {
  it('test.yml fires on branch pushes and pull requests, never on a tag', () => {
    const triggers = asMap(testWorkflow.on);
    expect(Object.keys(triggers).sort()).toEqual(['pull_request', 'push']);
    expect(asMap(triggers.push).tags).toBeUndefined();
    expect(asList(asMap(triggers.push)['branches-ignore'])).toContain('main');
  });

  it('canary-release.yml stays tag-triggered and reaches no desktop packaging at all', () => {
    const canary = workflow('canary-release.yml');
    expect(asList(asMap(asMap(canary.on).push).tags).length).toBeGreaterThan(0);
    expect(namesOf(canary, reachesPackaging)).toEqual([]);
  });

  it('release.yml stays tag-triggered, and every route it has to packaging is off with nothing set', () => {
    // What it may NOT have is packaging steps of its own -- those would be a
    // third description, unreachable from any branch push. What it may have is
    // a call, and every call has to carry a condition. Whether that condition
    // is really off by default is EVALUATED next door, in
    // release-desktop-optin-workflow.test.ts; only its presence is pinned here.
    const released = workflow('release.yml');
    expect(asList(asMap(asMap(released.on).push).tags).length).toBeGreaterThan(0);
    expect(namesOf(released, packagesTheApp)).toEqual([]);
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
