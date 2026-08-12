import { spawnSync } from 'node:child_process';
import path from 'node:path';

const repo = path.resolve(import.meta.dirname, '../..');
const result = spawnSync('go', ['test', './cmd/vc', '-run', '^TestPiVoidCodexExtensionSmoke$', '-count=1', '-v'], {
  cwd: repo,
  env: { ...process.env, VC_REQUIRE_PINNED_PI_SMOKE: '1' },
  stdio: 'inherit',
});
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
