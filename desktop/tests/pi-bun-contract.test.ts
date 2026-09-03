// rails:pin-on-coverage the two comment fixtures below pin behaviour that is already correct; each was run against a build with stripComments() replaced by the identity, which accepts both -- see the task report
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Bundling Pi's runtime to 17 files takes a cold Windows start from 278 s to 11.6 s, and the whole
// thing rests on an undeclared contract inside somebody else's package.
//
// Pi's extension loader, without node_modules on disk, dies: getAliases() calls require.resolve for
// typebox and the rest. The one path that uses the modules already inside the bundle is the one Pi
// takes when it believes it is a compiled Bun binary -- and it decides that by looking at its own
// module URL for "$bunfs", "~BUN" or "%7EBUN". There is no supported switch; the whole of our lever
// is that we named the file pi~BUN.mjs. Pi 0.84.1's dist was grepped for an explicit one, and there
// is none.
//
// Pi is pinned by hash, so a new Pi release breaks nothing by itself. What breaks things is a
// deliberate bump -- and this is meant to go red in the hands of whoever makes it.
//
// Honest limit: this reads somebody else's source and proves the shape of an expression, not what Pi
// does. Proving behaviour is the functional smoke's job (a real extension loaded with no
// node_modules); this one is only worth having because it fails earlier and says why.

// ---------------------------------------------------------------------------
// Verbatim from Pi 0.84.1, dist/config.js and dist/core/extensions/loader.js.
// Copied rather than read, because runtime/pi/node_modules is gitignored: this suite has to run on a
// checkout where the vendored Pi has never been provisioned.
// ---------------------------------------------------------------------------
const CONFIG_TODAY = `const __filename = fileURLToPath(import.meta.url);
/**
 * Detect if we're running as a Bun compiled binary.
 * Bun binaries have import.meta.url containing "$bunfs", "~BUN", or "%7EBUN" (Bun's virtual filesystem path)
 */
export const isBunBinary = import.meta.url.includes("$bunfs") || import.meta.url.includes("~BUN") || import.meta.url.includes("%7EBUN");
/** Detect if Bun is the runtime (compiled binary or bun run) */
export const isBunRuntime = !!process.versions.bun;
`;

const LOADER_TODAY = `    const jiti = createJiti(import.meta.url, {
        moduleCache: false,
        // Bun uses modules embedded in the executable. Source TypeScript reuses the
        // host-resolved modules and root tsconfig paths. Built Node uses dist aliases.
        ...(isBunBinary
            ? { virtualModules: VIRTUAL_MODULES, tryNative: false }
            : isTypeScriptSourceRuntime
                ? { virtualModules: VIRTUAL_MODULES, tsconfigPaths: true }
                : { alias: getAliases() }),
    });
`;

const shipped = { config: CONFIG_TODAY, loader: LOADER_TODAY };

// Imported per test rather than at the top, so a missing module reds each test under its own name
// instead of collapsing the file into "no tests" -- which reads like the suite shrank.
type Contract = (sources: { config: string; loader: string }) => void;
const contract = async (): Promise<Contract> => (await import('../scripts/pi-bun-contract-lib.mjs') as { assertPiBunContract: Contract }).assertPiBunContract;

