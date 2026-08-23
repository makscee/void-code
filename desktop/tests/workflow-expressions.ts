// An evaluator for the expression subset a workflow condition is written in.
//
// A gate cannot be pattern-matched. `if: ${{ vars.DESKTOP == 'true' }}` and
// `if: ${{ vars.DESKTOP != 'true' }}` differ by one character and mean opposite
// things; `if: ${{ !vars.SKIP_DESKTOP }}` mentions a variable and is on until
// someone disables it; `if: ${{ false }}` is off and always will be, while
// naming neither a variable nor a ref. The only way to tell them apart is to
// work out what each comes to, so a condition is EVALUATED, never matched.
//
// An expression this evaluator cannot read raises rather than returning false.
// A condition that silently read as false would be indistinguishable from a
// good one -- and it would be the pleasant answer, which is exactly why it must
// not be the default one.
//
// Like tests/workflow-yaml.ts this is a plain module and not a *.test.ts:
// importing from a test file drags that file's describe() blocks into the
// importer and every assertion in it runs twice. The self-tests that show this
// evaluator answering correctly live in release-desktop-optin-workflow.test.ts,
// which is where the rule that needs them lives.

export type ExpressionValue = string | number | boolean;
export type Context = { readonly [reference: string]: string };

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

export const truthy = (value: ExpressionValue): boolean =>
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

export function evaluateExpression(source: string, context: Context): ExpressionValue {
  const cursor: Cursor = { tokens: tokenize(source), index: 0 };
  const value = evaluateOr(cursor, context);
  if (cursor.index < cursor.tokens.length) {
    throw new Error(`«${cursor.tokens.slice(cursor.index).join(' ')}» is left over in expression: ${source}`);
  }
  return value;
}

export function interpolate(text: string, context: Context): string {
  return text.replace(/\$\{\{([\s\S]*?)\}\}/g, (_all, expression: string) => asString(evaluateExpression(expression, context)));
}

// A job or step with no `if:` runs.
export function conditionHolds(condition: string, context: Context): boolean {
  const text = condition.trim();
  if (text === '') return true;
  const wrapped = /^\$\{\{([\s\S]*)\}\}$/.exec(text);
  return truthy(evaluateExpression(wrapped ? wrapped[1] : text, context));
}


// The contexts a condition is measured in. A gate is only ever "off" or "on"
// relative to one of these; asking without one is how `if: ${{ false }}` passes
// for a gate.

// What a release run is in when Maksim pushes an ordinary tag and nobody has
// set anything: this is where "off by default" is measured.
export const PLAIN_TAG: Context = {
  'github.event_name': 'push',
  'github.ref': 'refs/tags/v1.4.2',
  'github.ref_name': 'v1.4.2',
  'github.ref_type': 'tag',
  'github.repository': 'makscee/void-code',
  'github.repository_owner': 'makscee',
  'github.actor': 'makscee',
};

// The events test.yml fires on. A job that CI is supposed to run every time has
// to come out true in all of them, and the list is written out rather than
// summarised because "every branch push" is a claim about more than one branch.
export const BRANCH_EVENTS: ReadonlyArray<readonly [string, Context]> = [
  ['a push to work/desktop-auth-ui', {
    'github.event_name': 'push',
    'github.ref': 'refs/heads/work/desktop-auth-ui',
    'github.ref_name': 'work/desktop-auth-ui',
    'github.ref_type': 'branch',
    'github.repository': 'makscee/void-code',
    'github.repository_owner': 'makscee',
    'github.actor': 'makscee',
  }],
  ['a push to fix/whatever', {
    'github.event_name': 'push',
    'github.ref': 'refs/heads/fix/whatever',
    'github.ref_name': 'fix/whatever',
    'github.ref_type': 'branch',
    'github.repository': 'makscee/void-code',
    'github.repository_owner': 'makscee',
    'github.actor': 'makscee',
  }],
  ['a pull request', {
    'github.event_name': 'pull_request',
    'github.ref': 'refs/pull/12/merge',
    'github.ref_name': '12/merge',
    'github.ref_type': 'branch',
    'github.base_ref': 'main',
    'github.head_ref': 'work/desktop-auth-ui',
    'github.repository': 'makscee/void-code',
    'github.repository_owner': 'makscee',
    'github.actor': 'makscee',
  }],
];
