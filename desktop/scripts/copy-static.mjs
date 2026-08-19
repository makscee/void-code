import { cp, mkdir } from 'node:fs/promises';

await mkdir('dist/renderer/l10n', { recursive: true });
for (const name of ['index.html', 'smoke.html']) await cp(`src/renderer/${name}`, `dist/renderer/${name}`);
for (const locale of ['en', 'ru']) await cp(`src/renderer/l10n/${locale}.json`, `dist/renderer/l10n/${locale}.json`);
await cp('THIRD_PARTY_NOTICES.md', 'dist/THIRD_PARTY_NOTICES.md');
