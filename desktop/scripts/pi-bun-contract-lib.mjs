// The undeclared contract the bundled Pi runtime rests on, asserted against Pi's own source.
//
// Pure by design, like packaged-check-lib.mjs and resource-assembly-lib.mjs beside it: it takes two
// strings and reads nothing. Whether the check is built right is answered by fixtures in
// tests/pi-bun-contract.test.ts; what today's vendored Pi actually says is answered by
// check-pi-bun-contract.mjs, which is the only half that needs Pi on disk.
//
// What the contract is. Bundled to one file, Pi has no node_modules to resolve against, and its
// extension loader's ordinary path calls require.resolve for typebox and the rest before an
// extension asks for anything. The path that uses the modules already inside the bundle is the one
// Pi takes when it believes it is a compiled Bun binary, and it decides that by looking at its own
// module URL for "$bunfs", "~BUN" or "%7EBUN". Pi 0.84.1 offers no supported switch, so naming the
// bundle pi~BUN.mjs is the whole of our lever.
//
// Pi is vendored and pinned by hash, so a new Pi release cannot break this on its own. A deliberate
// bump can, silently, and this is written to go red in the hands of whoever makes it.

const MARK = '~BUN';

// Why the consequence and not the cause: whoever reads this is mid-bump, with no reason to know
// what a file name has to do with anything.
const COST = [
  'What this costs: with no node_modules beside the bundle, every extension fails to load,',
  'including the one vc installs to register its provider -- so the desktop app starts and finds',
  'no provider at all. Falling back means unbundling Pi: 19,069 files again, and a cold Windows',
  'start of 278 s instead of 11.6 s.',
].join('\n');

function refuse(what, detail) {
  throw new Error(`Pi no longer honours the contract the bundled runtime rests on: ${what}\n${detail}\n${COST}`);
}

/**
 * Comments removed, strings kept. Pi's own config.js names all three markers in the prose above the
 * line that uses them, so a check that greps the file stays green through the exact edit it exists
 * to catch. Regular-expression literals are not tracked -- a regex holding a quote character would
 * confuse the scan -- and Pi 0.84.1 has none; a future one that did would send this red rather than
 * quietly green, which is the direction to fail in.
 */
export function stripComments(source) {
  let out = '';
  let index = 0;
  while (index < source.length) {
    const character = source[index];
    const pair = source.slice(index, index + 2);
    if (pair === '//') {
      const end = source.indexOf('\n', index);
      index = end < 0 ? source.length : end;
      continue;
    }
    if (pair === '/*') {
      const end = source.indexOf('*/', index + 2);
      index = end < 0 ? source.length : end + 2;
      out += ' ';
      continue;
    }
    if (character === '"' || character === "'" || character === '`') {
      let cursor = index + 1;
      while (cursor < source.length && source[cursor] !== character) cursor += source[cursor] === '\\' ? 2 : 1;
      out += source.slice(index, cursor + 1);
      index = cursor + 1;
      continue;
    }
    out += character;
    index += 1;
  }
  return out;
}

/** Values of the string literals in a snippet, unescaped only as far as this contract needs. */
function stringValues(snippet) {
  const values = [];
  for (const match of snippet.matchAll(/"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|`((?:[^`\\]|\\.)*)`/g)) {
    values.push((match[1] ?? match[2] ?? match[3]).replace(/\\(.)/g, '$1'));
  }
  return values;
}

/**
 * Does this snippet test for the marker, directly or through a name it uses?
 *
 * Both halves are needed and neither alone is enough. Direct only would red on a tidy-up that hoists
 * the markers into an array -- a change that costs us nothing. Anywhere-in-the-file would stay green
 * on a Pi that dropped "~BUN" from the expression while keeping the word in a comment or an
 * unrelated constant, which is precisely the change this exists to catch. So: the marker must be
 * reachable from what computes the flag, and reachable means named there.
 *
 * The literal must be the marker exactly. A string that merely contains it -- a documentation URL,
 * say -- is not a test for it.
 */
function testsForMark(expression, source) {
  if (stringValues(expression).includes(MARK)) return true;
  for (const identifier of new Set(expression.match(/[A-Za-z_$][A-Za-z0-9_$]*/g) ?? [])) {
    const declaration = new RegExp(`(?:const|let|var)\\s+${identifier}\\s*=\\s*([^;]*);`).exec(source);
    if (declaration && stringValues(declaration[1]).includes(MARK)) return true;
  }
  return false;
}

/**
 * The branch isBunBinary selects, as source text: the consequent of a ternary, or the block of an
 * `if`. Returns every such branch, because loader.js mentions the flag more than once and only one
 * of those mentions is the one that steers jiti.
 */
function branchesSelectedBy(source, flag) {
  const branches = [];
  for (const match of source.matchAll(new RegExp(`\\b${flag}\\b`, 'g'))) {
    let cursor = match.index + flag.length;
    while (cursor < source.length && /[\s)]/.test(source[cursor])) cursor += 1;
    const opener = source[cursor];
    if (opener !== '?' && opener !== '{') continue;
    cursor += opener === '?' ? 1 : 0;
    let depth = 0;
    const start = cursor;
    for (; cursor < source.length; cursor += 1) {
      const character = source[cursor];
      if ('([{'.includes(character)) depth += 1;
      else if (')]}'.includes(character)) {
        depth -= 1;
        if (depth === 0 && opener === '{') { cursor += 1; break; }
        if (depth < 0) break;
      } else if (character === ':' && depth === 0 && opener === '?') break;
    }
    branches.push(source.slice(start, cursor));
  }
  return branches;
}

/**
 * Throws unless Pi still lets a file name switch it into bundle mode, and unless that mode still
 * routes the extension loader onto the modules inside the bundle.
 *
 * Honest limit: this reads somebody else's source and proves the shape of an expression, not what Pi
 * does when it runs. Behaviour is check-bundled-pi-smoke.mjs's job -- the real extension, loaded
 * with no node_modules. This half is worth having because it fails earlier and says why.
 */
export function assertPiBunContract({ config, loader }) {
  const configSource = stripComments(config);
  const declaration = /\bisBunBinary\s*=\s*([^;]*);/.exec(configSource);
  if (!declaration) {
    refuse('config.js declares no isBunBinary at all', 'Nothing is left to switch Pi into the mode that serves extensions from inside the bundle.');
  }
  const expression = declaration[1];
  if (!/import\s*\.\s*meta\s*\.\s*url/.test(expression)) {
    refuse('isBunBinary no longer reads the module\'s own URL', 'Our lever is the bundle\'s file name. A flag computed from the runtime, or from anything\nother than import.meta.url, cannot be turned on by naming a file.');
  }
  if (!testsForMark(expression, configSource)) {
    refuse(`isBunBinary no longer tests for the ${MARK} marker`, `The bundle is named pi${MARK}.mjs precisely so this test matches it. Pi may move the markers\naround -- an array, a loop, a reformat -- but it has to still look for ${MARK}.`);
  }
  const loaderSource = stripComments(loader);
  const branches = branchesSelectedBy(loaderSource, 'isBunBinary');
  if (branches.length === 0) {
    refuse('the extension loader no longer branches on isBunBinary', 'The flag is worth nothing unless it still steers the loader.');
  }
  if (!branches.some((branch) => branch.includes('virtualModules'))) {
    refuse('the extension loader\'s bun branch no longer uses the bundled modules', 'It has to keep resolving extensions through virtualModules rather than getAliases(), whose\nrequire.resolve calls need a node_modules that a bundled runtime does not have.');
  }
}
