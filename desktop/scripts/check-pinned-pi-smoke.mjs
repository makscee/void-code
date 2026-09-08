import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { assertNamedTestExists } from './pinned-pi-smoke-lib.mjs';

const NAMES = ['TestPiVoidCodexExtensionSmoke', 'TestPiVoidCodeUIExtensionSmoke'];
const FILTER = `^(${NAMES.join('|')})$`;
const repo = path.resolve(import.meta.dirname, '../..');
const env = { ...process.env, VC_REQUIRE_PINNED_PI_SMOKE: '1' };

// Ask go what it would run before asking it to run it. `-run` on a filter that
// matches nothing exits 0, so without this step the whole qualification is
// satisfied by tests that are not there.
//
// Captured rather than inherited, because the decision is made on the text --
// and echoed straight back, so the CI log still shows exactly what it showed
// when this ran with stdio: 'inherit'.
const listing = spawnSync('go', ['test', './cmd/vc', '-list', FILTER], { cwd: repo, env, encoding: 'utf8' });
if (listing.error) throw listing.error;
process.stdout.write(listing.stdout ?? '');
process.stderr.write(listing.stderr ?? '');
for (const name of NAMES) {
  assertNamedTestExists({ name, output: `${listing.stdout ?? ''}\n${listing.stderr ?? ''}`, status: listing.status ?? 1 });
}

const result = spawnSync('go', ['test', './cmd/vc', '-run', FILTER, '-count=1', '-v'], {
  cwd: repo,
  env,
  stdio: 'inherit',
});
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
