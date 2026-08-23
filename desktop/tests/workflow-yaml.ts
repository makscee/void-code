// A reader for the workflow subset the .github/workflows files are written in.
//
// js-yaml sits in node_modules only as a transitive dependency of eslint and
// electron-builder; a test that reaches for it would break the day either one
// drops it. So the subset is read here, by hand.
//
// This lived in three copies -- mac-app-artifact-workflow.test.ts,
// windows-app-artifact-workflow.test.ts and
// release-desktop-optin-workflow.test.ts -- and the windows copy exported it so
// the others could import. That was measured and rejected: importing from a
// *.test.ts file drags that file's own describe() blocks into the importer,
// vitest collects them again, and every assertion in it runs and reports twice.
// A plain module has no describe() to drag, so the three copies become one and
// the suite counts each assertion once.
//
// It is a reader, not a validator: it is never trusted on its own. Each test
// file that imports it still shows it reading a fixture written around the
// shapes that file cares about, before any claim is made about the real
// workflows.

export type YamlValue = string | YamlValue[] | { [key: string]: YamlValue };
export type YamlMap = { [key: string]: YamlValue };

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

export function parseWorkflow(text: string): YamlMap {
  const [value] = parseMapping(text.replace(/\r\n/g, '\n').split('\n'), 0, 0);
  return value;
}

export const asMap = (value: YamlValue | undefined): YamlMap =>
  value === undefined || typeof value === 'string' || Array.isArray(value) ? {} : value;
export const asList = (value: YamlValue | undefined): YamlValue[] =>
  value === undefined ? [] : Array.isArray(value) ? value : [value];
export const asText = (value: YamlValue | undefined): string => (typeof value === 'string' ? value : '');
