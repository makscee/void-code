import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import pins from '../scripts/resource-pins.json';

// A person needs a link, a download, and a working app. Today CI produces the
// CLI for six platforms and never once invokes electron-builder, so there is
// nothing to hand over. This file states the shape of the job that fixes that:
// on every branch push, build the macOS app and leave it as a run artifact.
//
// It states just as firmly what the job must NOT become. Publishing installers
// and cutting releases are forbidden to us, and tags are Maksim's. So the job
// lives in test.yml -- which fires on branch pushes and pull requests -- and
// never in release.yml or canary-release.yml, which fire on tags. The absence
// of any release action, of `gh release`, of tag creation, and of write
// permission is asserted, not assumed.
//
// What this file cannot do: it cannot run GitHub Actions. It fixes the FORM of
// the workflow -- that the job exists, where it lives, what it builds with,
// what it refuses to publish. It does not prove the build passes on a runner.
// That remains unverified here, deliberately and visibly.

// ---------------------------------------------------------------------------
// A reader for the workflow subset these files are written in.
//
// js-yaml sits in node_modules only as a transitive dependency of eslint and
// electron-builder; a test that reaches for it would break the day either one
// drops it. So the subset is read here, and -- as with the npm provenance rule
// next door -- the reader is first shown to read a fixture correctly, before
// any claim is made about the real files.
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
    branches-ignore: ['main', "release"]
  pull_request:

jobs:
  build:
    runs-on: macos-14
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@abc123 # v4
        with:
          fetch-depth: 0
      - name: Two things
        run: |
          echo one
          # not a yaml comment
          echo two
      - uses: actions/setup-node@def456
        with:
          node-version: '22.23.1'
          cache-dependency-path: |
            desktop/package-lock.json
            desktop/runtime/pi/package-lock.json
