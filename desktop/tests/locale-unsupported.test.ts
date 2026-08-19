import { mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, expect, it } from 'vitest';
import { LocaleStore } from '../src/main/locale-store';

const roots: string[] = [];
function root() { const value = mkdtempSync(path.join(os.tmpdir(), 'void-locale-invalid-')); roots.push(value); return value; }
afterEach(() => { for (const value of roots.splice(0)) rmSync(value, { recursive: true, force: true }); });

it('recovers unsupported/corrupt primary from last valid explicit locale and quarantines it', () => {
  const userData = root(); const store = new LocaleStore(userData); store.set('en');
  writeFileSync(path.join(userData, 'locale.json'), '{"version":1,"locale":"fr"}\n');
  expect(new LocaleStore(userData).resolution()).toEqual({ locale: 'en', explicit: true });
  expect(readdirSync(userData).some((name) => name.startsWith('locale.invalid-') && name.endsWith('.json'))).toBe(true);

  const clean = root(); writeFileSync(path.join(clean, 'locale.json'), 'not-json');
  expect(new LocaleStore(clean).resolution()).toEqual({ locale: 'ru', explicit: false });
});

it('does not change current or restart state when persistence fails', () => {
  const userData = root(); const store = new LocaleStore(userData);
  mkdirSync(path.join(userData, 'locale.json'));
  expect(() => store.set('en')).toThrow();
  expect(store.current()).toBe('ru');
  expect(new LocaleStore(userData).resolution()).toEqual({ locale: 'ru', explicit: false });
});
