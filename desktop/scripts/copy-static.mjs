import { cp, mkdir } from 'node:fs/promises';

await mkdir('dist/renderer', { recursive: true });
for (const name of ['index.html', 'smoke.html']) await cp(`src/renderer/${name}`, `dist/renderer/${name}`);
