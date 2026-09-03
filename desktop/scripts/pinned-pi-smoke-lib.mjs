// ---------------------------------------------------------------------------
// Reading `go test -list` without letting silence pass for success.
//
// go answers a filter that matched nothing the same way it answers one that
// matched and passed: it prints its own summary line and exits 0.
//
//     ok  	github.com/makscee/void-code/cmd/vc	0.223s
//
// So the output is never empty, "did anything come back?" is always yes, and a
// pinned-Pi qualification whose test has been renamed, moved, or lost in a
// merge goes on reporting success on every push and every release.
//
// Three outcomes, not two:
//   - the name is listed        -> the qualification has something to run;
//   - go listed, name absent    -> refuse, and say what did not happen;
//   - go could not list at all  -> refuse as its own outcome. A package that
//     will not build prints no names either, and calling that a test that is
//     not there sends the reader off to write one that already exists.
// ---------------------------------------------------------------------------

/**
 * Decide, from a `go test -list` run the caller already made, whether the named
 * test is actually there. Pure: it runs nothing and reads nothing.
 *
 * @param {{ name: string, output: string, status: number }} inspection
 */
export function assertNamedTestExists({ name, output, status }) {
  if (status !== 0) {
    throw new Error(
      `could not obtain the test listing for the pinned Pi qualification: go exited ${status} instead of listing names. ` +
      `That is not an answer about ${name} -- a package that will not compile lists nothing either. ` +
      'The go output is above; until it builds, the qualification cannot be judged either way.',
    );
  }

  // Line by line and equal, not `includes`: `TestPiVoidCodexExtensionSmokeDisabled`
  // contains the name we asked for and is a different test, and go's summary
  // line contains whatever package path happens to be printed.
  const listed = output.split('\n').some((line) => line.trim() === name);
  if (!listed) {
    throw new Error(
      `the pinned Pi qualification never ran: the test ${name} does not exist in ./cmd/vc -- go listed the ` +
      'package and it was not among the names. Nothing checked that the pinned Pi runtime works, and an ' +
      'incompatible Pi can reach a release with every check still reporting green. What go did list is above.',
    );
  }
}
