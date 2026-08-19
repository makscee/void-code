import { mkdtempSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, expect, it } from 'vitest';
import { LocaleStore } from '../src/main/locale-store';

const roots: string[] = [];
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

it('resolves a clean profile to implicit Russian before renderer startup', () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'void-locale-clean-')); roots.push(root);
  const store = new LocaleStore(root);
  expect(store.resolution()).toEqual({ locale: 'ru', explicit: false });
  expect(store.current()).toBe('ru');
});
