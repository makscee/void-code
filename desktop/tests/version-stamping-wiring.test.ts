import { readFileSync, readdirSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import packageJson from '../package.json';

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
    expect(code, `${file} does not call vcBuildArgs`).toMatch(/\bvcBuildArgs\s*\(/);
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
