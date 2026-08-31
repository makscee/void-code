import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import pins from '../scripts/resource-pins.json';
import { assemblyTarget, vcBuildPlan } from '../scripts/resource-assembly-lib.mjs';
import { asList, asMap, asText, parseWorkflow, type YamlMap } from './workflow-yaml';

// Two assemblies stage the same binary, and only one of them can be wrong about
// which binary it is.
//
//   scripts/assemble-resources.mjs      (macOS)
//     go build -trimpath -buildvcs=false -o <staging>/vc/bin/vc ./cmd/vc
//
//   scripts/assemble-windows-resources.mjs
//     cp runtime/cache/vc/vc.exe  <staging>/vc/vc.exe
//
// The Mac bundle's vc IS the tree being packaged: there is no step at which the
// two could differ, and none at which anyone has to remember to keep them
// together. The Windows bundle's vc is a file the workflow DOWNLOADED from a
// past GitHub release, named by resource-pins.json, and the only thing holding
// it level with the app around it is that somebody bumps a pin. Nobody did.
// v0.2.48 shipped v0.2.47 inside it, the desktop spawned `vc login --json`, and
// every Windows user got `unknown flag: --json` instead of a sign-in.
//
// That is not a stale pin. A pin whose staleness breaks the product on every
// release is a structure that produces the defect on a schedule -- so the fix
// is not a fresher pin, it is the same construction the Mac side already has:
// the Windows bundle builds vc from the tree it is packaging, and the concept
// of a downloadable vc pin stops existing.
//
// This file states that as four claims, in the order a reader would check them:
// the pin is gone, the assembly reads no prebuilt binary, the assembly builds
// one, and the workflow provisions a toolchain instead of a download. The Node
// pin is untouched and is asserted to be untouched -- Node is a third-party
// distribution nobody here builds, and it is the pinning mechanism working as
// intended.
//
// What this file cannot do: it cannot run the Windows assembly. The script's
// first line refuses to start anywhere but Windows x64, so on this machine and
// on their Linux and macOS runners there is nothing to execute. What is checked
// is what a reader can check: the constants, the imports, and the file the
// script no longer reaches for. The behaviour of the assembled bundle remains
// unverified here, deliberately and visibly, and the argv contract the incident
// actually broke is measured for real in
// tests/vc-understands-desktop-arguments.test.ts.

const desktop = path.resolve('.');
const windowsAssembly = readFileSync(path.join(desktop, 'scripts/assemble-windows-resources.mjs'), 'utf8');
const macAssembly = readFileSync(path.join(desktop, 'scripts/assemble-resources.mjs'), 'utf8');

// Comments describe; code does. A rule read off a file has to look at the half
// that runs, or a sentence explaining the old arrangement keeps it red forever
// after it is gone.
const codeOf = (source: string) => source.split('\n').filter((line) => !/^\s*(?:\/\/|\*|\/\*)/.test(line)).join('\n');
const windowsCode = codeOf(windowsAssembly);

// ---------------------------------------------------------------------------
// 1. resource-pins.json pins no vc for anyone.
//
// The pin is the mechanism, so the mechanism is what goes -- not today's value
// inside it. Bumping `releaseTag` to v0.2.48 would make the product work this
// week and break it again at the next release; the assertion below is written
// so that such a bump does not satisfy it.
// ---------------------------------------------------------------------------

