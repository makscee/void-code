import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

export function nsisUtf8Environment(platform, environment) {
  if (platform !== 'linux') return environment;
  return { ...environment, LANG: 'C.UTF-8', LC_ALL: 'C.UTF-8' };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const result = spawnSync(process.execPath, ['node_modules/electron-builder/cli.js', '--win', '--x64'], {
    cwd: process.cwd(), env: nsisUtf8Environment(process.platform, process.env), stdio: 'inherit',
  });
  if (result.error) throw result.error;
  process.exitCode = result.status ?? 1;
}
