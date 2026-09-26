// Turns a Pi tree that npm built for win32 on a Unix host into the tree npm
// builds on Windows itself (makscee/void-board#162).
//
// `npm ci --os=win32` picks Windows' native packages, but it still links each
// package bin as a Unix symlink: npm writes the .cmd/.ps1 shims only when it
// runs on Windows. vc on Windows launches node_modules\.bin\pi.cmd, and a
// symlink in the archive would not unpack there without developer mode. So
// every bin symlink is replaced by the shims npm itself writes on Windows,
// made with npm's own cmd-shim, and any other symlink is an error.
//
// Usage: node scripts/pi-runtime-windows-shims.mjs <tree> <npm package dir>
import { createRequire } from 'node:module';
import { lstat, readdir, readlink, unlink } from 'node:fs/promises';
import path from 'node:path';

const [tree, npmDir] = process.argv.slice(2);
if (!tree || !npmDir) {
  console.error('usage: pi-runtime-windows-shims.mjs <tree> <npm package dir>');
  process.exit(2);
}
const cmdShim = createRequire(import.meta.url)(path.join(npmDir, 'node_modules', 'cmd-shim'));

async function* symlinks(dir) {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isSymbolicLink()) yield p;
    else if (entry.isDirectory()) yield* symlinks(p);
  }
}

let shims = 0;
for await (const link of symlinks(tree)) {
  if (path.basename(path.dirname(link)) !== '.bin') {
    throw new Error(`symlink outside a .bin folder: ${link}`);
  }
  const target = path.resolve(path.dirname(link), await readlink(link));
  if (!(await lstat(target)).isFile()) {
    throw new Error(`${link} points at ${target}, which is not a file`);
  }
  await unlink(link);
  await cmdShim(target, link);
  shims++;
}
await lstat(path.join(tree, 'node_modules', '.bin', 'pi.cmd'));
console.log(`${tree}: ${shims} bin links replaced by Windows shims`);