describe('resource-pins.json pins no downloadable vc', () => {
  it('has no vc entry under the Windows pins', () => {
    expect((pins.windows as Record<string, unknown>).vc).toBeUndefined();
  });

  it('names no release asset anywhere in the file, for any platform', () => {
    // Discovered rather than listed: a vc pin re-added under `platforms` or
    // under a new architecture is the same defect wearing a different key, and
    // it has to be red the day it lands.
    const found: string[] = [];
    const walk = (value: unknown, trail: string) => {
      if (Array.isArray(value)) { value.forEach((item, index) => walk(item, `${trail}[${index}]`)); return; }
      if (value === null || typeof value !== 'object') return;
      const record = value as Record<string, unknown>;
      for (const key of ['assetName', 'releaseTag', 'releaseCommit', 'cliSourceCommit']) {
        if (key in record) found.push(`${trail}.${key}`);
      }
      if (record.provenance === 'github-release') found.push(`${trail}.provenance`);
      for (const [key, item] of Object.entries(record)) walk(item, `${trail}.${key}`);
    };
    walk(pins, 'pins');
    expect(found.join(', ') || 'nothing in resource-pins.json points at a release').toBe('nothing in resource-pins.json points at a release');
  });

  it('still pins the Windows Node distribution, which nobody here builds', () => {
    // The pin mechanism is right for a third-party distribution downloaded from
    // nodejs.org. Deleting it along with the vc pin would trade one defect for
    // a worse one, so it is asserted present rather than merely left alone.
    expect(pins.windows.node.version).toMatch(/^v\d+\.\d+\.\d+$/);
    expect(pins.windows.node.sourceArchiveSha256).toMatch(/^[0-9a-f]{64}$/);
    expect(pins.platforms['win32-x64'].node.sourceArchiveSha256).toBe(pins.windows.node.sourceArchiveSha256);
  });
});

// ---------------------------------------------------------------------------
// 2 and 3. The Windows assembly builds vc; it does not fetch one.
// ---------------------------------------------------------------------------

