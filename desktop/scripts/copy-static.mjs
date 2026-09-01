import { cp, mkdir } from 'node:fs/promises';

await mkdir('dist/renderer', { recursive: true });
for (const name of ['index.html', 'smoke.html', 'splash.html', 'splash.css']) await cp(`src/renderer/${name}`, `dist/renderer/${name}`);

// Plain JS the compiler does not emit: tsc reads only src/**/*.ts, and these two must sit next to
// the compiled main process because a worker thread loads them by relative path.
await mkdir('dist/main', { recursive: true });
for (const name of ['private-runtime.js', 'private-runtime-worker.js']) await cp(`src/main/${name}`, `dist/main/${name}`);
