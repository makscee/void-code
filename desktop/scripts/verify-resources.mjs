import path from 'node:path';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { resolvePrivateRuntime } = require('../dist/main/resources.js');
const root = path.resolve(process.argv[3] ?? 'resources/staged');
const target = process.argv[4];
if (target !== undefined && target !== 'darwin-arm64' && target !== 'win32-x64') throw new Error('invalid verification target');
const runtime = resolvePrivateRuntime(root, target);
if (!runtime.node.startsWith(root) || !runtime.piEntry.startsWith(root) || !runtime.vc.startsWith(root)) throw new Error('resource lookup escaped private root');
console.log(JSON.stringify({ platform: runtime.manifest.platform, vc: runtime.manifest.vc.sha256, node: runtime.manifest.node.sha256, pi: runtime.manifest.pi.treeSha256 }));
