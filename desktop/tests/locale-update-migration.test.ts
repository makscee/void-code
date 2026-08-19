import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, expect, it } from 'vitest';
import { LocaleStore } from '../src/main/locale-store';

const roots: string[] = [];
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

it('keeps locale solely in userData across old-to-new update migration semantics', () => {
  const legacy = mkdtempSync(path.join(os.tmpdir(), 'void-locale-legacy-')); roots.push(legacy);
  writeFileSync(path.join(legacy, 'workspace.json'), '{"version":1,"workspace":null}\n');
  expect(new LocaleStore(legacy).resolution()).toEqual({ locale: 'ru', explicit: false });

  const selected = new LocaleStore(legacy); selected.set('en');
  writeFileSync(path.join(legacy, 'updater-temporary.exe'), 'old updater bytes');
  rmSync(path.join(legacy, 'updater-temporary.exe'));
  expect(new LocaleStore(legacy).resolution()).toEqual({ locale: 'en', explicit: true });
  expect(readFileSync(path.join(legacy, 'locale.json'), 'utf8')).toContain('"en"');

  const main = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');
  expect(main).toContain('configureLocaleBeforeReady(');
  expect(main.indexOf('configureLocaleBeforeReady(')).toBeLessThan(main.indexOf("startupStage('runtime-validation'"));
});
