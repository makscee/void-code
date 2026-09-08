import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { asList, asMap, asText, parseWorkflow, type YamlMap, type YamlValue } from './workflow-yaml';
import { conditionHolds, interpolate, PLAIN_TAG, type Context } from './workflow-expressions';

// The release publishes three things a person downloads and double-clicks --
//
//     void-code-mac-arm64.zip          207 MB
//     void-code-mac-x64.zip            214 MB
//     Void-Code-windows-x64.exe        149 MB
//
// -- and nothing on any route says what their bytes should be. Measured on the
// v0.2.51 asset: SHA256SUMS names seven files, six `vc-*` and version.json, and
// stops there. It is written by one line in the job that creates the release,
//
//     working-directory: dist
//     run: sha256sum vc-* version.json > SHA256SUMS
//
// whose operands are typed out. The bundles are built by other jobs and
// attached by another one again, so they were never in reach of that glob and
// will not be however many architectures are added.
//
// THE SHAPE OF THE FIX IS DECIDED, and it is not "add the bundles to that list".
// The attaching job publishes a checksum list OF ITS OWN, and does not touch the
// one the creating job published:
//
//     SHA256SUMS           the CLI, written and published by the release job
//     SHA256SUMS-desktop   the bundles, written and published by the attaching job
//
// Why, since amending the one list is fewer moving parts. It was tried, against
// this file's earlier form, and the neighbouring release-desktop-attach-
// workflow.test.ts caught it: `gh release upload SHA256SUMS --clobber` from the
// attaching job is that file's "leaves the CLI assets to the job that created
// the release" rule, broken in the most expensive place available. That rule
// exists so a re-run of a desktop build cannot rewrite bytes install.sh depends
// on. A partial or raced rewrite of the shared list breaks CLI installation for
// EVERYONE; a bundle with no published checksum means one person cannot verify
// one download. The second is bad and the first is worse, so ownership of a list
// stays with the job that wrote it. Two lists, two owners, no shared write.
//
// That decision also decides where the new list lives, and the constraint is
// worth stating because it is easy to get wrong in a way nothing here would
// explain. The list is written INTO the staging directory the attaching job
// already downloads into, so the `desktop-installers/*` glob that job already
// has publishes it, and the `subject-path: desktop-installers/*` it already has
// signs it. A list written anywhere else has to be named in an upload path of
// its own -- and a path naming a file no artifact contains is exactly what the
// neighbour's "attaches nothing it did not stage" rule refuses. Riding the
// existing glob is what keeps that file green with no edit, which is a condition
// on this change, not a bonus.
//
// Measured, because it is the obvious objection: `sha256sum * > SHA256SUMS-desktop`
// run inside that directory does NOT name the list in itself. bash expands the
// glob before it performs the redirection, so the file does not exist yet when
// `*` is evaluated. Three empty fixtures in a scratch directory, three lines out,
// `grep -c SHA256SUMS-desktop` on the result: 0.
//
// So the rules, and none of them is "write these three names down":
//
//   1. The attaching job publishes a checksum list for the bundles, and that
//      list names every desktop build the run publishes. DERIVED from the build
//      workflows -- a third architecture tomorrow must need no edit to
//      release.yml, which is stated as a mutation rather than as a wish.
//   2. SHA256SUMS stays the creating job's. No other job writes to it, appends
//      to it, or publishes an asset by that name. Asserted directly, so a later
//      "why two lists" tidy-up cannot pass quietly.
//   3. The names in the new list are bare basenames. Not cosmetic: install.sh
//      looks a download up with
//          awk -v n="$_ss_name" '$2 == n || $2 == "*" n { print $1; exit }'
//      over `${VC_ARTIFACT_PATH##*/}`, and install.ps1 does the same. A list of
//      `desktop-installers/void-code-mac-x64.zip` satisfies rule 1 and helps
//      nobody, so it is the shape that is asked about.
//   4. It is written by a tool whose output that awk can read. `openssl dgst`
//      prints `SHA256(f)= h`, which is rule 3 broken somewhere else.
//   5. Nothing is hashed that the run does not hold. `sha256sum` on a path that
//      is not there exits non-zero and takes the step with it, so a name that
//      outlives what it named goes red on the release, which is the worst place
//      to find out.
//
// A note on where the fix can live, because this file's model has an opinion and
// it is deliberate. Files are staged into a job only from the jobs it `needs`.
// The `release` job needs `build` and nothing desktop -- on purpose, so a desktop
// build dying on a runner cannot withhold the CLI release or self-update -- so no
// glob written inside `release` can reach a bundle. Widening `vc-*` to `*` there
// does not go green here and must not: what such a list covered would depend on
// which runner happened to finish first.
//
// What this file cannot do: it cannot push a tag, it cannot run GitHub Actions,
// and it cannot hash a byte. It fixes the FORM of the workflow -- which files a
// job holds when a command runs, which of them that command names, and which
// names come out. That a real run produces a list matching the real bytes is
// unverified here, by anyone, and a green suite must not be read as saying so.
//
// Out of scope, named so it is not mistaken for covered: `publish-auth` needs
// `release` alone and syncs SHA256SUMS to $AUTH_HOST for install.sh's primary
// check. It knows nothing about the desktop list, and under this shape it does
// not need to -- the bundles are not served from that route. If they ever are,
// that route needs its own decision and its own test.