describe('the undeclared Pi contract the bundled runtime rests on', () => {
  it('accepts the detection Pi ships today', async () => {
    const assertPiBunContract = await contract();
    expect(() => assertPiBunContract(shipped)).not.toThrow();
  });

  it('accepts a rewrite that moves the markers around without changing what they mean', async () => {
    const assertPiBunContract = await contract();
    // The point of the pin is to catch a real change, not a tidy-up. Pi is free to hoist the marks
    // into an array, iterate them, reformat, reorder -- our lever survives all of it, and so must
    // this. If this test ever has to be weakened to let a cosmetic change through, the pin is doing
    // more harm than good and the functional smoke should carry the contract alone.
    const rewritten = `const BUN_MARKS = ["$bunfs", "~BUN", "%7EBUN"];
export const isBunBinary = BUN_MARKS.some((mark) => import.meta.url.includes(mark));
`;
    expect(() => assertPiBunContract({ ...shipped, config: rewritten })).not.toThrow();
  });

  it('refuses a Pi that no longer treats ~BUN as a marker, and says what that costs us', async () => {
    const assertPiBunContract = await contract();
    const withoutOurMark = CONFIG_TODAY.replace(' || import.meta.url.includes("~BUN")', '');
    let thrown: Error | undefined;
    try { assertPiBunContract({ ...shipped, config: withoutOurMark }); } catch (error) { thrown = error as Error; }
    expect(thrown, 'a Pi that ignores ~BUN was accepted').toBeInstanceOf(Error);
    // The message has to name the consequence, because the person reading it is mid-bump and has no
    // reason to know what a filename has to do with anything.
    expect(thrown?.message, 'the failure does not say that extensions stop loading').toMatch(/extension/i);
    expect(thrown?.message, 'the failure does not say the app loses its provider').toMatch(/provider/i);
  });

  it('is not satisfied by ~BUN turning up somewhere else in the file', async () => {
    const assertPiBunContract = await contract();
    // Pi's own file mentions "~BUN" twice already: once in the prose above the line, once in the
    // line. A check that greps the file would stay green through the exact change it exists to
    // catch, so the marker has to be found in what computes the flag and nowhere else.
    const elsewhere = `const BUN_DOCS = "https://bun.sh/docs/~BUN";
export const isBunBinary = import.meta.url.includes("$bunfs") || import.meta.url.includes("%7EBUN");
`;
    expect(() => assertPiBunContract({ ...shipped, config: elsewhere })).toThrow();
  });

  it('refuses detection that no longer comes from the module\'s own URL', async () => {
    const assertPiBunContract = await contract();
    // Our lever is the file name. A Pi that asks the runtime instead of its own path -- which it
    // already does elsewhere, as isBunRuntime -- cannot be switched on by naming a file.
    const fromRuntime = CONFIG_TODAY.replace(/export const isBunBinary = [^;]*;/, 'export const isBunBinary = !!process.versions.bun;');
    expect(() => assertPiBunContract({ ...shipped, config: fromRuntime })).toThrow();
  });

  it('refuses a Pi with no isBunBinary at all', async () => {
    const assertPiBunContract = await contract();
    expect(() => assertPiBunContract({ ...shipped, config: 'export const isBunRuntime = !!process.versions.bun;\n' })).toThrow();
  });

  it('refuses a loader that stops using the bundled modules for the bun path', async () => {
    const assertPiBunContract = await contract();
    // The other half of the contract, and the half that explains why any of this works: the flag is
    // worth nothing unless it still routes the loader away from getAliases() and its require.resolve
    // calls, onto the modules already inside the bundle.
    const alwaysAliases = `    const jiti = createJiti(import.meta.url, {
        moduleCache: false,
        alias: getAliases(),
    });
`;
    expect(() => assertPiBunContract({ ...shipped, loader: alwaysAliases })).toThrow();
  });

  it('is not fooled by the old form surviving in a block comment above the live one', async () => {
    // Found by the implementer, mutating his own work: removing stripComments() survived every
    // fixture here. It survived because none of them put a whole assignment inside a comment --
    // Pi's real prose mentions the markers, but not in the shape of code, so the declaration regex
    // skipped past it either way.
    //
    // This is the shape that tells the two apart, and it is the ordinary shape of a deprecation: the
    // old line kept above the new one, for whoever comes looking. Verified both ways -- refused as
    // the checker stands, accepted by a build whose stripComments() returns its argument.
    const documented = `/**
 * Detect if we are running as a Bun compiled binary. Until 0.85 this read:
 *   isBunBinary = import.meta.url.includes("$bunfs") || import.meta.url.includes("~BUN");
 */
export const isBunBinary = import.meta.url.includes("$bunfs") || import.meta.url.includes("%7EBUN");
`;
    const assertPiBunContract = await contract();
    expect(() => assertPiBunContract({ ...shipped, config: documented }), 'the old form in a comment was read as the live one').toThrow();
  });

  it('is not fooled by the old form surviving in line comments', async () => {
    // The same trap through the other half of stripComments(): `//` and `/* */` are separate paths
    // in it, and a fixture for one proves nothing about the other.
    const documented = `// Until 0.85 this read:
// export const isBunBinary = import.meta.url.includes("$bunfs") || import.meta.url.includes("~BUN");
export const isBunBinary = import.meta.url.includes("$bunfs") || import.meta.url.includes("%7EBUN");
`;
    const assertPiBunContract = await contract();
    expect(() => assertPiBunContract({ ...shipped, config: documented }), 'the old form in a comment was read as the live one').toThrow();
  });

  it('reads the declaration, not the first thing in the file shaped like one', async () => {
    // The comment case is a symptom; this is the illness. The check takes the first text matching
    // `isBunBinary = ...;` anywhere in the file, and a comment was only the first place that turned
    // out to be. Strings are kept on purpose -- testsForMark needs their values -- so a string
    // holding the old line reaches the same regex, and stripping comments does nothing about it.
    //
    // Verified both ways and it fails both: refused by neither build. Anchoring the match to a
    // declaration -- optional `export`, then const/let/var -- closes the class rather than this one
    // instance, because a comment, a string, and whatever turns up next are all "not a declaration".
    const noted = `const MIGRATION_NOTE = 'isBunBinary = import.meta.url.includes("~BUN");';
export const isBunBinary = import.meta.url.includes("$bunfs") || import.meta.url.includes("%7EBUN");
`;
    const assertPiBunContract = await contract();
    expect(() => assertPiBunContract({ ...shipped, config: noted }), 'a string holding the old line was read as the declaration').toThrow();
  });

  it('runs where a version bump actually happens', () => {
    // runtime/pi/node_modules is gitignored, so this file cannot be read by the ordinary suite: on a
    // fresh checkout it is not there. The path that both provisions the pinned Pi and is run by CI
    // (test.yml and release.yml) is provision-pinned-pi-smoke.sh -- so the check has to be called
    // from there, or it will only ever run when somebody remembers to run it by hand.
    //
    // Honest limit: this proves the call is written in the script, not that CI ran it.
    const provision = readFileSync(new URL('../scripts/provision-pinned-pi-smoke.sh', import.meta.url), 'utf8');
    expect(provision, 'nothing on the pinned-Pi path checks the bun contract, so a version bump will not go red').toMatch(/pi-bun-contract/);
  });
});
