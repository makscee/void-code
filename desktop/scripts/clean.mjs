import { rm } from 'node:fs/promises';

for (const target of ['dist', 'release', 'resources/staged', 'smoke-output.json']) await rm(target, { recursive: true, force: true });
