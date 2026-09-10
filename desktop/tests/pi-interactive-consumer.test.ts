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

it('source-hook fails closed when consumer constructor no longer acquires the extracted factory', () => {
  const source = readFileSync(interactiveFile, 'utf8');
  expect(() => consumerHooks(interactiveFile, source.replace('this.ui = createInteractiveTuiReference(() => this.renderer)', 'this.ui = this.renderer'))).toThrow('actual InteractiveMode constructor');
  expect(() => consumerHooks('missing-bundle.mjs', '')).toThrow('one actual reference factory');
});
