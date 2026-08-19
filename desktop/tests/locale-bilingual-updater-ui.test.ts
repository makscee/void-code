import { expect, it } from 'vitest';
import { configureLocale, translate } from '../src/renderer/localization';
import { updateStatusText } from '../src/renderer/update-view';

it('renders every updater state and action coherently in Russian and English', () => {
  const statuses = [
    { state: 'checking', currentVersion: '1.0.0', canRetry: false },
    { state: 'up-to-date', currentVersion: '1.0.0', canRetry: false },
    { state: 'available', currentVersion: '1.0.0', availableVersion: '1.1.0', canRetry: false },
    { state: 'downloading', currentVersion: '1.0.0', availableVersion: '1.1.0', percent: 42, transferred: 42, total: 100, canRetry: false },
    { state: 'verifying', currentVersion: '1.0.0', availableVersion: '1.1.0', canRetry: false },
    { state: 'installing', currentVersion: '1.0.0', availableVersion: '1.1.0', canRetry: false },
    { state: 'failed', currentVersion: '1.0.0', canRetry: true },
    { state: 'unavailable', currentVersion: '1.0.0', canRetry: true },
  ] as const;
  configureLocale('ru');
  const russian = statuses.map((status) => updateStatusText(status, translate));
  expect(russian.map((item) => item.status).join(' ')).toMatch(/[А-Яа-яЁё]/);
  expect(russian.every((item) => !/[A-Za-z]{3,}/.test(item.status))).toBe(true);
  configureLocale('en');
  const english = statuses.map((status) => updateStatusText(status, translate));
  expect(english.map((item) => item.status).join(' ')).toContain('Checking for updates');
  expect(english.map((item) => item.action)).toContain('Retry update');
  expect(new Set(russian.map((item) => item.status))).toHaveLength(statuses.length);
});