// ---------------------------------------------------------------------------
// Reading a release run: what each job holds, what it writes, what it publishes.
//
// workflow-yaml.ts is a reader, not a validator, and neither is anything below
// it, so none of it is trusted on the real files until it has been shown
// answering correctly -- and answering DIFFERENTLY -- on the synthetic workflows
// further down.
//
// The overlap with release-desktop-attach-workflow.test.ts is the glob matcher
// and the matrix reader, some thirty lines. That file asks which artifacts reach
// the release; this one asks what a job's working directory holds at the moment
// a command runs, step by step, which is a walk that file has no need of.
// Whoever next has cause to touch both can lift the shared half into a plain
// module the way workflow-yaml.ts was lifted.
// ---------------------------------------------------------------------------

type Read = (workflow: string) => string;

const LOCAL = './.github/workflows/';

// The two lists, and the only two names this file spells out. They are contract
// names -- a person who downloaded an installer reads them off the release page
// -- so they are pinned here rather than derived, the same way install.sh's
// route is pinned by a Go test next door.
const CLI_SUMS = 'SHA256SUMS';
const DESKTOP_SUMS = 'SHA256SUMS-desktop';
const isSumsList = (name: string) => name === CLI_SUMS || name === DESKTOP_SUMS;

const lines = (text: string) => text.split('\n').map((line) => line.trim()).filter((line) => line !== '');
const basename = (path: string) => path.split('/').filter((part) => part !== '').pop() ?? path;

// `..` is resolved rather than carried, because a step whose working-directory
// is one level down writes its output back up with one -- and `dist/../SUMS` and
// `SUMS` have to be the same file here or the walk loses track of it.
const normalize = (path: string) => {
  const parts: string[] = [];
  for (const part of path.replace(/\/{2,}/g, '/').split('/')) {
    if (part === '' || part === '.') continue;
    if (part === '..' && parts.length > 0 && parts[parts.length - 1] !== '..') parts.pop();
    else parts.push(part);
  }
  return parts.join('/') || '.';
};

// A glob as GitHub and the shell mean it: `*` stands for a run of characters
// within one path segment and never crosses a `/`.
const escapeRegExp = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
const asRegExp = (pattern: string) => new RegExp(`^${pattern.split('*').map(escapeRegExp).join('[^/]*')}$`);
const covers = (pattern: string, candidate: string) => asRegExp(normalize(pattern)).test(normalize(candidate).replace(/\*/g, 'x'));
const isGlob = (pattern: string) => pattern.includes('*') || pattern.includes('?') || pattern.includes('[');

const stepsOf = (job: YamlMap) => asList(job.steps).map(asMap);
const transcriptOf = (job: YamlMap) => stepsOf(job).map((step) => `${asText(step.uses)}\n${asText(step.run)}`).join('\n \n');

// Does this job package the desktop app? Answered from what it runs, so a
// renamed job or a third reusable workflow is still recognised.
const PACKAGES_DESKTOP = /\bpackage:mac\b|\bpackage:win\b|(?:^|[\s/])electron-builder\s/;

const RELEASE_ACTION = /softprops\/action-gh-release|ncipollo\/release-action|actions\/create-release/;

// The tools whose output install.sh's awk can read: `<hash>  <name>`, hash
// first, name second. Named rather than inferred -- see rule 4.
const SUM_TOOLS = new Set(['sha256sum', 'gsha256sum', 'shasum', 'sha256']);

// Flags that swallow the word after them, so `shasum -a 256 f` does not report
// `256` as a file nothing produced.
const FLAGS_TAKING_A_VALUE = new Set(['-a', '--algorithm', '-b', '--binary-flag']);

function matrixRows(job: YamlMap, where: string): YamlMap[] {
  const matrix = asMap(asMap(job.strategy).matrix);
  const included = asList(matrix.include).map(asMap);
  const axes = Object.entries(matrix).filter(([key]) => key !== 'include' && key !== 'exclude');
  if (axes.length > 1) throw new Error(`${where} has a matrix of ${axes.length} axes, which this test does not model`);
  const spread = axes.length === 1 ? asList(axes[0][1]).map((value) => ({ [axes[0][0]]: asText(value) })) : [];
  const rows = [...spread, ...included];
  return rows.length > 0 ? rows : [{}];
}

const substitute = (text: string, row: YamlMap) =>
  text.replace(/\$\{\{\s*matrix\.([A-Za-z0-9_-]+)\s*\}\}/g, (whole, key: string) => {
    const value = row[key];
    return typeof value === 'string' ? value : whole;
  });

const jobsOf = (read: Read, workflow: string): [string, YamlMap][] =>
  Object.entries(asMap(parseWorkflow(read(workflow)).jobs)).map(([name, value]) => [name, asMap(value)]);

// `via` is the release.yml job the artifact arrives through -- the job itself, or
// the job whose `uses:` called the reusable workflow that uploaded it. It is what
// makes `needs` answerable: an artifact is available to a job only if the job it
// came through is one this job waited for.
type Artifact = {
  readonly via: string;
  readonly name: string;
  readonly files: readonly string[];
  readonly desktop: boolean;
};

