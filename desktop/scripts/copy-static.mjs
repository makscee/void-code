import { cp, mkdir } from 'node:fs/promises';

await mkdir('dist/renderer', { recursive: true });
for (const name of ['index.html', 'index.css', 'smoke.html']) await cp(`src/renderer/${name}`, `dist/renderer/${name}`);
await cp('node_modules/@xterm/xterm/lib/xterm.js', 'dist/renderer/xterm.js');
await cp('node_modules/@xterm/xterm/css/xterm.css', 'dist/renderer/xterm.css');
