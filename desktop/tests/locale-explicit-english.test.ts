import { mkdtempSync, readFileSync, rmSync, statSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, expect, it } from 'vitest';
import { LocaleStore } from '../src/main/locale-store';
import { localeRequest } from '../src/shared/contract';

const roots: string[] = [];
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

it('validates and persists an explicit English selection across restart', () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'void-locale-en-')); roots.push(root);
  const first = new LocaleStore(root);
  expect(first.set(localeRequest({ locale: 'en' }))).toBe('en');
  expect(new LocaleStore(root).resolution()).toEqual({ locale: 'en', explicit: true });
  expect(JSON.parse(readFileSync(path.join(root, 'locale.json'), 'utf8'))).toEqual({ version: 1, locale: 'en' });
  if (process.platform !== 'win32') expect(statSync(path.join(root, 'locale.json')).mode & 0o777).toBe(0o600);
  expect(() => localeRequest({ locale: 'fr' })).toThrow('unsupported locale');
  expect(() => localeRequest({ locale: 'en', path: '/tmp/injected' })).toThrow('unknown or missing fields');
});