describe('scripts/assemble-windows-resources.mjs stages a vc it built', () => {
  it('reads no prebuilt vc from the runtime cache', () => {
    // runtime/cache/vc/vc.exe is the file the workflow downloads. Nothing in
    // the repository creates it, and after this change nothing may expect it.
    const reaching = windowsCode.split('\n').filter((line) => /runtime\/cache\/vc/.test(line));
    expect(reaching.join('\n') || 'nothing reads a cached vc').toBe('nothing reads a cached vc');
  });

  it('reads nothing from a vc pin', () => {
    const reaching = windowsCode.split('\n')
      .filter((line) => /\bwin\.vc\b|\bpins\.windows\.vc\b|releaseTag|releaseCommit|cliSourceCommit|assetName/.test(line));
    expect(reaching.join('\n') || 'nothing reads a vc pin').toBe('nothing reads a vc pin');
  });

  it('builds vc from ./cmd/vc with the Go toolchain, as the macOS assembly does', () => {
    // The same invocation, not a similar one: -trimpath and -buildvcs=false are
    // what make the binary reproducible, and a Windows build that dropped them
    // would produce a bundle nobody could compare against a Mac one.
    const goBuild = /execFileSync\(\s*'go',\s*\[\s*'build',\s*'-trimpath',\s*'-buildvcs=false',\s*'-o'/;
    expect(goBuild.test(macAssembly)).toBe(true);
    expect(goBuild.test(windowsCode) && /'\.\/cmd\/vc'/.test(windowsCode)).toBe(true);
  });

  it('takes GOOS and GOARCH from the shared assembly target, never from a literal', () => {
    // `GOOS: 'windows'` typed into this file is the same class of constant the
    // Mac assembly stopped carrying when Intel was added: it makes the third
    // platform a new branch here instead of a new entry in one map. The target
    // and the builds it needs are resource-assembly-lib.mjs's answer --
    // assemblyTarget for the vocabularies, vcBuildPlan for which builds to run
    // and what each is for, including the host-runnable one the version is read
    // from when the shipped binary cannot be run where it was built.
    expect(/from\s+'\.\/resource-assembly-lib\.mjs'/.test(windowsCode)).toBe(true);
    expect(windowsCode).toMatch(/\bassemblyTarget\b/);
    expect(windowsCode).toMatch(/\bvcBuildPlan\b/);
    const literals = [...codeOf(windowsAssembly).matchAll(/(['"`])(?:windows|amd64|arm64|x64)\1/g)].map((match) => match[0]);
    expect(literals.join(', ') || 'no GOOS or GOARCH written into the script').toBe('no GOOS or GOARCH written into the script');
  });

  it('records the vc it staged, not a release it trusted', () => {
    // The manifest is what src/main/resources.ts authenticates the bundle
    // against, and today the Windows one writes the PIN's digest into it --
    // which cannot disagree with the file, because the same pin was used to
    // accept the file. The digest has to be taken from the bytes that were
    // staged, the way the Mac manifest takes it.
    const vcEntry = /\bvc:\s*\{[^}]*\}/.exec(windowsCode)?.[0] ?? 'the manifest has no vc entry';
    expect(/sha256:\s*await shaFile\(/.test(vcEntry) ? 'hashed from the staged bytes' : vcEntry).toBe('hashed from the staged bytes');
  });
});

// ---------------------------------------------------------------------------
// Provenance survives; only its source changes.
//
// tests/windows-pin-provenance.test.ts held this ground until now, and it held
// it well: the pin had to name a published release, the commit that release was
// built from, and the CLI revision inside it, and those three had to agree with
// git. Every one of those cases is about a release, so the file goes with the
// release. What must NOT go with it is the demand itself -- that the bytes in
// the bundle can be traced to a revision a reader can check out.
//
// The answer just moves. "vc-windows-amd64.exe of makscee/void-code v0.2.47,
// sha256 00ae01d6..." becomes "the vc this tree builds, commit <sha>", and a
// bundle that cannot say which commit it came from is exactly as unverifiable
// as one whose digest came from nowhere.
// ---------------------------------------------------------------------------

describe('the Windows bundle can still say where its vc came from', () => {
  it('asks git which tree it is packaging', () => {
    expect(/execFileSync\(\s*'git',\s*\[\s*'rev-parse',\s*'HEAD'\s*\]/.test(windowsCode)).toBe(true);
  });

  it('records that commit rather than a constant somebody typed', () => {
    // Deliberately stricter than the macOS manifest, which stamps the
    // `expectedCommit` literal at the top of assemble-resources.mjs -- a floor
    // it checks HEAD against, not the tree it built. A 40-hex literal in the
    // manifest identifies a commit somebody chose, and the whole incident was a
    // constant that stopped describing the thing it named.
    const vcEntry = /\bvc:\s*\{[^}]*\}/.exec(windowsCode)?.[0] ?? '';
    const bound = /sourceCommit:\s*([^,}]+)/.exec(vcEntry)?.[1]?.trim();
    const verdict = bound === undefined ? 'the manifest records no vc sourceCommit'
      : /^'?[0-9a-f]{40}'?$/.test(bound) ? `a commit literal typed into the script: ${bound}`
        : 'the commit git answered with';
    expect(verdict).toBe('the commit git answered with');
  });
});

// ---------------------------------------------------------------------------
// The machinery that qualified the pin goes with the pin.
//
// scripts/verify-windows-pin.mjs and its library fetch a release's SHA256SUMS
// and compare the pinned digest against the asset it names. There is no pin and
// no release to ask, so the script answers a question nobody can pose --
// unreachable code that still typechecks, still lints, and reads to the next
// person as a step they forgot to run. tests/windows-pin-published.test.ts and
// tests/windows-pin-transport.test.ts covered it thoroughly, and both were
// deleted with this change; this case is what stops the code they covered being
// left behind untested.
// ---------------------------------------------------------------------------

describe('nothing is left over from qualifying a pin', () => {
  it('has no pin-verification scripts', () => {
    const leftover = ['scripts/verify-windows-pin.mjs', 'scripts/verify-windows-pin-lib.mjs']
      .filter((script) => existsSync(path.join(desktop, script)));
    expect(leftover.join(', ') || 'no pin-verification script remains').toBe('no pin-verification script remains');
  });

  it('offers no command to qualify a pin', () => {
    const scripts = (JSON.parse(readFileSync(path.join(desktop, 'package.json'), 'utf8')) as { scripts: Record<string, string> }).scripts;
    const leftover = Object.entries(scripts).filter(([, command]) => /verify-windows-pin/.test(command)).map(([name]) => name);
    expect(leftover.join(', ') || 'no command qualifies a pin').toBe('no command qualifies a pin');
  });
});

describe('the shared assembly library already answers for Windows', () => {
  // Green before the change and after it: the library grew this when the Intel
  // Mac build landed, and the Windows assembly simply never asked. Stated here
  // so a reader can see the implementation is a call, not a new mechanism.
  it('describes the Windows target in Go vocabulary', () => {
    expect(assemblyTarget('win32-x64', 'win32-x64')).toEqual({ platform: 'win32-x64', goos: 'windows', goarch: 'amd64', native: true });
  });

  it('plans one shipped build on a Windows host, and a host-runnable probe when the target is foreign', () => {
    const windowsOnWindows = assemblyTarget('win32-x64', 'win32-x64');
    expect(vcBuildPlan(windowsOnWindows, 'win32-x64')).toEqual([{ goos: 'windows', goarch: 'amd64', purpose: 'ship' }]);
    // The foreign case, shown on the pair the library actually permits: a
    // shipped build for the target plus a runnable one whose only job is to be
    // asked its version.
    const intelOnArm = assemblyTarget('darwin-x64', 'darwin-arm64');
    expect(vcBuildPlan(intelOnArm, 'darwin-arm64')).toEqual([
      { goos: 'darwin', goarch: 'amd64', purpose: 'ship' },
      { goos: 'darwin', goarch: 'arm64', purpose: 'version' },
    ]);
  });
});

// ---------------------------------------------------------------------------
// 4. The workflow provisions a toolchain, not a download.
//
// The reusable workflow is the only description of Windows packaging there is,
// and both callers reach it through `uses:`. Its provisioning step currently
// downloads two things; after this change it downloads one, and it needs Go for
// the other.
// ---------------------------------------------------------------------------

const REUSABLE = 'desktop-windows-app.yml';
const workflowText = (file: string) => readFileSync(path.resolve('..', '.github/workflows', file), 'utf8');

describe('the workflow reader', () => {
  // The reader is shared and is never trusted on its own, so it is shown to
  // read the shapes this file asks about before any claim is made about the
  // real workflow.
  const fixture = `
name: Fixture
on:
  workflow_call:
jobs:
  package:
    runs-on: windows-latest
    steps:
      - uses: actions/setup-go@aaa111 # v5
        with:
          go-version-file: .go-version
      - name: Fetch things
        run: |
          echo https://example.invalid/thing
          echo done
`;
  const parsed = parseWorkflow(fixture);
  const steps = asList(asMap(asMap(parsed.jobs).package).steps).map(asMap);

  it('reads a step by its action and its with: block', () => {
    expect(asText(steps[0].uses)).toBe('actions/setup-go@aaa111');
    expect(asText(asMap(steps[0].with)['go-version-file'])).toBe('.go-version');
  });

  it('reads a multi-line run script whole', () => {
    expect(asText(steps[1].run).split('\n')).toEqual(['echo https://example.invalid/thing', 'echo done']);
  });
});

const reusable: YamlMap = parseWorkflow(workflowText(REUSABLE));
const packagingJob = asMap(Object.values(asMap(reusable.jobs))[0]);
const packagingSteps = asList(packagingJob.steps).map(asMap);
const runScripts = packagingSteps.map((step) => asText(step.run)).filter(Boolean);

describe(`${REUSABLE} provisions a Go toolchain instead of a vc download`, () => {
  it('sets up Go, because the packaging step now compiles vc', () => {
    const go = packagingSteps.find((step) => asText(step.uses).startsWith('actions/setup-go@'));
    expect(asText(asMap(go?.with)['go-version-file'] ?? '') || 'no actions/setup-go step with a version file').toBe('.go-version');
  });

  it('downloads no vc from a release', () => {
    const downloading = runScripts.filter((script) => /releases\/download|runtime\/cache\/vc|pins\.vc\b|\.vc\.(?:repository|releaseTag|assetName)/.test(script));
    expect(downloading.join('\n---\n') || 'nothing downloads a vc').toBe('nothing downloads a vc');
  });

  it('still fetches the pinned Node archive through resource-pins.json', () => {
    // The Node half of the provisioning step is not collateral. Removing it
    // would leave the assembly with no archive to authenticate, and the job
    // would fail at packaging rather than here.
    const provisioning = runScripts.filter((script) => /resource-pins\.json/.test(script));
    expect(provisioning.length > 0 ? 'provisioned' : 'nothing reads scripts/resource-pins.json to fetch the pinned Windows Node archive').toBe('provisioned');
    expect(provisioning.join('\n')).toMatch(/runtime\/cache\/node/);
  });
});
