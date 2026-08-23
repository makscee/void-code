import { readFileSync, readdirSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// release.yml ships the CLI and nothing else: six `vc-*` binaries, version.json
// and SHA256SUMS, then a sync of the same six into void-auth so installed
// clients update themselves. CI can now build both desktop applications --
// `desktop-mac-app` and `desktop-windows-app` in test.yml leave a bundle and an
// NSIS installer as run artifacts -- but a run artifact expires and needs a
// GitHub login to reach. Nobody can be handed one.
//
// This file states the shape that lets those builds reach a release, and states
// far more firmly what that shape may not become. Publishing an installer is
// forbidden to us without a separate gate, and tags are Maksim's. So the
// construction has to be one where merging it changes nothing: under an
// ordinary `v1.4.2` the workflow does exactly what it does today, and the
// desktop half wakes only when the owner says so -- a repository variable he
// sets, or a tag he spells differently.
//
// The four things pinned here:
//
//   1. Off by default. Under a plain tag the jobs that run are exactly today's
//      five and the assets published are exactly today's eight. The gate on
//      each desktop job is EVALUATED, not pattern-matched: it must come out
//      false with nothing set, and it must be capable of coming out true, so
//      neither `if: success()` nor `if: false` can pass for a gate.
//   2. A failed desktop build does not withhold the CLI. `build`, `release` and
//      `publish-auth` -- the last one being self-update -- must still run when
//      the desktop job fails. Either they do not need it, or it carries
//      continue-on-error, or their own condition survives an upstream failure.
//   3. The triggers do not widen. `push` on `v*.*.*` minus the vi20 canaries,
//      and nothing else -- no workflow_dispatch, no second tag-triggered
//      workflow -- so the only way in stays a tag Maksim pushes.
//   4. One release, not two. Exactly one step in the workflow creates a
//      release, and it is the one already there.
//
// What this file cannot do: it cannot run GitHub Actions, and it cannot push a
// tag to find out. It fixes the FORM of the workflow. That a release built this
// way would assemble, that the gated jobs would pass on a runner, that the
// assets would arrive intact -- none of that is tested here, by anyone, and
// saying otherwise would be a lie about what a green suite means.

// ---------------------------------------------------------------------------
// A reader for the workflow subset these files are written in.
//
// js-yaml sits in node_modules only as a transitive dependency of eslint and
// electron-builder; a test that reaches for it would break the day either one
// drops it. So the subset is read here, and the reader is first shown to read a
// fixture correctly, before any claim is made about the real files.
//
// This is the third copy of the reader mac-app-artifact-workflow.test.ts and
// windows-app-artifact-workflow.test.ts carry, and the windows file exports it
// precisely so the copies can go. Importing it was tried and measured, not
// assumed: `import { parseWorkflow } from './windows-app-artifact-workflow'`
// pulls that file's own 60 tests into this one -- vitest collects every
// describe() reached through the import graph -- and the suite grows from 505
// tests to 565, each Windows assertion running and reporting twice. The honest
// fix is a plain module, tests/workflow-yaml.ts, that all three import; it
// requires editing the two sibling files, which is not this file's to do. So:
// copied, deliberately, with the fixture below re-written around the shapes
// release.yml uses -- a negated tag pattern, a flow-sequence `needs`, a job
// condition, a `files:` list, a heredoc.
// ---------------------------------------------------------------------------

type YamlValue = string | YamlValue[] | { [key: string]: YamlValue };
type YamlMap = { [key: string]: YamlValue };

function stripComment(line: string) {
  let quote = '';
  for (let index = 0; index < line.length; index += 1) {
    const character = line[index];
    if (quote) {
      if (character === quote) quote = '';
      continue;
    }
    if (character === "'" || character === '"') { quote = character; continue; }
    if (character === '#' && (index === 0 || /\s/.test(line[index - 1]))) return line.slice(0, index);
  }
  return line;
}

const indentOf = (line: string) => line.length - line.trimStart().length;
const isBlank = (line: string) => stripComment(line).trim() === '';

function unquote(text: string) {
  const trimmed = text.trim();
  if (/^'[^]*'$/.test(trimmed)) return trimmed.slice(1, -1).replace(/''/g, "'");
  if (/^"[^]*"$/.test(trimmed)) return trimmed.slice(1, -1).replace(/\\(.)/g, '$1');
  return trimmed;
}

function flowSequence(text: string): YamlValue[] {
  const inner = text.trim().slice(1, -1).trim();
  return inner === '' ? [] : inner.split(',').map((item) => unquote(item));
}

// `run: |` and `cache-dependency-path: |`. Comment syntax does not apply inside
// a block scalar: a `#` line there is content, usually a shell comment.
function blockScalar(lines: string[], start: number, parentIndent: number, style: string): [string, number] {
  const collected: string[] = [];
  let bodyIndent = -1;
  let index = start;
  for (; index < lines.length; index += 1) {
    if (lines[index].trim() === '') { collected.push(''); continue; }
    if (indentOf(lines[index]) <= parentIndent) break;
    if (bodyIndent < 0) bodyIndent = indentOf(lines[index]);
    collected.push(lines[index].slice(bodyIndent));
  }
  while (collected.length > 0 && collected[collected.length - 1] === '') collected.pop();
  return [collected.join(style.startsWith('>') ? ' ' : '\n'), index];
}

function nextContent(lines: string[], from: number) {
  let index = from;
  while (index < lines.length && isBlank(lines[index])) index += 1;
  return index;
}

function parseNode(lines: string[], start: number, indent: number): [YamlValue, number] {
  const index = nextContent(lines, start);
  if (index >= lines.length) return ['', index];
  return /^-(\s|$)/.test(stripComment(lines[index]).trim())
    ? parseSequence(lines, index, indent)
    : parseMapping(lines, index, indent);
}

function parseSequence(lines: string[], start: number, indent: number): [YamlValue[], number] {
  const items: YamlValue[] = [];
  let index = start;
  while (index < lines.length) {
    if (isBlank(lines[index])) { index += 1; continue; }
    const content = stripComment(lines[index]).trimEnd();
    if (indentOf(content) < indent) break;
    const bare = content.trimStart();
    if (!/^-(\s|$)/.test(bare)) break;
    const after = bare.slice(1);
    const column = indentOf(content) + 1 + (after.length - after.trimStart().length);
    const value = after.trim();
    if (value === '') {
      const probe = nextContent(lines, index + 1);
      const [nested, next] = parseNode(lines, probe, probe < lines.length ? indentOf(stripComment(lines[probe])) : column);
      items.push(nested);
      index = next;
      continue;
    }
    // `- uses: x` opens a mapping whose later keys sit under the dash, so the
    // dash is rewritten as padding and the mapping is read from that column.
    if (/^(?:"[^"]*"|'[^']*'|[A-Za-z0-9_.$-]+)\s*:(\s|$)/.test(value)) {
      const patched = lines.slice();
      patched[index] = ' '.repeat(column) + value;
      const [mapping, next] = parseMapping(patched, index, column);
      items.push(mapping);
      index = next;
      continue;
    }
    items.push(value.startsWith('[') ? flowSequence(value) : unquote(value));
    index += 1;
  }
  return [items, index];
}

function parseMapping(lines: string[], start: number, indent: number): [YamlMap, number] {
  const mapping: YamlMap = {};
  let index = start;
  while (index < lines.length) {
    if (isBlank(lines[index])) { index += 1; continue; }
    const content = stripComment(lines[index]).trimEnd();
    if (indentOf(content) < indent) break;
    if (indentOf(content) > indent) throw new Error(`unexpected indent on workflow line ${index + 1}: ${content}`);
    const bare = content.trimStart();
    if (/^-(\s|$)/.test(bare)) break;
    const entry = /^("[^"]*"|'[^']*'|[^:]+?)\s*:(?:\s+([^]*))?$/.exec(bare);
    if (!entry) throw new Error(`cannot read workflow line ${index + 1}: ${content}`);
    const key = unquote(entry[1]);
    const value = (entry[2] ?? '').trim();
    if (/^[|>][-+]?\d*$/.test(value)) {
      const [text, next] = blockScalar(lines, index + 1, indentOf(content), value);
      mapping[key] = text;
      index = next;
      continue;
    }
    if (value !== '') {
      mapping[key] = value.startsWith('[') ? flowSequence(value) : unquote(value);
      index += 1;
      continue;
    }
    const probe = nextContent(lines, index + 1);
    const childIndent = probe < lines.length ? indentOf(stripComment(lines[probe])) : -1;
    const childIsOwnSequence = childIndent === indentOf(content) && /^-(\s|$)/.test(stripComment(lines[probe]).trim());
    if (childIndent > indentOf(content) || childIsOwnSequence) {
      const [nested, next] = parseNode(lines, probe, childIndent);
      mapping[key] = nested;
      index = next;
      continue;
    }
    mapping[key] = '';
    index += 1;
  }
  return [mapping, index];
}

function parseWorkflow(text: string): YamlMap {
  const [value] = parseMapping(text.replace(/\r\n/g, '\n').split('\n'), 0, 0);
  return value;
}

const asMap = (value: YamlValue | undefined): YamlMap =>
  value === undefined || typeof value === 'string' || Array.isArray(value) ? {} : value;
const asList = (value: YamlValue | undefined): YamlValue[] =>
  value === undefined ? [] : Array.isArray(value) ? value : [value];
const asText = (value: YamlValue | undefined): string => (typeof value === 'string' ? value : '');

const fixture = `
name: Fixture
# a whole-line comment

on:
  push:
    tags:
      - 'v*.*.*'
      - '!v*.*.*-vi20.*'

jobs:
  build:
    name: Build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@abc123 # v4
      - name: Generate version.json
        run: |
          VERSION="\${{ github.ref_name }}"
          # not a yaml comment
          cat > dist/version.json <<EOF
          {
            "tag": "\${VERSION}"
          }
          EOF

  desktop:
    if: \${{ vars.DESKTOP_RELEASE == 'true' }}
    runs-on: macos-14
    continue-on-error: true
    steps:
      - name: Package the macOS app
        working-directory: desktop
        run: npm run package:mac

  release:
    needs: [build, desktop]
    runs-on: ubuntu-latest
    steps:
      - uses: softprops/action-gh-release@v2
        with:
          files: |
            dist/vc-darwin-arm64
            dist/SHA256SUMS
          draft: false
`;

describe('the workflow reader', () => {
  const parsed = parseWorkflow(fixture);
  const jobs = asMap(parsed.jobs);

  it('reads a tag trigger, including the negated pattern', () => {
    expect(Object.keys(asMap(parsed.on))).toEqual(['push']);
    expect(asList(asMap(asMap(parsed.on).push).tags)).toEqual(['v*.*.*', '!v*.*.*-vi20.*']);
  });

  it('reads a flow-sequence `needs` and a job condition verbatim', () => {
    expect(asList(asMap(jobs.release).needs)).toEqual(['build', 'desktop']);
    expect(asText(asMap(jobs.desktop).if)).toBe("${{ vars.DESKTOP_RELEASE == 'true' }}");
    expect(asText(asMap(jobs.desktop)['continue-on-error'])).toBe('true');
  });

  it('keeps a heredoc, its "#" line and its shell interpolation intact', () => {
    const steps = asList(asMap(jobs.build).steps).map(asMap);
    expect(asText(steps[0].uses)).toBe('actions/checkout@abc123');
    expect(asText(steps[1].run).split('\n')).toEqual([
      'VERSION="${{ github.ref_name }}"',
      '# not a yaml comment',
      'cat > dist/version.json <<EOF',
      '{',
      '  "tag": "${VERSION}"',
      '}',
      'EOF',
    ]);
  });

  it('reads a release step’s file list as separate paths', () => {
    const step = asMap(asList(asMap(jobs.release).steps).map(asMap)[0]);
    expect(asText(asMap(step.with).files).split('\n').filter(Boolean))
      .toEqual(['dist/vc-darwin-arm64', 'dist/SHA256SUMS']);
  });
});

// ---------------------------------------------------------------------------
// An evaluator for the expression subset a gate is written in.
//
// "Off by default" cannot be pattern-matched. `if: ${{ vars.DESKTOP == 'true' }}`
// and `if: ${{ vars.DESKTOP != 'true' }}` differ by one character and mean
// opposite things; `if: ${{ !vars.SKIP_DESKTOP }}` mentions a variable and is on
// until someone disables it; `if: false` is off and always will be. The only
// way to tell them apart is to work out what each comes to, so the condition is
// EVALUATED here, twice: once with nothing set, once with the switch thrown.
//
// An expression this evaluator cannot read raises rather than returning false.
// A gate that silently read as false would be indistinguishable from a good one
// -- and it would be the pleasant answer, which is exactly why it must not be
// the default one.
// ---------------------------------------------------------------------------

type ExpressionValue = string | number | boolean;
type Context = { readonly [reference: string]: string };

function tokenize(source: string): string[] {
  const tokens: string[] = [];
  let index = 0;
  while (index < source.length) {
    const character = source[index];
    if (/\s/.test(character)) { index += 1; continue; }
    if (character === "'") {
      let cursor = index + 1;
      let text = '';
      for (;;) {
        if (cursor >= source.length) throw new Error(`unterminated string in expression: ${source}`);
        if (source[cursor] === "'" && source[cursor + 1] === "'") { text += "''"; cursor += 2; continue; }
        if (source[cursor] === "'") { cursor += 1; break; }
        text += source[cursor];
        cursor += 1;
      }
      tokens.push(`'${text}'`);
      index = cursor;
      continue;
    }
    const pair = source.slice(index, index + 2);
    if (['==', '!=', '&&', '||', '>=', '<='].includes(pair)) { tokens.push(pair); index += 2; continue; }
    if ('()!,<>'.includes(character)) { tokens.push(character); index += 1; continue; }
    const word = /^[A-Za-z0-9_.*-]+/.exec(source.slice(index));
    if (!word) throw new Error(`cannot read expression at «${source.slice(index)}»`);
    tokens.push(word[0]);
    index += word[0].length;
  }
  return tokens;
}

type Cursor = { readonly tokens: string[]; index: number };

const truthy = (value: ExpressionValue): boolean =>
  typeof value === 'boolean' ? value : typeof value === 'number' ? value !== 0 : value !== '';

const asString = (value: ExpressionValue): string =>
  typeof value === 'boolean' ? (value ? 'true' : 'false') : String(value);

// Anything under a context this evaluator knows and the caller did not set is
// unset: an undeclared repository variable is the empty string, and that is the
// state the "off by default" question is asked in. A bare word belonging to no
// context is a typo or a construct not modelled here, and raises.
const KNOWN_CONTEXTS = /^(?:github|vars|env|inputs|needs|matrix|runner|job|steps|secrets)\./;

function lookup(reference: string, context: Context): ExpressionValue {
  if (reference in context) return context[reference];
  if (KNOWN_CONTEXTS.test(reference)) return '';
  throw new Error(`unknown identifier «${reference}» in a workflow expression`);
}

// success() and always() hold unless the caller says the run is already broken.
function status(name: string, context: Context): boolean {
  const declared = context[`${name}()`];
  if (declared !== undefined) return declared !== '';
  return name === 'success' || name === 'always';
}

function callFunction(name: string, args: ExpressionValue[], context: Context): ExpressionValue {
  const text = args.map(asString);
  if (name === 'contains') return text[0].includes(text[1]);
  if (name === 'startsWith') return text[0].startsWith(text[1]);
  if (name === 'endsWith') return text[0].endsWith(text[1]);
  if (name === 'format') return text.slice(1).reduce((carried, value, position) => carried.replaceAll(`{${position}}`, value), text[0]);
  if (['success', 'failure', 'cancelled', 'always'].includes(name)) return status(name, context);
  throw new Error(`unsupported function «${name}()» in a workflow expression`);
}

function evaluateOr(cursor: Cursor, context: Context): ExpressionValue {
  let left = evaluateAnd(cursor, context);
  while (cursor.tokens[cursor.index] === '||') {
    cursor.index += 1;
    const right = evaluateAnd(cursor, context);
    left = truthy(left) ? left : right;
  }
  return left;
}

function evaluateAnd(cursor: Cursor, context: Context): ExpressionValue {
  let left = evaluateComparison(cursor, context);
  while (cursor.tokens[cursor.index] === '&&') {
    cursor.index += 1;
    const right = evaluateComparison(cursor, context);
    left = truthy(left) ? right : left;
  }
  return left;
}

function evaluateComparison(cursor: Cursor, context: Context): ExpressionValue {
  const left = evaluateUnary(cursor, context);
  const operator = cursor.tokens[cursor.index];
  if (operator !== '==' && operator !== '!=') return left;
  cursor.index += 1;
  const right = evaluateUnary(cursor, context);
  const equal = typeof left === typeof right ? left === right : asString(left) === asString(right);
  return operator === '==' ? equal : !equal;
}

function evaluateUnary(cursor: Cursor, context: Context): ExpressionValue {
  if (cursor.tokens[cursor.index] === '!') {
    cursor.index += 1;
    return !truthy(evaluateUnary(cursor, context));
  }
  return evaluatePrimary(cursor, context);
}

function evaluatePrimary(cursor: Cursor, context: Context): ExpressionValue {
  const token = cursor.tokens[cursor.index];
  if (token === undefined) throw new Error('a workflow expression ends where a value was expected');
  if (token === '(') {
    cursor.index += 1;
    const value = evaluateOr(cursor, context);
    if (cursor.tokens[cursor.index] !== ')') throw new Error('a workflow expression leaves a "(" unclosed');
    cursor.index += 1;
    return value;
  }
  cursor.index += 1;
  if (token.startsWith("'")) return token.slice(1, -1).replace(/''/g, "'");
  if (cursor.tokens[cursor.index] === '(') {
    cursor.index += 1;
    const args: ExpressionValue[] = [];
    while (cursor.tokens[cursor.index] !== ')') {
      if (cursor.index >= cursor.tokens.length) throw new Error(`«${token}(» is never closed`);
      args.push(evaluateOr(cursor, context));
      if (cursor.tokens[cursor.index] === ',') cursor.index += 1;
    }
    cursor.index += 1;
    return callFunction(token, args, context);
  }
  if (token === 'true') return true;
  if (token === 'false') return false;
  if (token === 'null') return '';
  if (/^\d+(?:\.\d+)?$/.test(token)) return Number(token);
  return lookup(token, context);
}

function evaluateExpression(source: string, context: Context): ExpressionValue {
  const cursor: Cursor = { tokens: tokenize(source), index: 0 };
  const value = evaluateOr(cursor, context);
  if (cursor.index < cursor.tokens.length) {
    throw new Error(`«${cursor.tokens.slice(cursor.index).join(' ')}» is left over in expression: ${source}`);
  }
  return value;
}

function interpolate(text: string, context: Context): string {
  return text.replace(/\$\{\{([\s\S]*?)\}\}/g, (_all, expression: string) => asString(evaluateExpression(expression, context)));
}

// A job or step with no `if:` runs.
function conditionHolds(condition: string, context: Context): boolean {
  const text = condition.trim();
  if (text === '') return true;
  const wrapped = /^\$\{\{([\s\S]*)\}\}$/.exec(text);
  return truthy(evaluateExpression(wrapped ? wrapped[1] : text, context));
}

// The state a release run is in when Maksim pushes an ordinary tag and nobody
// has set anything: this is where "off by default" is measured.
const PLAIN_TAG: Context = {
  'github.event_name': 'push',
  'github.ref': 'refs/tags/v1.4.2',
  'github.ref_name': 'v1.4.2',
  'github.ref_type': 'tag',
  'github.repository': 'makscee/void-code',
  'github.repository_owner': 'makscee',
  'github.actor': 'makscee',
};

const BROKEN_RUN: Context = { ...PLAIN_TAG, 'success()': '', 'failure()': 'true', 'cancelled()': '', 'always()': 'true' };

describe('the expression evaluator', () => {
  const withVariable = (name: string, value: string): Context => ({ ...PLAIN_TAG, [`vars.${name}`]: value });
  const withTag = (tag: string): Context => ({ ...PLAIN_TAG, 'github.ref_name': tag, 'github.ref': `refs/tags/${tag}` });

  const cases: ReadonlyArray<readonly [string, string, Context, boolean]> = [
    ['an unset variable compared to true is false', "vars.DESKTOP_RELEASE == 'true'", PLAIN_TAG, false],
    ['the same comparison once the variable is set', "vars.DESKTOP_RELEASE == 'true'", withVariable('DESKTOP_RELEASE', 'true'), true],
    ['a variable set to something else stays false', "vars.DESKTOP_RELEASE == 'true'", withVariable('DESKTOP_RELEASE', 'no'), false],
    ['a bare unset variable is false', 'vars.DESKTOP_RELEASE', PLAIN_TAG, false],
    ['a bare set variable is true', 'vars.DESKTOP_RELEASE', withVariable('DESKTOP_RELEASE', 'true'), true],
    ['"unless disabled" is on with nothing set', '!vars.SKIP_DESKTOP', PLAIN_TAG, true],
    ['a negated comparison is on with nothing set', "vars.DESKTOP_RELEASE != 'true'", PLAIN_TAG, true],
    ['the event of a tag push is always push', "github.event_name == 'push'", PLAIN_TAG, true],
    ['every release ref starts with refs/tags/v', "startsWith(github.ref, 'refs/tags/v')", PLAIN_TAG, true],
    ['a plain tag does not contain -desktop', "contains(github.ref_name, '-desktop')", PLAIN_TAG, false],
    ['a tag spelled -desktop does', "contains(github.ref_name, '-desktop')", withTag('v1.4.2-desktop'), true],
    ['endsWith reads the same tag', "endsWith(github.ref_name, '-desktop')", withTag('v1.4.2-desktop'), true],
    ['success() holds in a healthy run', 'success()', PLAIN_TAG, true],
    ['success() fails once something upstream failed', 'success()', BROKEN_RUN, false],
    ['always() holds anyway', 'always()', BROKEN_RUN, true],
    ['!cancelled() holds after a failure', '!cancelled()', BROKEN_RUN, true],
    ['failure() is false in a healthy run', 'failure()', PLAIN_TAG, false],
    ['a gated always-run job stays off with nothing set', "!cancelled() && vars.DESKTOP_RELEASE == 'true'", BROKEN_RUN, false],
    ['and comes on when it is set', "!cancelled() && vars.DESKTOP_RELEASE == 'true'", { ...BROKEN_RUN, 'vars.DESKTOP_RELEASE': 'true' }, true],
    ['parentheses bind before &&', "(vars.A == 'true' || vars.B == 'true') && !cancelled()", { ...PLAIN_TAG, 'vars.B': 'true' }, true],
    ['neither alternative set', "vars.A == 'true' || vars.B == 'true'", PLAIN_TAG, false],
    ['the literal false', 'false', PLAIN_TAG, false],
    ['the literal true', 'true', PLAIN_TAG, true],
  ];

  it.each(cases)('%s', (_name, expression, context, expected) => {
    expect(truthy(evaluateExpression(expression, context))).toBe(expected);
  });

  it('wraps and unwraps a ${{ }} condition the same way', () => {
    expect(conditionHolds("${{ vars.X == 'true' }}", PLAIN_TAG)).toBe(false);
    expect(conditionHolds("${{ vars.X == 'true' }}", { ...PLAIN_TAG, 'vars.X': 'true' })).toBe(true);
    expect(conditionHolds('', PLAIN_TAG)).toBe(true);
  });

  it('interpolates a file list, dropping what the switch leaves out', () => {
    const list = "dist/vc-darwin-arm64\n${{ vars.X == 'true' && 'dist/void-code-mac-arm64.zip' || '' }}\ndist/SHA256SUMS";
    expect(interpolate(list, PLAIN_TAG).split('\n').map((line) => line.trim()).filter(Boolean))
      .toEqual(['dist/vc-darwin-arm64', 'dist/SHA256SUMS']);
    expect(interpolate(list, { ...PLAIN_TAG, 'vars.X': 'true' }).split('\n').map((line) => line.trim()).filter(Boolean))
      .toEqual(['dist/vc-darwin-arm64', 'dist/void-code-mac-arm64.zip', 'dist/SHA256SUMS']);
    expect(interpolate('tag ${{ github.ref_name }}', PLAIN_TAG)).toBe('tag v1.4.2');
  });

  it('raises on what it cannot read, rather than answering "off"', () => {
    // The failure that would matter: an unreadable gate quietly reading as
    // false, and passing every test below while the job runs on every tag.
    expect(() => evaluateExpression("hashFiles('desktop/**') != ''", PLAIN_TAG)).toThrow(/unsupported function/);
    expect(() => evaluateExpression('desktop_release', PLAIN_TAG)).toThrow(/unknown identifier/);
    expect(() => evaluateExpression("vars.X == 'true' vars.Y", PLAIN_TAG)).toThrow(/left over/);
  });
});

// ---------------------------------------------------------------------------
// "Off by default" as a verdict.
//
// A gate has to fail two ways to be one. It must be false with nothing set --
// otherwise merging it publishes an installer on Maksim's next tag, which is
// the thing we are forbidden to do. And it must be capable of being true --
// otherwise `if: false` passes, the desktop half never runs, and the workflow
// carries dead YAML that reads like a feature.
//
// What counts as "the owner said so" is deliberately not enumerated here. A
// repository variable he sets and a tag he spells differently are both his
// decision; the test derives the candidate settings from whatever the workflow
// itself mentions, so the design stays the implementer's to choose.
// ---------------------------------------------------------------------------

type Switch = readonly [string, Context];

const withBrokenRun = (context: Context): Context =>
  ({ ...context, 'success()': '', 'failure()': 'true', 'cancelled()': '', 'always()': 'true' });

function optInSettings(text: string): Switch[] {
  const variables = Array.from(new Set(Array.from(text.matchAll(/\bvars\.([A-Za-z_][A-Za-z_0-9]*)/g), (found) => found[1])));
  const spellings = Array.from(new Set(Array.from(
    text.matchAll(/(?:contains|startsWith|endsWith)\(\s*github\.ref(?:_name)?\s*,\s*'([^']+)'/g),
    (found) => found[1],
  ))).filter((literal) => !literal.startsWith('refs/'));
  const everyVariable = Object.fromEntries(variables.map((name) => [`vars.${name}`, 'true']));
  const settings: Switch[] = variables.map((name) => [`vars.${name} set to true`, { ...PLAIN_TAG, [`vars.${name}`]: 'true' }]);
  if (variables.length > 1) settings.push(['every repository variable set', { ...PLAIN_TAG, ...everyVariable }]);
  for (const spelling of spellings) {
    const tag = `v1.4.2${spelling}`;
    settings.push([`the tag ${tag}`, { ...PLAIN_TAG, ...everyVariable, 'github.ref_name': tag, 'github.ref': `refs/tags/${tag}` }]);
  }
  return settings;
}

type GateVerdict = { readonly gated: boolean; readonly reason: string };

function gateVerdict(condition: string, settings: readonly Switch[]): GateVerdict {
  if (condition.trim() === '') return { gated: false, reason: 'the job carries no `if:` at all, so it runs on every tag Maksim pushes' };
  let runsAnyway: boolean;
  try {
    runsAnyway = conditionHolds(condition, PLAIN_TAG);
  } catch (error) {
    return { gated: false, reason: `the condition cannot be read, so it cannot be shown to be off: ${(error as Error).message}` };
  }
  if (runsAnyway) return { gated: false, reason: `the condition is already true on a plain tag, so this is on by default: ${condition}` };
  const opened = settings.filter(([, context]) => {
    try {
      return conditionHolds(condition, context);
    } catch {
      return false;
    }
  });
  if (opened.length === 0) return { gated: false, reason: `nothing in this workflow can turn the condition on, so the job is dead YAML: ${condition}` };
  return { gated: true, reason: `off by default, on under ${opened.map(([name]) => name).join(' or ')}` };
}

describe('the "off by default" rule', () => {
  const settings = optInSettings("vars.DESKTOP_RELEASE contains(github.ref_name, '-desktop')");

  const accepted: ReadonlyArray<readonly [string, string]> = [
    ['a repository variable compared to true', "${{ vars.DESKTOP_RELEASE == 'true' }}"],
    ['the same condition unwrapped', "vars.DESKTOP_RELEASE == 'true'"],
    ['a bare repository variable', '${{ vars.DESKTOP_RELEASE }}'],
    ['a tag spelled differently', "${{ contains(github.ref_name, '-desktop') }}"],
    ['a gate that also survives an upstream failure', "${{ !cancelled() && vars.DESKTOP_RELEASE == 'true' }}"],
  ];

  const rejected: ReadonlyArray<readonly [string, string, string]> = [
    ['no condition at all', '', 'no `if:` at all'],
    ['the implicit condition spelled out', '${{ success() }}', 'already true on a plain tag'],
    ['a condition about the event', "${{ github.event_name == 'push' }}", 'already true on a plain tag'],
    ['a condition about the ref that every release has', "${{ startsWith(github.ref, 'refs/tags/') }}", 'already true on a plain tag'],
    ['opt-out rather than opt-in', '${{ !vars.SKIP_DESKTOP }}', 'already true on a plain tag'],
    ['the comparison written the wrong way round', "${{ vars.DESKTOP_RELEASE != 'true' }}", 'already true on a plain tag'],
    ['a switch nothing can throw', '${{ false }}', 'dead YAML'],
    ['a variable this workflow never mentions', "${{ vars.SOMETHING_ELSE == 'true' }}", 'dead YAML'],
    ['a condition the evaluator cannot read', "${{ hashFiles('desktop/**') != '' }}", 'cannot be read'],
  ];

  it.each(accepted)('accepts %s', (_name, condition) => {
    expect(gateVerdict(condition, settings).reason).toContain('off by default');
    expect(gateVerdict(condition, settings).gated).toBe(true);
  });

  it.each(rejected)('rejects %s', (_name, condition, because) => {
    const verdict = gateVerdict(condition, settings);
    expect(verdict.reason).toContain(because);
    expect(verdict.gated).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// "A failure here does not withhold the CLI" as a verdict.
//
// The release chain is linear today: test and the pinned-Pi smoke, then build,
// then release, then publish-auth -- and publish-auth is self-update, the step
// that decides what an installed `vc` upgrades itself to. A desktop job wired
// into that line with a plain `needs:` stops all of it the first time
// electron-builder has a bad day on a runner, and the CLI release that had
// nothing to do with the desktop app never happens.
//
// GitHub skips a job whose needed job failed, unless the dependent's own
// condition survives a failure (`always()`, `!cancelled()`) or the failing job
// declares continue-on-error, which reports its failure as success. So the
// question is answered by walking `needs` backwards and evaluating each
// condition in a run where something upstream has already failed.
// ---------------------------------------------------------------------------

type Link = { readonly needs: readonly string[]; readonly condition: string; readonly tolerated: boolean };
type Chain = { readonly [job: string]: Link };
type BlockVerdict = { readonly blocked: boolean; readonly reason: string };

function chainOf(jobs: YamlMap, context: Context): Chain {
  const chain: { [job: string]: Link } = {};
  for (const [name, value] of Object.entries(jobs)) {
    const job = asMap(value);
    const tolerance = asText(job['continue-on-error']);
    chain[name] = {
      needs: asList(job.needs).map(asText).filter((need) => need !== ''),
      condition: asText(job.if),
      tolerated: tolerance !== '' && conditionHolds(tolerance, context),
    };
  }
  return chain;
}

function blockVerdict(chain: Chain, target: string, source: string, context: Context): BlockVerdict {
  if (!(source in chain)) return { blocked: false, reason: `there is no job called ${source}` };
  if (!(target in chain)) return { blocked: false, reason: `there is no job called ${target}` };
  if (chain[source].tolerated) return { blocked: false, reason: `${source} carries continue-on-error, so a failure there is reported as success` };
  const broken = withBrokenRun(context);
  // A job with no `if:` carries the implicit `success()`, which is exactly the
  // condition that fails when something it needs has failed.
  const skipped = (job: string) => !conditionHolds(chain[job].condition === '' ? 'success()' : chain[job].condition, broken);
  const visiting = new Set<string>();
  const route = (job: string): string[] | null => {
    if (job === source) return [source];
    if (visiting.has(job)) return null;
    visiting.add(job);
    for (const need of chain[job].needs) {
      if (!(need in chain)) continue;
      const upstream = route(need);
      if (upstream !== null && skipped(job)) return [...upstream, job];
    }
    visiting.delete(job);
    return null;
  };
  const found = route(target);
  return found === null
    ? { blocked: false, reason: `${target} is not waiting on ${source} in a way a failure can skip` }
    : { blocked: true, reason: `a failure of ${source} skips ${found.join(' → ')}` };
}

describe('the "does not withhold the CLI" rule', () => {
  const chainFixture = (jobs: string) => chainOf(asMap(parseWorkflow(`jobs:\n${jobs}`).jobs), PLAIN_TAG);

  const linear = `
  desktop:
    runs-on: macos-14
  build:
    runs-on: ubuntu-latest
  release:
    needs: [build, desktop]
    runs-on: ubuntu-latest
`;
  const tolerated = `
  desktop:
    runs-on: macos-14
    continue-on-error: true
  build:
    runs-on: ubuntu-latest
  release:
    needs: [build, desktop]
    runs-on: ubuntu-latest
`;
  const survives = `
  desktop:
    runs-on: macos-14
  build:
    runs-on: ubuntu-latest
  release:
    needs: [build, desktop]
    if: \${{ !cancelled() }}
    runs-on: ubuntu-latest
`;
  const alwaysRuns = `
  desktop:
    runs-on: macos-14
  release:
    needs: [desktop]
    if: always()
    runs-on: ubuntu-latest
`;
  const unrelated = `
  desktop:
    runs-on: macos-14
  build:
    runs-on: ubuntu-latest
  release:
    needs: [build]
    runs-on: ubuntu-latest
`;
  const throughAnother = `
  desktop:
    runs-on: macos-14
  bundle:
    needs: [desktop]
    runs-on: ubuntu-latest
  release:
    needs: [bundle]
    runs-on: ubuntu-latest
`;
  const throughAnotherThatSurvives = `
  desktop:
    runs-on: macos-14
  bundle:
    needs: [desktop]
    if: \${{ !cancelled() }}
    runs-on: ubuntu-latest
  release:
    needs: [bundle]
    runs-on: ubuntu-latest
`;

  const cases: ReadonlyArray<readonly [string, string, boolean]> = [
    ['a plain needs edge blocks', linear, true],
    ['continue-on-error on the desktop job does not', tolerated, false],
    ['a dependent that survives a failure does not', survives, false],
    ['always() on the dependent does not', alwaysRuns, false],
    ['no edge at all does not', unrelated, false],
    ['a needs edge through a third job still blocks', throughAnother, true],
    ['unless that third job survives the failure', throughAnotherThatSurvives, false],
  ];

  it.each(cases)('%s', (_name, jobs, expected) => {
    expect(blockVerdict(chainFixture(jobs), 'release', 'desktop', PLAIN_TAG).blocked).toBe(expected);
  });

  it('names the route it found, so a failure says where to look', () => {
    expect(blockVerdict(chainFixture(throughAnother), 'release', 'desktop', PLAIN_TAG).reason)
      .toBe('a failure of desktop skips desktop → bundle → release');
  });
});

// ---------------------------------------------------------------------------
// The real workflow.
// ---------------------------------------------------------------------------

const workflowText = (name: string) => readFileSync(new URL(`../../.github/workflows/${name}`, import.meta.url), 'utf8');
const workflow = (name: string) => parseWorkflow(workflowText(name));

const releaseText = workflowText('release.yml');
const releaseWorkflow = workflow('release.yml');
const releaseJobs = asMap(releaseWorkflow.jobs);
const settings = optInSettings(releaseText);

// A census of release.yml as it stands on 2026-08-23, which is the whole point:
// after the desktop half is added, an ordinary tag must still produce exactly
// this and nothing else.
const TODAYS_JOBS = ['build', 'desktop-pinned-pi-smoke', 'publish-auth', 'release', 'test'];
const TODAYS_ASSETS = [
  'dist/SHA256SUMS',
  'dist/vc-darwin-amd64',
  'dist/vc-darwin-arm64',
  'dist/vc-linux-amd64',
  'dist/vc-linux-arm64',
  'dist/vc-windows-amd64.exe',
  'dist/vc-windows-arm64.exe',
  'dist/version.json',
];

type Resolved = {
  readonly name: string;
  readonly condition: string;
  readonly steps: YamlMap[];
  readonly runners: string[];
  readonly transcript: string;
};

// A job may hold its own steps or call a reusable workflow in this repository;
// both are read, so the choice between inlining the desktop steps and reusing
// the ones test.yml already has stays the implementer's.
function resolveJob(name: string, value: YamlValue): Resolved {
  const job = asMap(value);
  const called = asText(job.uses);
  const bodies = called.startsWith('./.github/workflows/')
    ? Object.values(asMap(workflow(called.slice('./.github/workflows/'.length).split('@')[0]).jobs)).map(asMap)
    : [job];
  const steps = bodies.flatMap((body) => asList(body.steps).map(asMap));
  return {
    name,
    condition: asText(job.if),
    steps,
    runners: bodies.map((body) => asText(body['runs-on'])).filter((runner) => runner !== ''),
    transcript: steps.map((step) => `${asText(step.uses)}\n${asText(step.run)}`).join('\n \n'),
  };
}

const jobs = Object.entries(releaseJobs).map(([name, value]) => resolveJob(name, value));

const packagesMac = (job: Resolved) => /\bpackage:mac\b/.test(job.transcript);
const packagesWindows = (job: Resolved) => /\bpackage:win\b/.test(job.transcript);
const buildsDesktop = (job: Resolved) => packagesMac(job) || packagesWindows(job) || /(?:^|[\s/])electron-builder\s/.test(job.transcript);

const macJob = jobs.find(packagesMac);
const windowsJob = jobs.find(packagesWindows);
const desktopJobs = jobs.filter(buildsDesktop);

const macMissing = 'no job in .github/workflows/release.yml packages the macOS app with `npm run package:mac`';
const windowsMissing = 'no job in .github/workflows/release.yml packages the Windows installer with `npm run package:win`';
const macReason = (verdict: string) => (macJob ? verdict : macMissing);
const windowsReason = (verdict: string) => (windowsJob ? verdict : windowsMissing);

const RELEASE_ACTION = /softprops\/action-gh-release|ncipollo\/release-action|actions\/create-release/;
// Not `.exe`: six of today's CLI assets end in it. What a desktop build leaves
// behind is a bundle archive, a disk image, or a named installer.
const DESKTOP_ASSET = /\.(?:app|dmg|msi|zip|tar\.gz)\b|\bvoid-code-(?:mac|windows)|\bwin-unpacked\b|installer|Void-Code-[^\s/]*\.exe/i;

const lines = (text: string) => text.split('\n').map((line) => line.trim()).filter((line) => line !== '');
const isDesktopAsset = (path: string) => !TODAYS_ASSETS.includes(path) && DESKTOP_ASSET.test(path);

// Everything that would end up attached to the release in a run of this shape:
// the file list of a release action, and any `gh release upload` -- the second
// route into the same release, and the one a rule that only read `files:` would
// miss.
function publishedAssets(context: Context): string[] {
  const assets: string[] = [];
  for (const job of jobs) {
    if (!conditionHolds(job.condition, context)) continue;
    for (const step of job.steps) {
      if (!conditionHolds(asText(step.if), context)) continue;
      if (RELEASE_ACTION.test(asText(step.uses))) {
        assets.push(...lines(interpolate(asText(asMap(step.with).files), context)));
      }
      const script = interpolate(asText(step.run), context);
      for (const call of script.matchAll(/\bgh\s+release\s+(?:upload|create)\s+\S+([^\n]*)/g)) {
        assets.push(...call[1].split(/\s+/).filter((word) => word !== '' && !word.startsWith('-')));
      }
    }
  }
  return assets;
}

function provenanceSubjects(context: Context): string[] {
  const subjects: string[] = [];
  for (const job of jobs) {
    if (!conditionHolds(job.condition, context)) continue;
    for (const step of job.steps) {
      if (!conditionHolds(asText(step.if), context)) continue;
      if (!/actions\/attest-build-provenance/.test(asText(step.uses))) continue;
      subjects.push(...lines(interpolate(asText(asMap(step.with)['subject-path']), context)));
    }
  }
  return subjects;
}

describe('release.yml can carry the desktop builds', () => {
  it('has a job that packages the macOS app', () => {
    expect(macJob?.name ?? macMissing).not.toBe(macMissing);
  });

  it('has a job that packages the Windows installer', () => {
    expect(windowsJob?.name ?? windowsMissing).not.toBe(windowsMissing);
  });

  it('builds each one where that platform can be built at all', () => {
    // electron-builder writes an NSIS installer only on Windows, and
    // scripts/assemble-windows-resources.mjs refuses to run anywhere else.
    expect(macReason(macJob?.runners.join(', ') ?? '')).toMatch(/macos-/);
    expect(windowsReason(windowsJob?.runners.join(', ') ?? '')).toMatch(/windows-/);
  });

  it('packages through the npm scripts, never electron-builder by hand', () => {
    // package:mac and package:win are build, then the assembly, then the
    // builder. Calling the builder directly skips the assembly, and the
    // assembly is what stages the private runtime the app cannot start without.
    const byHand = desktopJobs
      .flatMap((job) => job.steps.map((step) => asText(step.run)))
      .filter((script) => /(?:^|[\s/])electron-builder\s/.test(script));
    expect(desktopJobs.length === 0 ? macMissing : byHand.join(' | ') || 'through the npm scripts').toBe('through the npm scripts');
  });

  it('has a setting under which a desktop asset reaches the release', () => {
    // Without this the rest of the file could be satisfied by a gate nobody can
    // ever open, and a release that never gains an installer would read green.
    const nothing = 'nothing this workflow mentions -- no repository variable, no tag spelling -- puts a desktop asset into the release';
    const opened = settings.filter(([, context]) => publishedAssets(context).some(isDesktopAsset));
    expect(opened.map(([name]) => name).join(' or ') || nothing).not.toBe(nothing);
  });
});

describe('and does none of it on an ordinary tag', () => {
  it('runs exactly the five jobs that run today', () => {
    const running = jobs.filter((job) => conditionHolds(job.condition, PLAIN_TAG)).map((job) => job.name);
    expect(running.slice().sort()).toEqual(TODAYS_JOBS);
  });

  it('leaves the macOS job switched off', () => {
    const verdict = macJob ? gateVerdict(macJob.condition, settings) : { gated: false, reason: macMissing };
    expect(verdict.gated ? 'off by default' : verdict.reason).toBe('off by default');
  });

  it('leaves the Windows job switched off', () => {
    const verdict = windowsJob ? gateVerdict(windowsJob.condition, settings) : { gated: false, reason: windowsMissing };
    expect(verdict.gated ? 'off by default' : verdict.reason).toBe('off by default');
  });

  it('publishes exactly the eight assets it publishes today', () => {
    expect(publishedAssets(PLAIN_TAG).slice().sort()).toEqual(TODAYS_ASSETS);
  });

  it('signs exactly the eight subjects it signs today', () => {
    expect(provenanceSubjects(PLAIN_TAG).slice().sort()).toEqual(TODAYS_ASSETS);
  });

  it('sends nothing desktop into void-auth, where installed clients self-update from', () => {
    const sync = jobs.find((job) => job.name === 'publish-auth');
    const script = (sync?.steps ?? [])
      .filter((step) => conditionHolds(asText(step.if), PLAIN_TAG))
      .map((step) => `${asText(step.uses)} ${interpolate(asText(step.run), PLAIN_TAG)} ${interpolate(asText(asMap(step.with).fileName), PLAIN_TAG)}`)
      .join('\n');
    const named = lines(script).filter((line) => DESKTOP_ASSET.test(line));
    expect(sync === undefined ? 'there is no publish-auth job any more' : named.join(' | ') || 'the CLI only').toBe('the CLI only');
  });
});

describe('a failed desktop build does not withhold the CLI', () => {
  const desktopEnabled = (settings.find(([, context]) => desktopJobs.some((job) => conditionHolds(job.condition, context)))
    ?? ['nothing set', PLAIN_TAG] as Switch)[1];
  const chain = chainOf(releaseJobs, desktopEnabled);
  const noDesktop = 'no job in .github/workflows/release.yml builds the desktop app, so there is nothing to be tolerant of yet';

  it.each(['build', 'release', 'publish-auth'])('%s runs anyway', (cliJob) => {
    const blocked = desktopJobs
      .map((job) => blockVerdict(chain, cliJob, job.name, desktopEnabled))
      .filter((verdict) => verdict.blocked);
    // A renamed job would otherwise leave this asking about nothing and
    // answering "fine", which is the one answer it must never give for free.
    const verdict = !(cliJob in chain) ? `there is no job called ${cliJob} in release.yml any more`
      : desktopJobs.length === 0 ? noDesktop
        : blocked.map((blocker) => blocker.reason).join(' | ') || 'runs anyway';
    expect(verdict).toBe('runs anyway');
  });
});

describe('the way in stays a tag Maksim pushes', () => {
  const triggers = asMap(releaseWorkflow.on);

  it('fires on a tag push and on nothing else', () => {
    expect(Object.keys(triggers)).toEqual(['push']);
    expect(Object.keys(asMap(triggers.push))).toEqual(['tags']);
  });

  it('keeps the same tag patterns, canaries still excluded', () => {
    expect(asList(asMap(triggers.push).tags)).toEqual(['v*.*.*', '!v*.*.*-vi20.*']);
  });

  it.each(['workflow_dispatch', 'workflow_call', 'repository_dispatch', 'schedule', 'pull_request'])(
    'has no %s anywhere in the file',
    (event) => {
      expect(releaseText.includes(event)).toBe(false);
    },
  );

  it('adds no second workflow a tag can start', () => {
    const startedByTag = readdirSync(new URL('../../.github/workflows/', import.meta.url))
      .filter((file) => file.endsWith('.yml') || file.endsWith('.yaml'))
      .filter((file) => asList(asMap(asMap(workflow(file).on).push).tags).length > 0);
    expect(startedByTag.sort()).toEqual(['canary-release.yml', 'release.yml']);
  });
});

describe('one release, and no tag of ours', () => {
  const releasing = jobs.flatMap((job) => job.steps.map((step) => ({ job: job.name, step })))
    .filter(({ step }) => RELEASE_ACTION.test(asText(step.uses)) || /\bgh\s+release\s+create\b/.test(asText(step.run)));

  it('creates the release in exactly one place, the one already there', () => {
    expect(releasing.map(({ job, step }) => `${job}: ${asText(step.name) || asText(step.uses)}`))
      .toEqual(['release: Create GitHub Release']);
  });

  it('leaves that step ungated, so the CLI release still happens on a plain tag', () => {
    const step = releasing[0]?.step;
    const owner = jobs.find((job) => job.name === releasing[0]?.job);
    const runs = step !== undefined && owner !== undefined
      && conditionHolds(owner.condition, PLAIN_TAG) && conditionHolds(asText(step.if), PLAIN_TAG);
    expect(runs ? 'runs on a plain tag' : 'the release step no longer runs on an ordinary tag').toBe('runs on a plain tag');
  });

  const forbidden: ReadonlyArray<readonly [string, RegExp]> = [
    ['a second release action', /ncipollo\/release-action|actions\/create-release/],
    ['`gh release create`', /\bgh\s+release\s+create\b/],
    ['tag creation', /\bgit\s+tag\b/],
    ['a push of tags', /\bgit\s+push\b[^\n]*(?:--tags|--follow-tags|\stag)/],
    ['an electron-builder publish', /--publish(?:=|\s+)(?:always|onTag|onTagOrDraft)/],
  ];

  it.each(forbidden)('the workflow contains no %s', (_name, pattern) => {
    expect(pattern.test(releaseText) ? `found ${pattern}` : 'absent').toBe('absent');
  });
});