function uploadsIn(read: Read, workflow: string, via: string, context: Context): Artifact[] {
  const artifacts: Artifact[] = [];
  for (const [jobName, job] of jobsOf(read, workflow)) {
    const desktop = PACKAGES_DESKTOP.test(transcriptOf(job));
    for (const row of matrixRows(job, `${workflow}:${jobName}`)) {
      for (const step of stepsOf(job)) {
        if (!/actions\/upload-artifact/.test(asText(step.uses))) continue;
        const settings = asMap(step.with);
        const resolve = (value: YamlValue | undefined) => interpolate(substitute(asText(value), row), context);
        artifacts.push({ via, name: resolve(settings.name), files: lines(resolve(settings.path)).map(basename), desktop });
      }
    }
  }
  return artifacts;
}

// uploadsIn() walks a whole workflow, which is what a `uses:` call means: one
// release.yml job stands for every job in the file it calls. A job that uploads
// on its own behalf is a different question -- only its own steps count -- so it
// is asked separately rather than by filtering the other answer.
function ownUploads(jobName: string, job: YamlMap, context: Context): Artifact[] {
  const desktop = PACKAGES_DESKTOP.test(transcriptOf(job));
  const artifacts: Artifact[] = [];
  for (const row of matrixRows(job, `release.yml:${jobName}`)) {
    for (const step of stepsOf(job)) {
      if (!/actions\/upload-artifact/.test(asText(step.uses))) continue;
      const settings = asMap(step.with);
      const resolve = (value: YamlValue | undefined) => interpolate(substitute(asText(value), row), context);
      artifacts.push({ via: jobName, name: resolve(settings.name), files: lines(resolve(settings.path)).map(basename), desktop });
    }
  }
  return artifacts;
}

function artifactsOfRun(read: Read, context: Context): Artifact[] {
  const produced: Artifact[] = [];
  for (const [name, job] of jobsOf(read, 'release.yml')) {
    if (!conditionHolds(asText(job.if), context)) continue;
    const called = asText(job.uses);
    if (called.startsWith(LOCAL)) produced.push(...uploadsIn(read, called.slice(LOCAL.length).split('@')[0], name, context));
    else produced.push(...ownUploads(name, job, context));
  }
  return produced;
}

// The order jobs run in. A cycle raises rather than being read as "no
// dependencies".
function dependencyOrder(jobs: Map<string, YamlMap>): string[] {
  const done = new Set<string>();
  const ordered: string[] = [];
  const visit = (name: string, stack: readonly string[]) => {
    if (done.has(name)) return;
    if (stack.includes(name)) throw new Error(`release.yml has a cycle in needs: ${[...stack, name].join(' -> ')}`);
    for (const need of asList(jobs.get(name)?.needs).map(asText)) {
      if (jobs.has(need)) visit(need, [...stack, name]);
    }
    done.add(name);
    ordered.push(name);
  };
  for (const name of jobs.keys()) visit(name, []);
  return ordered;
}

function waitedFor(jobs: Map<string, YamlMap>, name: string): Set<string> {
  const reached = new Set<string>();
  const walk = (current: string) => {
    for (const need of asList(jobs.get(current)?.needs).map(asText)) {
      if (!jobs.has(need) || reached.has(need)) continue;
      reached.add(need);
      walk(need);
    }
  };
  walk(name);
  return reached;
}

// ---------------------------------------------------------------------------
// The shell, to the depth these workflows use it: where a file comes from and
// where a command's output goes.
// ---------------------------------------------------------------------------

// Heredoc bodies are content, not commands: `cat > version.json <<EOF` is
// followed by JSON, and reading it as shell finds redirections that are not
// there. Line continuations are joined first so a command split over two lines
// is still one command.
function commandsIn(script: string): string[] {
  const source = script.replace(/\\\n/g, ' ').split('\n');
  const commands: string[] = [];
  for (let index = 0; index < source.length; index += 1) {
    const line = source[index];
    const heredoc = /<<-?\s*'?"?([A-Za-z_][A-Za-z0-9_]*)'?"?/.exec(line);
    commands.push(...line.split(/&&|\|\||;/).map((part) => part.trim()).filter((part) => part !== '' && !part.startsWith('#')));
    if (!heredoc) continue;
    index += 1;
    while (index < source.length && source[index].trim() !== heredoc[1]) index += 1;
  }
  return commands;
}

// Redirection operators are spaced out first so `>SUMS` and `> SUMS` tokenize
// the same. `2>&1` becomes `2 > &1`, whose target is not a checksum list and is
// ignored below.
function words(command: string): string[] {
  const spaced = command.replace(/>>?/g, (operator) => ` ${operator} `);
  return Array.from(spaced.matchAll(/'[^']*'|"[^"]*"|\S+/g), (found) => found[0].replace(/^['"]|['"]$/g, ''));
}

type Redirection = { readonly target: string; readonly append: boolean; readonly before: readonly string[] };

function redirection(command: string): Redirection | null {
  const token = words(command);
  const at = token.findIndex((word) => word === '>' || word === '>>');
  if (at < 0 || at + 1 >= token.length) return null;
  return { target: token[at + 1], append: token[at] === '>>', before: token.slice(0, at) };
}

const join = (directory: string, path: string) =>
  normalize(path.startsWith('/') ? path : directory === '' || directory === '.' ? path : `${directory}/${path}`);

// ---------------------------------------------------------------------------
// The walk.
// ---------------------------------------------------------------------------

