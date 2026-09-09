import { existsSync, readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { describe, expect, it } from 'vitest';

// js-yaml is already locked by the repository's ESLint/builder toolchain. Parse
// YAML rather than allowing comments, unrelated jobs, or quoted text to satisfy it.
const loadDependency = createRequire(import.meta.url);
const { load } = loadDependency('js-yaml') as { load: (source: string) => unknown };
const file = new URL('../../.github/workflows/desktop-update-native-probe.yml', import.meta.url);
type RecordValue = Record<string, unknown>;
function object(value: unknown): RecordValue {
  expect(value !== null && typeof value === 'object' && !Array.isArray(value)).toBe(true);
  return value as RecordValue;
}
function strings(value: unknown): string[] {
  expect(Array.isArray(value)).toBe(true);
  const items = value as unknown[];
  for (const item of items) expect(typeof item).toBe('string');
  return items as string[];
}
function workflow() {
  expect(existsSync(file), 'missing dedicated native probe workflow: desktop-update-native-probe.yml').toBe(true);
  return object(load(readFileSync(file, 'utf8')));
}
function nativeJob(w: RecordValue) {
  const jobs = Object.values(object(w.jobs));
  expect(jobs, 'one bounded native matrix job, no hidden publication job').toHaveLength(1);
  const job = object(jobs[0]);
  expect(Array.isArray(job.steps)).toBe(true);
  return { job, steps: (job.steps as unknown[]).map(object) };
}
const pins = new Map([
  ['actions/checkout', '11d5960a326750d5838078e36cf38b85af677262'],
  ['actions/setup-node', '49933ea5288caeca8642d1e84afbd3f7d6820020'],
  ['actions/setup-go', '40f1582b2485089dde7abd97c1529aa768e1baff'],
  ['actions/upload-artifact', 'ea165f8d65b6e75b540449e92b4886f43607fa02'],
]);
function action(steps: RecordValue[], name: string) {
  const found = steps.filter((step) => step.uses === `${name}@${pins.get(name)}`);
  expect(found, `exact pinned ${name}`).toHaveLength(1);
  return found[0]!;
}
function matchesPath(glob: string, path: string) {
  const pattern = glob.split('**').map((part) => part.split('*')
    .map((literal) => literal.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('[^/]*')).join('.*');
  return new RegExp(`^${pattern}$`).test(path);
}
function command(step: RecordValue) { return typeof step.run === 'string' ? step.run : ''; }
function env(...scopes: RecordValue[]) {
  return Object.assign({}, ...scopes.map((scope) => scope.env === undefined ? {} : object(scope.env))) as RecordValue;
}

describe('dedicated native S0 workflow semantics (production workflows are not inspected or changed)', () => {
  it('runs on the experiment branch and relevant PR paths, with read-only permissions', () => {
    const w = workflow();
    const triggers = object(w.on);
    expect(Object.keys(triggers).sort().every((key) => ['push', 'pull_request', 'workflow_dispatch'].includes(key))).toBe(true);
    expect(strings(object(triggers.push).branches)).toEqual(['work/desktop-update-native']);
    const pr = object(triggers.pull_request);
    expect(pr['paths-ignore']).toBeUndefined();
    const paths = strings(pr.paths);
    expect(paths.some((path) => path.startsWith('!'))).toBe(false);
    for (const relevant of [
      '.github/workflows/desktop-update-native-probe.yml', 'go.mod', 'go.sum',
      'desktop/package.json', 'desktop/package-lock.json',
      'desktop/experiments/update-native/main.go',
      'desktop/tests/native-update-probe.test.ts', 'desktop/tests/native-update-workflow.test.ts',
      'desktop/tests/fixtures/native-update/main.mjs', 'desktop/tests/fixtures/native-update/tooling.ts',
      'desktop/tests/fixtures/native-update/nsis-fault.nsh', 'docs/desktop-auto-update-native-red-plan.md',
    ]) expect(paths.some((pattern) => matchesPath(pattern, relevant)), `PR filter covers ${relevant}`).toBe(true);
    expect(object(w.permissions)).toEqual({ contents: 'read' });
    const { job } = nativeJob(w);
    if (job.permissions !== undefined) expect(object(job.permissions)).toEqual({ contents: 'read' });
    expect(job.if, 'qualification must not be conditionally bypassed').toBeUndefined();
    const timeout = job['timeout-minutes'];
    expect(typeof timeout).toBe('number');
    expect(timeout as number).toBeGreaterThanOrEqual(10);
    expect(timeout as number).toBeLessThanOrEqual(60);
    const matrix = object(object(job.strategy).matrix);
    expect(strings(matrix.os).sort()).toEqual(['macos-14', 'windows-latest']);
    expect(matrix.exclude).toBeUndefined();
    expect(matrix.include).toBeUndefined();
    expect(String(job['runs-on']).replace(/\s/g, '')).toBe('${{matrix.os}}');
  });

  it('uses pinned actions, Node 22, go.mod, ignore-scripts and explicit pinned Electron provisioning', () => {
    const w = workflow();
    const { job, steps } = nativeJob(w);
    for (const step of steps) if (step.uses !== undefined) {
      expect([...pins].some(([name, sha]) => step.uses === `${name}@${sha}`), `unapproved/unpinned action ${String(step.uses)}`).toBe(true);
    }
    const checkout = action(steps, 'actions/checkout');
    expect(object(checkout.with)['persist-credentials']).toBe(false);
    expect(String(object(action(steps, 'actions/setup-node').with)['node-version'])).toMatch(/^22(?:\.\d+){0,2}$/);
    expect(object(action(steps, 'actions/setup-go').with)['go-version-file']).toBe('go.mod');
    const ci = steps.findIndex((step) => /\bnpm\s+ci\s+--ignore-scripts\b/.test(command(step)));
    expect(ci).toBeGreaterThanOrEqual(0);
    const provisioning = steps.findIndex((step) => /\bnode\s+(?:\.\/)?node_modules\/electron\/install\.js\b/.test(command(step)));
    expect(provisioning, 'run the lockfile-pinned Electron install.js explicitly after ignore-scripts').toBeGreaterThan(ci);
    const pkg = object(JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8')));
    expect(object(pkg.devDependencies).electron).toBe('41.10.3');
    for (const index of [ci, provisioning]) {
      const step = steps[index]!;
      expect(step.if, 'dependency setup must run on both matrix hosts').toBeUndefined();
      const working = step['working-directory']
        ?? object(object(job.defaults ?? {}).run ?? {})['working-directory']
        ?? object(object(w.defaults ?? {}).run ?? {})['working-directory'];
      expect(working).toBe('desktop');
      expect(command(step)).not.toMatch(/\becho\b|\bWrite-Output\b/);
    }
  });

  it('actually executes both tests with native opt-in on both hosts and writes a JSON report', () => {
    const w = workflow();
    const { job, steps } = nativeJob(w);
    const executions = steps.filter((step) => /\bnpm\s+test\s+--\s/.test(command(step)));
    expect(executions).toHaveLength(1);
    const step = executions[0]!;
    const run = command(step);
    expect(run).toMatch(/tests\/native-update-probe\.test\.ts\b/);
    expect(run).toMatch(/tests\/native-update-workflow\.test\.ts\b/);
    expect(run).toMatch(/--reporter(?:=|\s+)json\b/);
    expect(run).toMatch(/--outputFile(?:=|\s+)\S*vitest-report\.json\b/);
    expect(String(env(w, job, step).VOID_NATIVE_UPDATE_PROBE)).toBe('1');
    expect(step.if).toBeUndefined();
    const working = step['working-directory']
      ?? object(object(job.defaults ?? {}).run ?? {})['working-directory']
      ?? object(object(w.defaults ?? {}).run ?? {})['working-directory'];
    expect(working).toBe('desktop');
    // Environment-based opt-in works in both bash and PowerShell; no POSIX-only
    // assignment is mistaken for a Windows execution contract.
    expect(run).not.toMatch(/--(?:passWithNoTests|help|version)|\becho\b|\bWrite-Output\b/);
    const provision = steps.findIndex((candidate) => /node_modules\/electron\/install\.js/.test(command(candidate)));
    expect(steps.indexOf(step)).toBeGreaterThan(provision);
  });

  it('always uploads only explicit diagnostic file globs and cannot publish or bypass failures', () => {
    const w = workflow();
    const { job, steps } = nativeJob(w);
    const upload = action(steps, 'actions/upload-artifact');
    expect(String(upload.if).replace(/\s|\$\{\{|\}\}/g, '')).toBe('always()');
    const options = object(upload.with);
    const paths = String(options.path).split(/\r?\n/).map((path) => path.trim()).filter(Boolean);
    expect(paths.length).toBeGreaterThan(0);
    for (const path of paths) {
      expect(path, 'allowlisted diagnostics tree, never packages/capsules/installers').toMatch(/^(?:\.\/)?artifacts\/native-update\//);
      expect(path).toMatch(/\.(?:json|log|txt|xml)$/);
      expect(path).not.toMatch(/\.\.|packages|installers|\.exe|\.zip|\.dmg|\.app/);
    }
    expect(options['include-hidden-files']).not.toBe(true);
    for (const scope of [w, job, ...steps]) {
      expect(scope['continue-on-error']).toBeUndefined();
      expect(scope.secrets).toBeUndefined();
      expect(scope.environment).toBeUndefined();
    }
    // Check executable/semantic values, not comments or descriptive step names.
    const execution = JSON.stringify({ env: env(w, job), steps: steps.map((step) => ({ run: step.run, env: step.env, with: step.with })) });
    expect(execution).not.toMatch(/secrets\.|write-all|id-token|npm\s+publish|gh\s+release|git\s+(?:push|tag)|--publish|deploy|curl\b|Invoke-WebRequest|workflow_call/i);
    expect(steps.map(command).join('\n')).not.toMatch(/\|\|\s*(?:true|exit\s+0)|exit\s+0|\$LASTEXITCODE\s*=\s*0|ErrorActionPreference\s*=\s*['"](?:Continue|SilentlyContinue)/i);
  });
});