`;

describe('the workflow reader', () => {
  const parsed = parseWorkflow(fixture);
  const steps = asList(asMap(asMap(parsed.jobs).build).steps).map(asMap);

  it('reads triggers, including a flow sequence and a valueless key', () => {
    expect(asList(asMap(asMap(parsed.on).push)['branches-ignore'])).toEqual(['main', 'release']);
    expect(Object.keys(asMap(parsed.on))).toEqual(['push', 'pull_request']);
  });

  it('reads a job, its runner, its permissions and its step count', () => {
    expect(asText(asMap(asMap(parsed.jobs).build)['runs-on'])).toBe('macos-14');
    expect(asText(asMap(asMap(asMap(parsed.jobs).build).permissions).contents)).toBe('read');
    expect(steps).toHaveLength(3);
  });

  it('drops trailing comments but keeps a "#" line inside a block scalar', () => {
    expect(asText(steps[0].uses)).toBe('actions/checkout@abc123');
    expect(asText(steps[1].run)).toBe('echo one\n# not a yaml comment\necho two');
  });

  it('reads a nested with: block and a multi-line path list', () => {
    expect(asText(asMap(steps[0].with)['fetch-depth'])).toBe('0');
    expect(asText(asMap(steps[2].with)['node-version'])).toBe('22.23.1');
    expect(asText(asMap(steps[2].with)['cache-dependency-path']).split('\n'))
      .toEqual(['desktop/package-lock.json', 'desktop/runtime/pi/package-lock.json']);
  });
});

// ---------------------------------------------------------------------------
// The symlink trap.
//
// A macOS .app is a directory whose Contents/Frameworks holds symlinks --
// `Versions/Current` pointing at `Versions/A`, and the framework binaries and
// Resources linked back out of it. An artifact upload transports files; a
// directory handed to it straight cannot be relied on to carry links across as
// links, and a bundle whose links arrived as copies -- or did not arrive -- is
// not an app that launches.
//
// The fix is to stop depending on the upload's behaviour at all: pack the
// bundle first, with a tool that stores a symlink as a symlink, and upload the
// single archive file. Three tools qualify on a macOS runner, each with its own
// way of being got wrong:
//
//   ditto -c -k   creates a zip, keeps symlinks -- but WITHOUT --keepParent it
//                 zips the CONTENTS of the .app and the bundle wrapper is lost.
//   zip -r        follows symlinks and inflates copies unless given -y.
//   tar -c        stores symlinks by default, and resolves them under -h.
//
// The rule below accepts those three shapes and rejects their broken spellings.
// It is exercised against both tables before it is pointed at the real job, so
// a rule that always answered "no" could not masquerade as a passing suite.
// ---------------------------------------------------------------------------

type ArchiveVerdict = { readonly preserved: boolean; readonly reason: string };

const dereferences = /(?:^|\s)(?:-[a-zA-Z]*h[a-zA-Z]*|--dereference)(?:\s|$)/;
const storesSymlinks = /(?:^|\s)(?:-[a-zA-Z]*y[a-zA-Z]*|--symlinks)(?:\s|$)/;

function archiveVerdict(script: string): ArchiveVerdict {
  const commands = script
    .replace(/\\\n\s*/g, ' ')
    .split(/\n|&&|\|\||;/)
    .map((command) => command.trim())
    .filter((command) => command !== '');
  const packing = commands.filter((command) => /\.app\b/.test(command) && /\b(?:ditto|zip|tar|cp|rsync|mv)\b/.test(command));
  if (packing.length === 0) return { preserved: false, reason: 'nothing packs the .app bundle into an archive before the upload' };

  for (const command of packing) {
    if (/\bditto\b/.test(command)) {
      if (!/(?:^|\s)-[a-zA-Z]*c/.test(command) || !/(?:^|\s)-[a-zA-Z]*k/.test(command)) {
        return { preserved: false, reason: `ditto copies rather than archives here: ${command}` };
      }
      if (!/--keepParent\b/.test(command)) {
        return { preserved: false, reason: `ditto without --keepParent archives the contents and drops the .app wrapper: ${command}` };
      }
      return { preserved: true, reason: 'ditto -c -k --keepParent stores the bundle with its symlinks' };
    }
    if (/\btar\b/.test(command)) {
      if (dereferences.test(command)) return { preserved: false, reason: `tar resolves the symlinks into copies here: ${command}` };
      if (!/(?:^|\s)-[a-zA-Z]*c/.test(command)) return { preserved: false, reason: `tar does not create an archive here: ${command}` };
      return { preserved: true, reason: 'tar stores symlinks as symlinks' };
    }
    if (/\bzip\b/.test(command)) {
      if (!storesSymlinks.test(command)) return { preserved: false, reason: `zip follows the symlinks instead of storing them, -y is missing: ${command}` };
      return { preserved: true, reason: 'zip -y stores symlinks as symlinks' };
    }
    return { preserved: false, reason: `the bundle is copied, not archived: ${command}` };
  }
  return { preserved: false, reason: 'nothing packs the .app bundle into an archive before the upload' };
}

describe('the symlink-preserving archive rule', () => {
  const accepted: ReadonlyArray<readonly [string, string]> = [
    ['ditto with --keepParent', 'ditto -c -k --sequesterRsrc --keepParent "release/mac-arm64/Void Code.app" release/void-code-mac-arm64.zip'],
    ['ditto with bundled flags', 'ditto -ck --keepParent "release/mac-arm64/Void Code.app" out.zip'],
    ['zip -y', 'zip -y -r out.zip "Void Code.app"'],
    ['zip --symlinks', 'zip --symlinks -r out.zip "Void Code.app"'],
    ['tar without -h', 'tar -czf out.tar.gz -C release/mac-arm64 "Void Code.app"'],
  ];
  const rejected: ReadonlyArray<readonly [string, string, string]> = [
    ['nothing at all', 'npm run package:mac', 'nothing packs'],
    ['ditto without --keepParent', 'ditto -c -k "release/mac-arm64/Void Code.app" out.zip', '--keepParent'],
    ['ditto as a plain copy', 'ditto "release/mac-arm64/Void Code.app" staging/Void Code.app', 'copies rather than archives'],
    ['zip without -y', 'zip -r out.zip "Void Code.app"', '-y is missing'],
    ['tar told to dereference', 'tar -chzf out.tar.gz -C release/mac-arm64 "Void Code.app"', 'resolves the symlinks'],
    ['a copy into an upload directory', 'cp -R "release/mac-arm64/Void Code.app" upload/', 'copied, not archived'],
  ];

  it.each(accepted)('accepts %s', (_name, command) => {
    expect(archiveVerdict(command).preserved).toBe(true);
  });

  it.each(rejected)('rejects %s', (_name, command, reason) => {
    const verdict = archiveVerdict(command);
    expect(verdict.preserved).toBe(false);
    expect(verdict.reason).toContain(reason);
  });
});

// ---------------------------------------------------------------------------
// The real workflows.
// ---------------------------------------------------------------------------

const workflow = (name: string) => parseWorkflow(readFileSync(new URL(`../../.github/workflows/${name}`, import.meta.url), 'utf8'));
const workflowText = (name: string) => readFileSync(new URL(`../../.github/workflows/${name}`, import.meta.url), 'utf8');

const testWorkflow = workflow('test.yml');
const packagesTheApp = (job: YamlValue) =>
  asList(asMap(job).steps).map(asMap).some((step) => /\bpackage:mac\b/.test(asText(step.run)));
const packagingJobs = Object.entries(asMap(testWorkflow.jobs)).filter(([, job]) => packagesTheApp(job));

const missing = 'no job in .github/workflows/test.yml runs `npm run package:mac`';
const found = packagingJobs.length === 1;
const packagingJob = asMap(packagingJobs[0]?.[1]);
const steps = asList(packagingJob.steps).map(asMap);
// Step order matters twice over, and both orders read off one transcript.
const transcript = steps.map((step) => `${asText(step.uses)}\n${asText(step.run)}`).join('\n \n');
const uploadIndex = steps.findIndex((step) => /actions\/upload-artifact/.test(asText(step.uses)));
const uploadStep = asMap(steps[uploadIndex]?.with);
const reason = (verdict: string) => (found ? verdict : missing);

describe('test.yml builds the macOS app and leaves it as a run artifact', () => {
  it('has exactly one job that packages the desktop app', () => {
    expect(packagingJobs.map(([name]) => name).join(', ') || missing).not.toBe(missing);
    expect(packagingJobs).toHaveLength(1);
  });

  it('builds on the macOS runner the smoke job already uses', () => {
    expect(reason(asText(packagingJob['runs-on']))).toBe('macos-14');
  });

  it('checks out the repository and installs Go, because the assembly shells out to `go build`', () => {
    // scripts/assemble-resources.mjs:55 runs `go build ./cmd/vc`; without a
    // toolchain the packaging step dies inside `npm run assemble`.
    const uses = steps.map((step) => asText(step.uses));
    expect(reason(uses.some((step) => step.startsWith('actions/checkout@')) ? 'checked out' : 'no actions/checkout step')).toBe('checked out');
    const go = asMap(steps.find((step) => asText(step.uses).startsWith('actions/setup-go@'))?.with);
    expect(reason(asText(go['go-version-file']) || 'no actions/setup-go step')).toBe('.go-version');
  });

  it('installs the Node version that resource-pins.json pins the runtime to', () => {
    // The pin moves; a literal here would rot silently. The workflow has to
    // follow the pin, not a copy of today's value.
    const node = asMap(steps.find((step) => asText(step.uses).startsWith('actions/setup-node@'))?.with);
    expect(reason(asText(node['node-version']) || 'no actions/setup-node step')).toBe(pins.node.version.replace(/^v/, ''));
  });

  it('caches npm against both lockfiles the assembly installs from', () => {
    const node = asMap(steps.find((step) => asText(step.uses).startsWith('actions/setup-node@'))?.with);
    expect(reason(asText(node.cache) || 'no actions/setup-node step')).toBe('npm');
    expect(asText(node['cache-dependency-path']).split('\n').map((line) => line.trim()).filter(Boolean))
      .toEqual(['desktop/package-lock.json', 'desktop/runtime/pi/package-lock.json']);
  });

  it('pins every action it uses to a commit SHA, as the rest of this file does', () => {
    const floating = steps
      .map((step) => asText(step.uses))
      .filter((uses) => uses !== '' && !/@[0-9a-f]{40}$/.test(uses));
    expect(reason(floating.join(', ') || 'all pinned')).toBe('all pinned');
  });

  it('provisions the pinned Pi runtime before it packages', () => {
    const provision = transcript.indexOf('provision-pinned-pi-smoke.sh');
    const packaged = transcript.indexOf('package:mac');
    expect(reason(provision < 0 ? 'no provision-pinned-pi-smoke.sh step' : provision < packaged ? 'provisioned first' : 'packaged before provisioning'))
      .toBe('provisioned first');
  });

  it('packages through `npm run package:mac`, never electron-builder by hand', () => {
    // package:mac is build + assemble + electron-builder --dir --mac --arm64.
    // Calling electron-builder directly skips the assembly, and the private
    // runtime the app needs is exactly what the assembly stages.
    const scripts = steps.map((step) => asText(step.run));
    const byHand = scripts.filter((script) => /(?:^|[\s/])electron-builder\s/.test(script));
    expect(reason(byHand.join(' | ') || 'through the npm script')).toBe('through the npm script');
    expect(reason(scripts.some((script) => /npm\s+run\s+(?:--\S+\s+)*package:mac\b/.test(script)) ? 'through the npm script' : 'no `npm run package:mac`'))
      .toBe('through the npm script');
  });

  it('runs the packaging step inside desktop/, where that script lives', () => {
    const step = steps.find((candidate) => /\bpackage:mac\b/.test(asText(candidate.run)));
    const located = /(?:^|\W)desktop(?:\W|$)/.test(`${asText(asMap(step)['working-directory'])} ${asText(asMap(step).run)}`);
    expect(reason(located ? 'in desktop/' : 'runs from the repository root, where package:mac does not exist')).toBe('in desktop/');
  });

  it('archives the bundle with its symlinks intact before uploading it', () => {
    const verdict = archiveVerdict(steps.map((step) => asText(step.run)).join('\n'));
    expect(reason(verdict.preserved ? 'symlinks preserved' : verdict.reason)).toBe('symlinks preserved');
  });

  it('archives first and uploads second', () => {
    const archived = steps.findIndex((step) => archiveVerdict(asText(step.run)).preserved);
    expect(reason(uploadIndex < 0 ? 'no actions/upload-artifact step' : archived < 0 ? 'no archiving step' : archived < uploadIndex ? 'archive, then upload' : 'uploads before archiving'))
      .toBe('archive, then upload');
  });

  it('uploads the archive file and not the bundle directory', () => {
    const path = asText(uploadStep.path);
    const verdict = path === '' ? 'no upload path'
      : /\.app\b(?!\.(?:zip|tar\.gz|tgz))/.test(path) ? `uploads the bundle directory: ${path}`
        : /\.(?:zip|tar\.gz|tgz)\b/.test(path) ? 'uploads an archive'
          : `uploads something that is not an archive: ${path}`;
    expect(reason(verdict)).toBe('uploads an archive');
  });

  it('fails the run rather than uploading nothing when the archive is not there', () => {
    // Without this, a renamed output path turns into a green run with an empty
    // artifact -- the failure mode that wastes a day before anyone notices.
    expect(reason(asText(uploadStep['if-no-files-found']) || 'if-no-files-found is unset, so a missing archive only warns')).toBe('error');
  });

  it('names the artifact so a person can tell what they are downloading', () => {
    const name = asText(uploadStep.name);
    expect(reason(/mac/i.test(name) ? 'named for macOS' : `artifact name does not say what platform it is: ${name || '(unset)'}`)).toBe('named for macOS');
  });

  it('runs on every branch push, not behind a ref condition', () => {
    const condition = asText(packagingJob.if);
    expect(reason(/github\.(?:ref|event_name)/.test(condition) ? `gated on a ref: ${condition}` : 'unconditional')).toBe('unconditional');
  });
});

describe('the artifact job publishes nothing', () => {
  // Release, installer publication and tag creation are forbidden to us without
  // a separate gate. The job may leave a build behind for the run; it may not
  // put anything where a stranger can reach it.
  const forbidden: ReadonlyArray<readonly [string, RegExp]> = [
    ['a GitHub release action', /softprops\/action-gh-release|ncipollo\/release-action|actions\/create-release/],
    ['`gh release`', /\bgh\s+release\b/],
    ['a release call through the API', /\bgh\s+api\b[^\n]*releases/],
    ['tag creation', /\bgit\s+tag\b|\brefs\/tags\//],
    ['a push of tags', /\bgit\s+push\b[^\n]*(?:--tags|--follow-tags|tag)/],
    ['a package publish', /\bnpm\s+publish\b|\bdocker\s+push\b/],
  ];

  it.each(forbidden)('the job contains no %s', (_name, pattern) => {
    expect(reason(pattern.test(transcript) ? `found ${pattern}` : 'absent')).toBe('absent');
  });

  it('asks for read-only permissions, so publishing is refused by the token and not merely by us', () => {
    const permissions = asMap(packagingJob.permissions);
    const writes = Object.entries(permissions).filter(([, value]) => asText(value) === 'write');
    expect(reason(Object.keys(permissions).length === 0 ? 'the job declares no permissions block' : writes.map(([scope]) => scope).join(', ') || 'read-only'))
      .toBe('read-only');
    expect(reason(asText(permissions.contents) || 'contents is unset')).toBe('read');
  });

  it.each(forbidden)('the whole of test.yml contains no %s', (_name, pattern) => {
    expect(pattern.test(workflowText('test.yml'))).toBe(false);
  });
});

describe('packaging stays out of the tag-triggered workflows', () => {
  it('test.yml fires on branch pushes and pull requests, never on a tag', () => {
    const triggers = asMap(testWorkflow.on);
    expect(Object.keys(triggers).sort()).toEqual(['pull_request', 'push']);
    expect(asMap(triggers.push).tags).toBeUndefined();
    expect(asList(asMap(triggers.push)['branches-ignore'])).toContain('main');
  });

  it.each(['release.yml', 'canary-release.yml'])('%s stays tag-triggered and gains no desktop packaging', (name) => {
    const released = workflow(name);
    expect(asList(asMap(asMap(released.on).push).tags).length).toBeGreaterThan(0);
    expect(Object.entries(asMap(released.jobs)).filter(([, job]) => packagesTheApp(job)).map(([job]) => job)).toEqual([]);
  });
});