// One checksum list, as the run builds it up: which names it holds and which
// jobs put them there. `>` clears it, `>>` extends it, so a job that truncates
// somebody else's list is visible as a list whose earlier names are gone.
type List = { readonly names: Map<string, string>; readonly writers: string[] };

type Report = {
  readonly trouble: string;
  // Basenames of everything the run attaches to the release.
  readonly published: readonly string[];
  readonly desktopPublished: readonly string[];
  // The desktop list as published: '' when the run publishes none.
  readonly desktopListJob: string;
  readonly desktopListNames: readonly string[];
  readonly desktopUnlisted: readonly string[];
  // Any write to, or publication of, the CLI list by a job that did not create
  // the release.
  readonly cliListTouched: readonly string[];
  readonly cliListNames: readonly string[];
  readonly notBasenames: readonly string[];
  readonly phantom: readonly string[];
  readonly unreadableWriter: readonly string[];
  readonly writers: readonly string[];
};

const EMPTY: Report = {
  trouble: '', published: [], desktopPublished: [], desktopListJob: '', desktopListNames: [],
  desktopUnlisted: [], cliListTouched: [], cliListNames: [], notBasenames: [], phantom: [],
  unreadableWriter: [], writers: [],
};

function walk(read: Read, context: Context): Report {
  const jobs = new Map(jobsOf(read, 'release.yml'));
  const produced = artifactsOfRun(read, context);
  const desktopFiles = new Set(produced.filter((artifact) => artifact.desktop).flatMap((artifact) => artifact.files));

  // Basename -> where the run held it and which job put it on the release.
  const published = new Map<string, { path: string; job: string }>();
  // Path -> the list written there.
  const listAt = new Map<string, List>();
  const cliListTouched: string[] = [];
  const phantom: string[] = [];
  const unreadableWriter: string[] = [];
  const writers: string[] = [];

  for (const jobName of dependencyOrder(jobs)) {
    const job = jobs.get(jobName) as YamlMap;
    if (!conditionHolds(asText(job.if), context)) continue;
    const waited = waitedFor(jobs, jobName);
    const available = produced.filter((artifact) => waited.has(artifact.via));
    const jobDirectory = normalize(asText(asMap(asMap(job.defaults).run)['working-directory']) || '.');
    const creates = stepsOf(job).some((step) =>
      RELEASE_ACTION.test(asText(step.uses)) || /\bgh\s+release\s+create\b/.test(asText(step.run)));
    // What this job's workspace holds, as paths from the workspace root.
    const present = new Set<string>();

    for (const step of stepsOf(job)) {
      if (!conditionHolds(asText(step.if), context)) continue;
      const settings = asMap(step.with);
      const resolve = (value: YamlValue | undefined) => interpolate(asText(value), context);
      const uses = asText(step.uses);
      const directory = normalize(resolve(step['working-directory']) || jobDirectory);

      if (/actions\/download-artifact/.test(uses)) {
        const into = normalize(resolve(settings.path) || '.');
        const name = resolve(settings.name);
        const pattern = resolve(settings.pattern);
        const merged = resolve(settings['merge-multiple']) === 'true';
        for (const artifact of available) {
          const selected = name !== '' ? artifact.name === name : pattern !== '' ? covers(pattern, artifact.name) : true;
          if (!selected) continue;
          const at = name !== '' || merged ? into : `${into}/${artifact.name}`;
          for (const file of artifact.files) present.add(join(at, file));
        }
      }

      const script = resolve(step.run);
      for (const command of commandsIn(script)) {
        const token = words(command);

        // `gh release download` brings assets the release already carries back
        // onto the runner. Modelled because a job that reaches for the other
        // job's list has to be visible, not because anything should.
        if (token[0] === 'gh' && token[1] === 'release' && token[2] === 'download') {
          const flag = (...names: string[]) => {
            const at = token.findIndex((word) => names.includes(word));
            return at >= 0 ? token[at + 1] ?? '' : '';
          };
          const into = normalize(flag('--dir', '-D') || '.');
          const pattern = flag('--pattern', '-p');
          for (const [asset, origin] of published) {
            if (pattern !== '' && !covers(pattern, asset)) continue;
            const at = join(into, asset);
            present.add(at);
            const existing = listAt.get(origin.path);
            if (existing) listAt.set(at, existing);
          }
        }

        // `mv`/`cp` of something the job holds, so a list assembled elsewhere
        // and moved into place is still followed.
        if ((token[0] === 'mv' || token[0] === 'cp') && token.length >= 3) {
          const operands = token.slice(1).filter((word) => !word.startsWith('-'));
          const destination = join(directory, operands[operands.length - 1]);
          for (const source of operands.slice(0, -1)) {
            for (const file of [...present]) {
              if (!covers(join(directory, source), file)) continue;
              const at = operands.length > 2 ? `${destination}/${basename(file)}` : destination;
              present.add(at);
              const carried = listAt.get(file);
              if (carried) listAt.set(at, carried);
              if (token[0] === 'mv') { present.delete(file); listAt.delete(file); }
            }
          }
        }

        const redirect = redirection(command);
        if (!redirect) continue;
        const target = join(directory, redirect.target);
        if (!isSumsList(basename(target))) {
          present.add(target);
          continue;
        }

        writers.push(`${jobName}: ${command}`);
        if (basename(target) === CLI_SUMS && !creates) {
          cliListTouched.push(
            `${jobName} ${redirect.append ? 'appends to' : 'overwrites'} ${CLI_SUMS}, which belongs to the job that created the release`,
          );
        }
        const tool = basename(redirect.before[0] ?? '');
        if (!SUM_TOOLS.has(tool)) {
          unreadableWriter.push(
            `${jobName} writes ${basename(target)} with «${tool || command}», whose output install.sh's awk cannot read`,
          );
          present.add(target);
          continue;
        }

        const list = redirect.append && listAt.has(target)
          ? (listAt.get(target) as List)
          : { names: new Map<string, string>(), writers: [] };
        list.writers.push(jobName);
        listAt.set(target, list);

        const prefix = directory === '.' || directory === '' ? '' : `${directory}/`;
        const operands = redirect.before.slice(1).filter((word, index, all) =>
          !word.startsWith('-') && !FLAGS_TAKING_A_VALUE.has(all[index - 1] ?? ''));
        for (const operand of operands) {
          let matched = 0;
          for (const file of present) {
            if (!file.startsWith(prefix)) continue;
            const relative = file.slice(prefix.length);
            if (!covers(operand, relative)) continue;
            matched += 1;
            list.names.set(relative, jobName);
          }
          if (matched === 0) {
            phantom.push(
              `${jobName} hashes ${isGlob(operand) ? 'the pattern ' : ''}${operand}, which nothing in this run stages`,
            );
          }
        }
        // Added after the operands are expanded, which is what bash does: the
        // glob is evaluated before the redirection creates the file. Measured,
        // not assumed -- see the header.
        present.add(target);
      }

      // What reaches the release: the action that creates it, and `gh release
      // upload` from any job that adds to it afterwards.
      const attaching: string[] = [];
      if (RELEASE_ACTION.test(uses)) attaching.push(...lines(resolve(settings.files)));
      for (const call of script.matchAll(/\bgh\s+release\s+(?:upload|create)\s+\S+([^\n]*)/g)) {
        attaching.push(...call[1].split(/\s+/).filter((word) => word !== '' && !word.startsWith('-')));
      }
      for (const path of attaching) {
        for (const file of present) {
          if (!covers(join(directory, path), file)) continue;
          if (basename(file) === CLI_SUMS && !creates) {
            cliListTouched.push(`${jobName} publishes ${CLI_SUMS}, which belongs to the job that created the release`);
          }
          published.set(basename(file), { path: file, job: jobName });
        }
      }
    }
  }

  const listOf = (asset: string): List | undefined => {
    const origin = published.get(asset);
    return origin ? listAt.get(origin.path) : undefined;
  };
  const desktopList = listOf(DESKTOP_SUMS);
  const cliList = listOf(CLI_SUMS);
  const desktopNames = new Set([...(desktopList?.names.keys() ?? [])].map(basename));
  const desktopOwed = [...published.keys()].filter((asset) => desktopFiles.has(asset));

  return {
    ...EMPTY,
    published: [...published.keys()].sort(),
    desktopPublished: desktopOwed.sort(),
    desktopListJob: published.get(DESKTOP_SUMS)?.job ?? '',
    desktopListNames: [...(desktopList?.names.keys() ?? [])].sort(),
    desktopUnlisted: desktopOwed.filter((asset) => !desktopNames.has(asset)).sort(),
    cliListTouched: [...new Set(cliListTouched)].sort(),
    cliListNames: [...(cliList?.names.keys() ?? [])].sort(),
    notBasenames: [...(desktopList?.names.keys() ?? [])].filter((name) => name.includes('/')).sort(),
    phantom: phantom.sort(),
    unreadableWriter: unreadableWriter.sort(),
    writers,
  };
}

