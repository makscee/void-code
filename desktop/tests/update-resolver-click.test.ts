import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const read = (relative: string) => readFileSync(new URL(`../${relative}`, import.meta.url), 'utf8');

describe('updater has no browser fallback', () => {
  it('exposes only a no-argument Update now IPC and never routes updater actions to shell.openExternal', () => {
    const contract = read('src/shared/preload-contract.ts');
    const preload = read('src/preload/index.ts');
    const main = read('src/main/index.ts');
    expect(contract).toContain("updateInstall: 'update:install'");
    expect(contract).not.toContain('updateOpen');
    expect(preload).toContain('install: () => ipcRenderer.invoke(IPC.updateInstall)');
    expect(main).not.toMatch(/stableUpdate\.(?:openDownload|openExternal)/);
    expect(read('src/renderer/l10n/en.json')).toContain('"Update now": "Update now"');
    expect(read('src/renderer/l10n/ru.json')).toContain('"Update now": "Обновить сейчас"');
    expect(read('src/renderer/index.html')).not.toContain('Download update');
  });
});
