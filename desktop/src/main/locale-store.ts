import { closeSync, existsSync, fsyncSync, mkdirSync, openSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';

export type Locale = 'ru' | 'en';
export interface LocaleResolution { locale: Locale; explicit: boolean }

const supported = new Set<Locale>(['ru', 'en']);

export function localeValue(value: unknown): Locale {
  if (value !== 'ru' && value !== 'en') throw new Error('unsupported locale');
  return value;
}

export async function configureLocaleBeforeReady(
  userData: string,
  configure: (locale: Locale) => void,
  whenReady: () => Promise<unknown>,
  create: (userData: string) => LocaleStore = (root) => new LocaleStore(root),
): Promise<LocaleStore> {
  const store = create(userData);
  configure(store.current());
  await whenReady();
  return store;
}

export class LocaleStore {
  private state: LocaleResolution;
  private readonly primary: string;
  private readonly backup: string;

  constructor(private readonly userData: string) {
    this.primary = path.join(userData, 'locale.json');
    this.backup = path.join(userData, 'locale.last-valid.json');
    this.state = this.load();
  }

  current(): Locale { return this.state.locale; }
  resolution(): LocaleResolution { return { ...this.state }; }

  set(locale: Locale): Locale {
    localeValue(locale);
    const document = `${JSON.stringify({ version: 1, locale })}\n`;
    let previousBackup: string | undefined;
    try { previousBackup = readFileSync(this.backup, 'utf8'); } catch { /* no last-valid preference */ }
    this.atomicWrite(this.backup, document);
    try { this.atomicWrite(this.primary, document); } catch (error) {
      try {
        if (previousBackup === undefined) rmSync(this.backup, { force: true });
        else this.atomicWrite(this.backup, previousBackup);
      } catch { /* preserve the original persistence failure */ }
      throw error;
    }
    this.state = { locale, explicit: true };
    return locale;
  }

  private read(file: string): Locale | undefined {
    try {
      const parsed = JSON.parse(readFileSync(file, 'utf8')) as { version?: unknown; locale?: unknown };
      return parsed.version === 1 && supported.has(parsed.locale as Locale) ? parsed.locale as Locale : undefined;
    } catch { return undefined; }
  }

  private load(): LocaleResolution {
    const primary = this.read(this.primary);
    if (primary) return { locale: primary, explicit: true };
    if (existsSync(this.primary)) {
      try { renameSync(this.primary, path.join(this.userData, `locale.invalid-${Date.now()}.json`)); } catch { /* invalid preference remains inert */ }
    }
    const backup = this.read(this.backup);
    return backup ? { locale: backup, explicit: true } : { locale: 'ru', explicit: false };
  }

  private atomicWrite(destination: string, contents: string): void {
    mkdirSync(this.userData, { recursive: true, mode: 0o700 });
    const temporary = `${destination}.${process.pid}.tmp`;
    writeFileSync(temporary, contents, { encoding: 'utf8', mode: 0o600 });
    const file = openSync(temporary, 'r+');
    try { fsyncSync(file); } finally { closeSync(file); }
    renameSync(temporary, destination);
    if (process.platform !== 'win32') {
      const directory = openSync(this.userData, 'r');
      try { fsyncSync(directory); } finally { closeSync(directory); }
    }
  }
}