function report(read: Read, context: Context): Report {
  try {
    return walk(read, context);
  } catch (error) {
    // Never the pleasant answer. A run this cannot read is reported as unread,
    // because "nothing is missing from the list" and "no list was looked at" are
    // otherwise the same green.
    return { ...EMPTY, trouble: `the release run could not be read: ${(error as Error).message}` };
  }
}

// ---------------------------------------------------------------------------
// The mutation: a third architecture, and a removed one. Textual, on the build
// workflow only, so what is asked is exactly what a real edit would ask --
// release.yml is not touched, and the question is whether it needed to be.
// ---------------------------------------------------------------------------

function editMatrixRow(text: string, value: string, replacement: string | null): string {
  const all = text.replace(/\r\n/g, '\n').split('\n');
  const start = all.findIndex((line) => new RegExp(`^\\s*-\\s+[A-Za-z0-9_-]+:\\s*${escapeRegExp(value)}\\s*$`).test(line));
  if (start < 0) throw new Error(`no matrix row in this workflow has a value of exactly ${value}`);
  const dent = all[start].length - all[start].trimStart().length;
  let end = start + 1;
  while (end < all.length && (all[end].trim() === '' || all[end].length - all[end].trimStart().length > dent)) end += 1;
  const row = all.slice(start, end);
  if (replacement === null) return [...all.slice(0, start), ...all.slice(end)].join('\n');
  const copy = row.map((line) => line.replace(new RegExp(`\\b${escapeRegExp(value)}\\b`, 'g'), replacement));
  return [...all.slice(0, end), ...copy, ...all.slice(end)].join('\n');
}

const withEditedRow = (read: Read, workflow: string, value: string, replacement: string | null): Read =>
  (name) => (name === workflow ? editMatrixRow(read(workflow), value, replacement) : read(name));

