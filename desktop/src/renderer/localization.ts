import { config, t } from '@vscode/l10n';
import type { L10nReplacement } from '@vscode/l10n';
import type { Locale } from '../shared/contract';
import english from './l10n/en.json';
import russian from './l10n/ru.json';

export function configureLocale(locale: Locale): void {
  config({ contents: locale === 'ru' ? russian : english });
}

export function translate(message: string, ...args: L10nReplacement[]): string {
  return t(message, ...args);
}

export async function persistLocaleSelection(
  selector: { value: string },
  currentLocale: Locale,
  setLocale: (locale: Locale) => Promise<Locale>,
  reload: () => void,
  announce: (message: string) => void,
  localize: (message: string) => string,
): Promise<void> {
  const requested: Locale = selector.value === 'en' ? 'en' : 'ru';
  try { await setLocale(requested); reload(); }
  catch { selector.value = currentLocale; announce(localize('Language could not be saved. Try again.')); }
}
