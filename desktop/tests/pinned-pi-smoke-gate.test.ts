import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// check-pinned-pi-smoke.mjs runs `go test ./cmd/vc -run '^TestPiVoidCodexExtensionSmoke$'`, and no
// such test exists anywhere in the repository -- the only occurrence of the name is the line that
// asks for it. Go answers a filter that matches nothing the same way it answers one that matched
// and passed:
//
//     ok  github.com/makscee/void-code/cmd/vc  0.309s [no tests to run]
//     exit 0
//
// So provision-pinned-pi-smoke.sh reports success, test.yml and release.yml are satisfied, and
// every push and every release has been carrying a pinned-Pi qualification that qualifies nothing.
// This is on main and predates our work here.
//
// What makes this worth a gate rather than a fix: the failure mode is silence, and silence is what
// the fix has to make impossible. A test can be renamed, moved, or lost in a merge, and go will go
// on answering `ok`.
//
// Honest limit: this pins the reading of go's output. It cannot tell that the test, once found,
// tested anything -- that is the second half of the work, the smoke itself.

// ---------------------------------------------------------------------------
// Captured from `go test ./cmd/vc -list ...` in this checkout, not invented: the summary line is
// exactly what go prints, tabs and all, and it is the whole reason a "did anything come back?"
// check reads as yes.
// ---------------------------------------------------------------------------
const SUMMARY = 'ok  \tgithub.com/makscee/void-code/cmd/vc\t0.223s\n';
const NOTHING_MATCHED = SUMMARY;
const ONE_MATCHED = `TestPiVoidCodexExtensionSmoke\n${SUMMARY}`;
const MANY_LISTED = `TestStatusJSONAsksTheAccessCheckHost
TestAccessRequestReadsEachState
TestPiVoidCodexExtensionSmoke
TestDecideGate
${SUMMARY}`;

const NAME = 'TestPiVoidCodexExtensionSmoke';

// Per test rather than at the top: a missing module reds each test under its own name instead of
// collapsing the file into "no tests", which reads like the suite shrank.
type Gate = (inspection: { name: string; output: string; status: number }) => void;
const gate = async (): Promise<Gate> => (await import('../scripts/pinned-pi-smoke-lib.mjs') as { assertNamedTestExists: Gate }).assertNamedTestExists;

const refusal = async (inspection: { name: string; output: string; status: number }): Promise<Error> => {
  const assertNamedTestExists = await gate();
  try { assertNamedTestExists(inspection); } catch (error) { return error as Error; }
  throw new Error('the gate accepted a listing it should have refused');
};

describe('the pinned-Pi qualification cannot pass by finding nothing', () => {
  it('refuses a listing in which no test matched, and says what the silence costs', async () => {
    const error = await refusal({ name: NAME, output: NOTHING_MATCHED, status: 0 });
    // The message is read by somebody whose build just went red on a line they did not write, so it
    // has to say what did not happen rather than what did not match.
    expect(error.message, 'the refusal does not name the test it went looking for').toContain(NAME);
    expect(error.message, 'the refusal does not say the pinned-Pi qualification never ran').toMatch(/pinned Pi|qualif/i);
    expect(error.message, 'the refusal does not say an incompatible Pi can reach a release').toMatch(/release/i);
  });

  it('accepts a listing that names the test', async () => {
    const assertNamedTestExists = await gate();
    expect(() => assertNamedTestExists({ name: NAME, output: ONE_MATCHED, status: 0 })).not.toThrow();
  });

  it('accepts the test among the whole package listing', async () => {
    // `-list` is a regexp filter, so the caller may hand over one name or all of them. A gate that
    // only works when go printed exactly one line would break the day somebody widens the filter.
    const assertNamedTestExists = await gate();
    expect(() => assertNamedTestExists({ name: NAME, output: MANY_LISTED, status: 0 })).not.toThrow();
  });

  it('is not fooled by a test whose name merely begins with the one we asked for', async () => {
    // The realistic shape of a rename. `output.includes(name)` is true here and wrong: the smoke we
    // asked for is gone, and what remains is a different test that happens to share a prefix.
    const renamed = `TestPiVoidCodexExtensionSmokeDisabled\n${SUMMARY}`;
    await refusal({ name: NAME, output: renamed, status: 0 });
  });

  it('does not count go\'s own summary line as a test', async () => {
    // Stated separately from the empty case because it is the specific lie that kept this quiet for
    // as long as it has been quiet: the output is never empty, so "did anything come back?" is
    // always yes.
    const assertNamedTestExists = await gate();
    expect(() => assertNamedTestExists({ name: 'ok', output: NOTHING_MATCHED, status: 0 })).toThrow();
  });

  it('reports a listing it could not obtain as its own outcome, not as a missing test', async () => {
    // Three outcomes, not two. A package that fails to compile prints no names either, and calling
    // that "the test does not exist" sends the reader to write a test that is already there.
    const brokenBuild = 'cmd/vc/pi_extension.go:12:2: undefined: piRuntimeRoot\nFAIL\tgithub.com/makscee/void-code/cmd/vc [build failed]\n';
    const error = await refusal({ name: NAME, output: brokenBuild, status: 1 });
    expect(error.message, 'a package that would not build was reported as a missing test').not.toMatch(/does not exist|no such test|missing/i);
  });

  it('is what check-pinned-pi-smoke.mjs actually decides on', async () => {
    // Without this the gate can be written, tested, and never consulted -- which is the state the
    // script is in today, only with a library sitting next to it.
    //
    // Honest limit: this proves the call is written in the script, not that CI ran it.
    const checker = readFileSync(new URL('../scripts/check-pinned-pi-smoke.mjs', import.meta.url), 'utf8');
    expect(checker, 'check-pinned-pi-smoke.mjs still takes go\'s exit code at face value').toMatch(/pinned-pi-smoke-lib/);
  });
});