// ---------------------------------------------------------------------------
// The machinery, shown answering on workflows written for the purpose.
//
// One mac build workflow and seven attaching jobs: the arrangement release.yml
// has today, the shape that was rejected, the shape that was chosen, and four
// ways of getting the chosen one subtly wrong. Every rule below is asked of them,
// so a rule that cannot fail is visible here rather than in a release.
// ---------------------------------------------------------------------------

const HEAD = `
name: Fixture
on:
  push:
    tags:
      - 'v*.*.*'

jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - artifact: cli-linux
    steps:
      - name: Build
        run: go build -o \${{ matrix.artifact }} ./cmd/vc
      - uses: actions/upload-artifact@abc123
        with:
          name: \${{ matrix.artifact }}
          path: \${{ matrix.artifact }}

  desktop-mac-app:
    if: \${{ vars.DESKTOP == 'true' }}
    uses: ./.github/workflows/mac.yml

  release:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/download-artifact@abc123
        with:
          path: dist
          merge-multiple: true
      - name: Generate version.json
        run: |
          cat > dist/version.json <<EOF
          { "tag": "\${{ github.ref_name }}" }
          EOF
      - name: Generate checksums
        working-directory: dist
        run: sha256sum cli-* version.json > SHA256SUMS
      - uses: softprops/action-gh-release@v2
        with:
          files: |
            dist/cli-linux
            dist/version.json
            dist/SHA256SUMS

  attach:
    needs: [release, desktop-mac-app]
    if: \${{ vars.DESKTOP == 'true' }}
    runs-on: ubuntu-latest
    steps:
      - uses: actions/download-artifact@abc123
        with:
          pattern: app-*
          merge-multiple: true
          path: desktop-installers
`;

const ATTACH = `      - name: Attach
        run: gh release upload \${{ github.ref_name }} desktop-installers/* --clobber
`;

// The shape that was chosen: a list of its own, written into the directory the
// existing glob already publishes.
const OWN_LIST = `      - name: Checksums for the bundles
        working-directory: desktop-installers
        run: sha256sum * > SHA256SUMS-desktop
`;

// The shape that was rejected: reach for the other job's list and clobber it.
const AMENDS_CLI = `      - name: Bring the CLI list back
        run: gh release download \${{ github.ref_name }} --pattern SHA256SUMS --dir .
      - name: Extend it
        working-directory: desktop-installers
        run: sha256sum * >> ../SHA256SUMS
      - name: Put it back
        run: gh release upload \${{ github.ref_name }} SHA256SUMS --clobber
`;

// The chosen shape with the names typed out instead of globbed.
const ENUMERATED = `      - name: Checksums for the bundles
        working-directory: desktop-installers
        run: sha256sum app-mac-arm64.zip app-mac-x64.zip > SHA256SUMS-desktop
`;

// The chosen shape written from the workspace root, so every name in the list
// carries the directory it was hashed in and no installer can look one up.
const PREFIXED = `      - name: Checksums for the bundles
        run: sha256sum desktop-installers/* > desktop-installers/SHA256SUMS-desktop
`;

// The chosen shape with the list written somewhere the publishing glob cannot
// reach: it exists on the runner and nobody ever sees it.
const UNPUBLISHED = `      - name: Checksums for the bundles
        working-directory: desktop-installers
        run: sha256sum * > ../SHA256SUMS-desktop
`;

// And the chosen shape with a tool whose output is a different shape.
const WRONG_TOOL = `      - name: Checksums for the bundles
        working-directory: desktop-installers
        run: openssl dgst -sha256 * > SHA256SUMS-desktop
`;

const MAC = `
name: Mac
on:
  workflow_call:

jobs:
  package:
    runs-on: macos-14
    strategy:
      fail-fast: false
      matrix:
        include:
          - arch: arm64
          - arch: x64
    steps:
      - name: Package
        run: npm run package:mac
      - uses: actions/upload-artifact@abc123
        with:
          name: app-mac-\${{ matrix.arch }}
          path: desktop/release/app-mac-\${{ matrix.arch }}.zip
`;

const world = (sums: string): Read => (name) => {
  if (name === 'release.yml') return HEAD + sums + ATTACH;
  if (name === 'mac.yml') return MAC;
  throw new Error(`the fixture has no workflow called ${name}`);
};

const TODAY = world('');
const CHOSEN = world(OWN_LIST);
const REJECTED = world(AMENDS_CLI);
const TYPED_OUT = world(ENUMERATED);
const PREFIXING = world(PREFIXED);
const HIDDEN = world(UNPUBLISHED);
const OTHER_TOOL = world(WRONG_TOOL);

const FIXTURE_ON: Context = { ...PLAIN_TAG, 'vars.DESKTOP': 'true' };

