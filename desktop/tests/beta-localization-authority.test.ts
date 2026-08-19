import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { updateStatusText } from '../src/renderer/update-view';
const read = (file: string) => readFileSync(new URL(`../${file}`, import.meta.url), 'utf8');

describe('closed-beta disclosure and renderer authority', () => {
  it('labels prerelease trust without claiming Windows publisher trust in EN and RU', () => {
    const en = JSON.parse(read('src/renderer/l10n/en.json')) as Record<string, string>;
    const ru = JSON.parse(read('src/renderer/l10n/ru.json')) as Record<string, string>;
    const status = { state: 'unavailable' as const, currentVersion: '0.1.3-beta.1', canRetry: true as const };
    expect(updateStatusText(status, (key, ...args) => (en[key] ?? key).replace('{0}', String(args[0] ?? ''))).current).toContain('does not establish Windows publisher trust');
    expect(updateStatusText(status, (key, ...args) => (ru[key] ?? key).replace('{0}', String(args[0] ?? ''))).current).toContain('не подтверждает доверие Windows к издателю');
  });

  it('keeps trust selection in main source with no renderer/IPC/environment input', () => {
    const main = read('src/main/index.ts'); const preload = read('src/preload/index.ts'); const contract = read('src/shared/preload-contract.ts');
    expect(main).toContain("compiledUpdateTrustMode(app.getVersion())");
    expect(main).not.toMatch(/compiledUpdateTrustMode\((?:process\.env|process\.argv|raw|event)/);
    expect(preload).not.toContain('trustMode'); expect(contract).not.toContain('trustMode');
  });
});
