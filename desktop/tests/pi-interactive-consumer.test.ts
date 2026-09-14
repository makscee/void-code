import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { transformSync } from 'esbuild';
import { expect, it } from 'vitest';
import { interactiveFile } from './fixtures/pi-fullscreen-clipboard';
import { consumerHooks } from './fixtures/pi-interactive-consumer';

it('source-hook provenance: exact installed factory body and both actual widget methods; no full module evaluation', () => {
  const source = readFileSync(interactiveFile, 'utf8');
  const hooks = consumerHooks(interactiveFile);
  expect(hooks.sha256).toBe(createHash('sha256').update(source).digest('hex'));
  expect(source).toContain(hooks.factory);
  expect([...hooks.methods.keys()]).toEqual(['setExtensionWidget', 'clearExtensionWidgets']);
  for (const method of hooks.methods.values()) expect(source).toContain(method);
});

it('source-hook control: esbuild class-expression syntax from actual module is supported (not bundled acceptance)', () => {
  const source = readFileSync(interactiveFile, 'utf8');
  const transformed = transformSync(source, { format: 'cjs', target: 'node22' }).code;
  const hooks = consumerHooks('actual-module-transformed.cjs', transformed);
  const createReference = new Function(`${hooks.factory}; return ${hooks.name};`)();
  const renderer = { value: 1, method() { return this.value; } };
  const ui = createReference(() => renderer);
  expect(ui.method.call({ value: 99 })).toBe(1);
  expect(ui.method).not.toBe(ui.method);
});

// Windows run 34439693743 / job 102752474588, artifact 10137645109:
// ZIP SHA256 94f334134d4ea8c823a8b93173bd7af6a88e5d6b367132c3eb672cd822617b87.
// pi/agent/pi~BUN.mjs:346473 uses `InteractiveMode = class _InteractiveMode {`;
// :346607 acquires createInteractiveTuiReference(() => this.renderer).
// Reproduce only that wrapper; retain the installed factory and entire class body.
function observedBindingSource() {
  const source = readFileSync(interactiveFile, 'utf8');
  const header = 'export class InteractiveMode {';
  expect(source.split(header)).toHaveLength(2);
  return source.replace(header, 'let InteractiveMode; InteractiveMode = class _InteractiveMode {');
}

it('source-hook regression: observed esbuild outer binding owns the actual constructor and widget bodies', () => {
  const source = observedBindingSource();
  const installed = consumerHooks(interactiveFile);
  const hooks = consumerHooks('observed-binding.mjs', source);
  expect(hooks.sha256).toBe(createHash('sha256').update(source).digest('hex'));
  expect(hooks.factory).toBe(installed.factory);
  expect([...hooks.methods]).toEqual([...installed.methods]);
  const createReference = new Function(`${hooks.factory}; return ${hooks.name};`)();
  const renderer = { value: 1, method() { return this.value; } };
  expect(createReference(() => renderer).method.call({ value: 99 })).toBe(1);
});

it.each([
  ['raw renderer', 'this.ui = this.renderer'],
  ['wrong factory', 'this.ui = unrelatedFactory(() => this.renderer)'],
  ['factory call outside constructor', 'this.ui = this.renderer; this.later = () => createInteractiveTuiReference(() => this.renderer)'],
])('observed binding fails closed: %s', (_label, replacement) => {
  const source = observedBindingSource();
  const acquisition = 'this.ui = createInteractiveTuiReference(() => this.renderer)';
  expect(source.split(acquisition)).toHaveLength(2);
  expect(() => consumerHooks('observed-binding.mjs', source.replace(acquisition, replacement)))
    .toThrow('actual InteractiveMode constructor');
});

it.each([
  'let InteractiveMode, Unrelated; Unrelated = class _InteractiveMode {',
  'let InteractiveMode, Unrelated; Unrelated = class _Unrelated {',
])('unrelated class or alias cannot establish consumer provenance: %s', (header) => {
  const source = observedBindingSource().replace(
    'let InteractiveMode; InteractiveMode = class _InteractiveMode {', header,
  ) + '\nInteractiveMode = Unrelated;';
  expect(() => consumerHooks('unrelated-binding.mjs', source)).toThrow('actual InteractiveMode constructor');
});

it('source-hook fails closed when consumer constructor no longer acquires the extracted factory', () => {
  const source = readFileSync(interactiveFile, 'utf8');
  expect(() => consumerHooks(interactiveFile, source.replace('this.ui = createInteractiveTuiReference(() => this.renderer)', 'this.ui = this.renderer'))).toThrow('actual InteractiveMode constructor');
  expect(() => consumerHooks('missing-bundle.mjs', '')).toThrow('one actual reference factory');
});