describe('the walk, on workflows written to exercise it', () => {
  it('sees what a release run publishes, through a reusable workflow and a matrix', () => {
    const found = report(TODAY, FIXTURE_ON);
    expect(found.trouble).toBe('');
    expect(found.published).toEqual(['SHA256SUMS', 'app-mac-arm64.zip', 'app-mac-x64.zip', 'cli-linux', 'version.json']);
    expect(found.desktopPublished).toEqual(['app-mac-arm64.zip', 'app-mac-x64.zip']);
    expect(found.cliListNames).toEqual(['cli-linux', 'version.json']);
  });

  it('reports today\'s arrangement as publishing no list for the bundles at all', () => {
    const found = report(TODAY, FIXTURE_ON);
    expect(found.desktopListJob).toBe('');
    expect(found.desktopUnlisted).toEqual(['app-mac-arm64.zip', 'app-mac-x64.zip']);
    expect(found.cliListTouched).toEqual([]);
  });

  it('is satisfied by a list of its own riding the glob the job already has', () => {
    const found = report(CHOSEN, FIXTURE_ON);
    expect(found.trouble).toBe('');
    expect(found.published).toContain('SHA256SUMS-desktop');
    expect(found.desktopListJob).toBe('attach');
    expect(found.desktopListNames).toEqual(['app-mac-arm64.zip', 'app-mac-x64.zip']);
    expect(found.desktopUnlisted).toEqual([]);
    expect(found.notBasenames).toEqual([]);
    expect(found.phantom).toEqual([]);
    expect(found.unreadableWriter).toEqual([]);
  });

  it('leaves the CLI list exactly as the creating job wrote it', () => {
    const found = report(CHOSEN, FIXTURE_ON);
    expect(found.cliListTouched).toEqual([]);
    expect(found.cliListNames).toEqual(['cli-linux', 'version.json']);
  });

  it('catches the rejected shape: the attaching job reaching for the other list', () => {
    // Both halves of it -- the write and the re-publication -- because a fix
    // that stops doing one of them is not a fix.
    const found = report(REJECTED, FIXTURE_ON);
    expect(found.cliListTouched.join(' | ')).toContain(`attach appends to ${CLI_SUMS}`);
    expect(found.cliListTouched.join(' | ')).toContain(`attach publishes ${CLI_SUMS}`);
    expect(found.desktopListJob).toBe('');
  });

  it('catches a list written where the publishing glob cannot reach it', () => {
    const found = report(HIDDEN, FIXTURE_ON);
    expect(found.desktopListJob).toBe('');
    expect(found.desktopUnlisted).toEqual(['app-mac-arm64.zip', 'app-mac-x64.zip']);
  });

  it('catches a list whose names carry a directory, which no installer looks up', () => {
    const found = report(PREFIXING, FIXTURE_ON);
    expect(found.notBasenames).toEqual(['desktop-installers/app-mac-arm64.zip', 'desktop-installers/app-mac-x64.zip']);
    // The bundles are named, just not in a way anything can use: the two rules
    // have to be able to disagree, or the basename rule is decoration.
    expect(found.desktopUnlisted).toEqual([]);
  });

  it('catches a writer whose output is not the shape install.sh parses', () => {
    const found = report(OTHER_TOOL, FIXTURE_ON);
    expect(found.unreadableWriter.join(' | ')).toContain('openssl');
    expect(found.desktopUnlisted).toEqual(['app-mac-arm64.zip', 'app-mac-x64.zip']);
  });

  it('publishes no desktop list, and owes none, when the switch is off', () => {
    const off = report(CHOSEN, PLAIN_TAG);
    expect(off.desktopPublished).toEqual([]);
    expect(off.desktopUnlisted).toEqual([]);
    expect(off.cliListNames).toEqual(['cli-linux', 'version.json']);
  });

  it('reports a run it could not read instead of finding nothing wrong', () => {
    const twoAxes: Read = (name) => (name === 'mac.yml'
      ? MAC.replace(/matrix:\n(\s+)include:[^]*$/, 'matrix:\n$1arch: [arm64, x64]\n$1node: [20, 22]\n')
      : CHOSEN(name));
    expect(report(twoAxes, FIXTURE_ON).trouble).toContain('does not model');
  });
});

describe('the mutation, on the same fixtures', () => {
  const third = (read: Read) => withEditedRow(read, 'mac.yml', 'arm64', 'universal');
  const without = (read: Read) => withEditedRow(read, 'mac.yml', 'arm64', null);

  it('really does add a build, and really does remove one', () => {
    expect(report(third(CHOSEN), FIXTURE_ON).published).toContain('app-mac-universal.zip');
    expect(report(without(CHOSEN), FIXTURE_ON).published).not.toContain('app-mac-arm64.zip');
  });

  it('leaves the chosen shape clean in both directions', () => {
    for (const mutant of [third(CHOSEN), without(CHOSEN)]) {
      const found = report(mutant, FIXTURE_ON);
      expect(found.trouble || [...found.desktopUnlisted, ...found.notBasenames, ...found.phantom].join(' | ')).toBe('');
    }
  });

  it('fails a list that types the names out, in both directions', () => {
    expect(report(third(TYPED_OUT), FIXTURE_ON).desktopUnlisted).toEqual(['app-mac-universal.zip']);
    expect(report(without(TYPED_OUT), FIXTURE_ON).phantom.join(' | '))
      .toContain('app-mac-arm64.zip, which nothing in this run stages');
  });
});

// ---------------------------------------------------------------------------
// The real workflows.
// ---------------------------------------------------------------------------

const readReal: Read = (name) => readFileSync(new URL(`../../.github/workflows/${name}`, import.meta.url), 'utf8');

