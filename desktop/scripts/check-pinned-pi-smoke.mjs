import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { assertNamedTestExists } from './pinned-pi-smoke-lib.mjs';

const NAME = 'TestPiVoidCodexExtensionSmoke';
const repo = path.resolve(import.meta.dirname, '../..');
const env = { ...process.env, VC_REQUIRE_PINNED_PI_SMOKE: '1' };

// Ask go what it would run before asking it to run it. `-run` on a filter that
// matches nothing exits 0, so without this step the whole qualification is
// satisfied by a test that is not there.
//
// Captured rather than inherited, because the decision is made on the text --
// and echoed straight back, so the CI log still shows exactly what it showed
// when this ran with stdio: 'inherit'.
const listing = spawnSync('go', ['test', './cmd/vc', '-list', `^${NAME}$`], { cwd: repo, env, encoding: 'utf8' });
if (listing.error) throw listing.error;
process.stdout.write(listing.stdout ?? '');
process.stderr.write(listing.stderr ?? '');
assertNamedTestExists({ name: NAME, output: `${listing.stdout ?? ''}\n${listing.stderr ?? ''}`, status: listing.status ?? 1 });

const result = spawnSync('go', ['test', './cmd/vc', '-run', `^${NAME}$`, '-count=1', '-v'], {
  cwd: repo,
  env,
  stdio: 'inherit',
});
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
