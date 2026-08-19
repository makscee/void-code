import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const read = (relative: string) => readFileSync(new URL(`../${relative}`, import.meta.url), 'utf8');
const english = JSON.parse(read('src/renderer/l10n/en.json')) as Record<string, string>;
const russian = JSON.parse(read('src/renderer/l10n/ru.json')) as Record<string, string>;
const placeholders = (value: string) => [...value.matchAll(/\{\w+\}/g)].map((match) => match[0]).sort();

describe('authoritative bundled localization coverage', () => {
  it('has a coherent Russian translation and matching placeholders for every English source message', () => {
    expect(Object.keys(russian).sort()).toEqual(Object.keys(english).sort());
    for (const [source, fallback] of Object.entries(english)) {
      expect(fallback, source).toBe(source);
      expect(russian[source]?.trim(), source).toBeTruthy();
      expect(placeholders(russian[source]), source).toEqual(placeholders(source));
    }
  });

  it('covers every renderer translation call, static text marker, and recovery message', () => {
    const sources = [read('src/renderer/index.ts'), read('src/renderer/update-view.ts'), read('src/renderer/index.html'), read('src/renderer/recovery.ts'), read('src/main/startup-diagnostic.ts'), read('src/main/index.ts')];
    const messages = new Set<string>();
    for (const source of sources) {
      for (const match of source.matchAll(/\bt\('([^']+)'/g)) messages.add(match[1]);
      for (const match of source.matchAll(/data-l10n(?:-aria)?="([^"]+)"/g)) messages.add(match[1]);
      for (const match of source.matchAll(/(?:heading|detail): '([^']*)'/g)) if (match[1]) messages.add(match[1]);
      for (const match of source.matchAll(/startupDialogMessage\(t\)/g)) expect(match[0]).toBeTruthy();
    }
    expect([...messages].filter((message) => !(message in russian))).toEqual([]);
  });

  it('uses only local bundles with @vscode/l10n and carries an auditable MIT notice', () => {
    const runtime = read('src/renderer/localization.ts'); const notice = read('THIRD_PARTY_NOTICES.md'); const provenance = read('docs/localization.md'); const copy = read('scripts/copy-static.mjs');
    expect(runtime).toContain("from '@vscode/l10n'");
    expect(runtime).toContain("./l10n/ru.json"); expect(runtime).toContain("./l10n/en.json");
    expect(runtime).not.toMatch(/fetch\(|Marketplace|language.?pack|uri:/i);
    expect(notice).toContain('@vscode/l10n 0.0.18'); expect(notice).toContain('https://github.com/microsoft/vscode-l10n/tree/v0.0.18'); expect(notice).toContain('MIT');
    expect(notice).toContain('does not embed the VS Code workbench');
    for (const phrase of ['schema version 1', 'first-party Void Code product', 'no separate third-party license']) expect(`${notice}\n${provenance}`.toLowerCase()).toContain(phrase.toLowerCase());
    expect(provenance).toContain('no Microsoft/CEINTL VS Code language-pack content');
    expect(copy).toContain("['en', 'ru']"); expect(copy).toContain('THIRD_PARTY_NOTICES.md');
  });
});