// Whatever release.yml's own gate is spelled, this is the run where it is open.
// Derived from the file rather than named here, so the choice of switch stays
// where release-desktop-optin-workflow.test.ts left it: the implementer's.
const DESKTOP_ON: Context = {
  ...PLAIN_TAG,
  ...Object.fromEntries(Array.from(
    new Set(Array.from(readReal('release.yml').matchAll(/\bvars\.([A-Za-z_][A-Za-z_0-9]*)/g), (found) => found[1])),
    (name) => [`vars.${name}`, 'true'],
  )),
};

const enabled = report(readReal, DESKTOP_ON);
const plain = report(readReal, PLAIN_TAG);

const nothingPublished = 'the release run publishes nothing this test could read';
const verdict = (found: Report, answer: string) =>
  found.trouble !== '' ? found.trouble : found.published.length === 0 ? nothingPublished : answer;

describe('the release publishes a checksum list for the desktop bundles', () => {
  it('has a release run to read at all, with the bundles in it', () => {
    // Every rule under this one reads a list, and a list that came out empty
    // would satisfy all of them at once. This is that list, named out loud.
    expect(enabled.trouble || enabled.published.join(', ') || nothingPublished).toContain(CLI_SUMS);
    expect(enabled.desktopPublished.length).toBeGreaterThanOrEqual(3);
  });

  it('publishes one, from the job that attaches the bundles', () => {
    // Today: no such asset. The three bundles, 570 MB of them, are downloaded
    // and double-clicked with nothing anywhere to check them against.
    const answer = enabled.desktopListJob !== ''
      ? `${DESKTOP_SUMS} is published by ${enabled.desktopListJob}`
      : `nothing publishes ${DESKTOP_SUMS}: the bundles are downloaded and run with no checksum anywhere`;
    expect(verdict(enabled, answer)).toContain(`${DESKTOP_SUMS} is published by`);
  });

  it('and it names every bundle the run publishes', () => {
    expect(verdict(enabled, enabled.desktopUnlisted.join(' | ') || 'every desktop bundle is in the list'))
      .toBe('every desktop bundle is in the list');
  });

  it('writes bare basenames, which is what install.sh and install.ps1 look up', () => {
    expect(verdict(enabled, enabled.notBasenames.join(' | ') || 'every name in the list is a basename'))
      .toBe('every name in the list is a basename');
  });

  it('is written by a tool whose output those two can parse', () => {
    expect(verdict(enabled, enabled.unreadableWriter.join(' | ') || 'the lists are written as <hash>  <name>'))
      .toBe('the lists are written as <hash>  <name>');
  });

  it('hashes nothing the run does not hold, which would take the step with it', () => {
    expect(verdict(enabled, enabled.phantom.join(' | ') || 'hashes what the run staged'))
      .toBe('hashes what the run staged');
  });
});

describe('and the CLI list stays the creating job\'s alone', () => {
  // The rule that made two lists rather than one. It is green today and has to
  // survive the change: whoever later wonders why there are two must find this
  // rather than a free hand.
  it('is written and published by the job that creates the release, by nobody else', () => {
    expect(verdict(enabled, enabled.cliListTouched.join(' | ') || `${CLI_SUMS} is untouched by any other job`))
      .toBe(`${CLI_SUMS} is untouched by any other job`);
  });

  it('and still names the CLI assets it named before', () => {
    expect(enabled.trouble || enabled.cliListNames.join(', ')).toContain('version.json');
    expect(enabled.cliListNames.filter((name) => name.startsWith('vc-')).length).toBeGreaterThanOrEqual(6);
  });
});

describe('a third architecture tomorrow needs no edit to release.yml', () => {
  // The whole point. release.yml is read unchanged; only the build workflow is
  // mutated, exactly as a real edit would.
  const macWorkflow = 'desktop-mac-app.yml';
  const third = report(withEditedRow(readReal, macWorkflow, 'arm64', 'universal'), DESKTOP_ON);
  const without = report(withEditedRow(readReal, macWorkflow, 'arm64', null), DESKTOP_ON);

  it('the mutation lands: one more bundle, named by the matrix and not by this test', () => {
    const added = third.published.filter((asset) => !enabled.published.includes(asset));
    expect(third.trouble || added.join(', ')).toMatch(/universal/);
  });

  it('and the new bundle is in the list anyway', () => {
    expect(verdict(third, third.desktopUnlisted.join(' | ') || 'every desktop bundle is in the list'))
      .toBe('every desktop bundle is in the list');
  });

  it('and a bundle that goes away leaves no name pointing at a file nobody builds', () => {
    // The other direction of the same fault. A name written twice is a name that
    // can outlive what it named, and `sha256sum` on a missing path exits non-zero
    // and fails the step -- on the release, where it is worst.
    expect(without.trouble || without.phantom.join(' | ') || 'nothing points at a build that is gone')
      .toBe('nothing points at a build that is gone');
  });
});

describe('and an ordinary tag publishes exactly what it built', () => {
  it('builds nothing desktop, so it owes no desktop list', () => {
    expect(plain.trouble || plain.desktopPublished.join(', ') || 'nothing desktop is published')
      .toBe('nothing desktop is published');
    expect(plain.desktopListJob).toBe('');
  });

  it('and its CLI list is untouched and complete', () => {
    expect(plain.trouble || plain.cliListTouched.join(' | ') || `${CLI_SUMS} is untouched`).toBe(`${CLI_SUMS} is untouched`);
    expect(plain.cliListNames).toContain('version.json');
  });
});
