import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';
import packageJson from '../package.json';
import { readBuildVersion } from '../scripts/build-version.mjs';

// build-version.test.ts proves the module answers correctly. This file proves
// the answer is USED -- by both assemblies, by the packer, and by the manifest.
// A correct module nobody calls is exactly the state the tree was already in:
// release.yml has stamped the CLI correctly since forever, and the desktop
// build simply never asked it anything.
//
// These are readings of source text, not measurements of a build. The
// assemblies cannot run here (the Windows one refuses to start off Windows;
// the Mac one wants a pinned Node archive nobody downloads in a unit test), and
// electron-builder is minutes of work per invocation. What a reader can check
// is what is checked: which module the scripts import, which call they make,
// and what the packer is handed. The behaviour they produce is measured for
// real by scripts/packaged-smoke.mjs and scripts/windows-package-check.mjs,
// which compare the packaged bundle against the manifest it ships -- see
// packaged-check-lib.test.ts for the rule those two share.

const scriptsDirectory = new URL('../scripts/', import.meta.url);
const read = (relative: string): string => readFileSync(new URL(relative, import.meta.url), 'utf8');
// Comments describe, code does. A sentence about the old arrangement must not
// be able to satisfy -- or to falsify -- a rule about the current one.
const codeOf = (source: string): string => source.split('\n').filter((line) => !/^\s*(?:\/\/|\*|\/\*)/.test(line)).join('\n');

const ASSEMBLIES = ['assemble-resources.mjs', 'assemble-windows-resources.mjs'] as const;

describe('both assemblies stamp the vc they build', () => {
  it.each(ASSEMBLIES)('%s asks build-version.mjs rather than composing ldflags itself', (file) => {
    const code = codeOf(read(`../scripts/${file}`));
    expect(code, `${file} does not import ./build-version.mjs`).toMatch(/from '\.\/build-version\.mjs'/);
    // The argv has to be the argv `go` is HANDED, not one computed beside the
    // call. The same class of mutation that survived in scripts/package.mjs --
    // compute the right thing, pass something else -- would survive a check for
    // a bare `vcBuildArgs(` anywhere in the file.
    expect(code, `${file} does not build vc with the argv vcBuildArgs returns`).toMatch(/execFileSync\(\s*'go',\s*vcBuildArgs\(/);
  });

  it.each(ASSEMBLIES)('%s no longer hands go build an argv typed into the script', (file) => {
    const code = codeOf(read(`../scripts/${file}`));
    // The current spelling is a literal array: ['build', '-trimpath', ...].
    // Leaving it beside a vcBuildArgs call is how one of the two platforms ends
    // up unstamped while the suite stays green -- which is the shape of the
    // regression this whole task is about, one platform at a time.
    expect(code, `${file} still builds vc from a hand-written argv`).not.toMatch(/'build',\s*'-trimpath'/);
  });

  it('only build-version.mjs asks git which version this is', () => {
    // One source of truth means one caller. A second `git describe` somewhere
    // in scripts/ is a second answer, free to disagree.
    const askers = readdirSync(scriptsDirectory)
      .filter((entry) => entry.endsWith('.mjs'))
      .filter((entry) => /['"]describe['"]/.test(codeOf(readFileSync(new URL(entry, scriptsDirectory), 'utf8'))));
    expect(askers.join(', ') || 'nothing').toBe('build-version.mjs');
  });
});

describe('the manifest records the version of the build, not only of the runtime', () => {
  it.each(ASSEMBLIES)('%s writes a build block into manifest.json', (file) => {
    const code = codeOf(read(`../scripts/${file}`));
    // `vc.version` becomes truthful on its own once the stamp lands, but it is
    // the string `vc --version` prints, and the QUESTION people ask is "which
    // version of the app is this". The manifest has to answer that directly:
    // scripts/packaged-smoke.mjs compares the installed bundle against it, and
    // a support report is worth reading only if the two agree.
    expect(code, `${file} writes no build block into the manifest`).toMatch(/build:\s*\{[^}]*version/);
  });
});

describe('the packer is told the version; the tree is not rewritten to carry it', () => {
  const scripts = packageJson.scripts as Record<string, string>;
  const scriptFiles = readdirSync(scriptsDirectory).filter((entry) => entry.endsWith('.mjs') || entry.endsWith('.cjs'));
  const sourceOf = (entry: string) => readFileSync(new URL(entry, scriptsDirectory), 'utf8');

  it('every invocation of electron-builder carries -c.extraMetadata.version', () => {
    // Stated as a rule over whatever invokes the packer, so the mechanism stays
    // the implementer's choice -- an npm script that passes the flag, or a node
    // wrapper that computes it -- while the property holds for all of them. A
    // second packaging entry point added later without the flag is red here.
    const units: Array<{ where: string; text: string }> = [
      ...Object.entries(scripts).map(([name, command]) => ({ where: `package.json scripts.${name}`, text: command })),
      ...scriptFiles.map((entry) => ({ where: `scripts/${entry}`, text: codeOf(sourceOf(entry)) })),
    ];
    const unstamped = units
      .filter((unit) => /\belectron-builder\b/.test(unit.text))
      .filter((unit) => !/extraMetadata\.version/.test(unit.text))
      .map((unit) => unit.where);
    expect(unstamped.join(', ') || 'every packer invocation carries a version').toBe('every packer invocation carries a version');
  });

  it('the version the packer gets comes from build-version.mjs', () => {
    const computing = scriptFiles.filter((entry) => /extraMetadata\.version/.test(codeOf(sourceOf(entry))));
    expect(computing.length > 0 ? 'computed in a script' : 'no scripts/*.mjs computes -c.extraMetadata.version, so nothing can derive it from git').toBe('computed in a script');
    for (const entry of computing) {
      expect(codeOf(sourceOf(entry)), `scripts/${entry} spells out a version instead of asking build-version.mjs`).toMatch(/from '\.\/build-version\.mjs'/);
    }
  });

  // -------------------------------------------------------------------------
  // And it reaches the packer's argv. This is the only test in this file that
  // RUNS anything, and it exists because the two rules above -- both of which
  // read source text -- are jointly satisfied by a wrapper that computes the
  // version and then forgets to pass it: deleting `...versionArgs` from the
  // execFileSync call leaves both substrings in place, keeps this suite green,
  // and ships an installer that says 0.1.0. That is the whole defect, restored,
  // under a green suite. It survived a mutation run; nothing but a person
  // building a bundle by hand would have caught it.
  //
  // THE SEAM IS DELIBERATE, and it is the team lead's call, not a convenience.
  // scripts/package.mjs reads VOID_DESKTOP_PACKER for the path to the packer so
  // this test can put a recorder there. The alternative -- exporting the argv
  // as a pure function and testing that -- does not catch the mutation, because
  // the mutation is at the CALL, not in the argv. Running electron-builder for
  // real is minutes per invocation and will never be in CI, so without the seam
  // the only real check is a human packaging a bundle.
  //
  // What the seam does NOT do is let the default drift: the wrapper must still
  // name the real packer when the variable is unset, asserted as text below,
  // because a default pointed at anything else is not observable from here.
  // -------------------------------------------------------------------------
  const temporaries: string[] = [];
  afterAll(() => { for (const root of temporaries.splice(0)) rmSync(root, { recursive: true, force: true }); });

  it('hands -c.extraMetadata.version to the packer it actually runs', () => {
    const root = mkdtempSync(path.join(os.tmpdir(), 'vc-packer-stub-'));
    temporaries.push(root);
    const stub = path.join(root, 'packer-stub.mjs');
    // A recorder, not a packer: it prints the argv it was handed and exits.
    writeFileSync(stub, 'console.log(JSON.stringify(process.argv.slice(2)));\n');

    const wrapper = new URL('../scripts/package.mjs', import.meta.url);
    const stdout = execFileSync(process.execPath, [decodeURIComponent(wrapper.pathname), '--win', '--x64'], {
      cwd: decodeURIComponent(new URL('../', import.meta.url).pathname),
      encoding: 'utf8',
      env: { ...process.env, VOID_DESKTOP_PACKER: stub },
    });
    const recorded = stdout.trim().split('\n').at(-1) ?? '';
    const argv = JSON.parse(recorded) as string[];

    // The caller's own flags survive -- a wrapper that dropped --win would
    // package the wrong platform while satisfying the version rule.
    expect(argv).toContain('--win');
    expect(argv).toContain('--x64');
    // And the version, compared against what git says right now rather than
    // against a literal: the relation, so this keeps meaning what it means at
    // v0.3.0.
    expect(argv).toContain(`-c.extraMetadata.version=${readBuildVersion().packageVersion}`);
  });

  it('still names the real packer when nothing overrides it', () => {
    const wrapper = codeOf(readFileSync(new URL('../scripts/package.mjs', import.meta.url), 'utf8'));
    expect(wrapper, 'the packer wrapper does not read VOID_DESKTOP_PACKER, so the check above cannot observe it').toMatch(/VOID_DESKTOP_PACKER/);
    expect(wrapper, 'the packer wrapper no longer defaults to electron-builder').toMatch(/node_modules\/electron-builder\/cli\.js/);
  });

  it('no npm script computes the version with a shell substitution', () => {
    // npm runs scripts through %COMSPEC% on Windows, so `$(node -p ...)` is not
    // expanded -- electron-builder would receive the seven characters `$(node`
    // and the rest as separate arguments. The Windows installer is the artifact
    // that matters most here, so the trap is closed rather than remembered.
    const substituting = Object.entries(scripts).filter(([, command]) => /\$\(|`/.test(command)).map(([name]) => name);
    expect(substituting.join(', ') || 'no script uses command substitution').toBe('no script uses command substitution');
  });

  it('nothing writes a version into desktop/package.json', () => {
    // The alternative implementation -- `npm version` or a sed over the file
    // before packaging -- makes the build mutate its own source, so a local
    // build leaves a modified tree and the next commit carries a version
    // nobody chose. -c.extraMetadata.version exists precisely so this is not
    // necessary.
    const writers = scriptFiles.filter((entry) => {
      const code = codeOf(sourceOf(entry));
      return /writeFileSync?\([^)]*package\.json|npm\s+version\b/.test(code);
    });
    expect(writers.join(', ') || 'nothing rewrites package.json').toBe('nothing rewrites package.json');
  });
});
