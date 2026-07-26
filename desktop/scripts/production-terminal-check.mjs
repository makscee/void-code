import { execFile, execFileSync } from 'node:child_process';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

if (process.platform !== 'darwin' || process.arch !== 'arm64') throw new Error('production terminal check supports only macOS-arm64');
const appBinary = path.resolve('release/mac-arm64/Void Code.app/Contents/MacOS/Void Code');
const asar = path.resolve('release/mac-arm64/Void Code.app/Contents/Resources/app.asar');
const listing = execFileSync(path.resolve('node_modules/.bin/asar'), ['list', asar], { encoding: 'utf8' });
for (const asset of ['/dist/renderer/index.html', '/dist/renderer/index.css', '/dist/renderer/index.js']) {
  if (!listing.includes(asset)) throw new Error(`packaged production renderer asset missing: ${asset}`);
}
if (!/\/dist\/renderer\/assets\/jetbrains-mono-[^/]+\.woff2/.test(listing)) throw new Error('packaged JetBrains Mono font missing');
const temporary = await mkdtemp(path.join(os.tmpdir(), 'vc-production-terminal-check-'));
async function runProbe(name, perturb) {
  const output = path.join(temporary, `${name}.json`);
  const args = [`--void-production-terminal-output=${output}`, ...(perturb ? [`--void-production-terminal-perturb=${perturb}`] : [])];
  const child = execFile(appBinary, args, { env: { ...process.env, ELECTRON_RUN_AS_NODE: undefined } });
  let stderr = ''; child.stderr?.on('data', (chunk) => { stderr += String(chunk); });
  const exit = await Promise.race([
    new Promise((resolve) => child.on('exit', (code, signal) => resolve({ code, signal }))),
    new Promise((_, reject) => setTimeout(() => { child.kill('SIGKILL'); reject(new Error(`${name} production terminal probe timed out`)); }, 60_000)),
  ]);
  const result = JSON.parse(await readFile(output, 'utf8'));
  return { exit, result, stderr };
}
try {
  const baseline = await runProbe('baseline');
  if (baseline.exit.code !== 0 || !baseline.result.ok) throw new Error(JSON.stringify({ name: 'baseline', ...baseline }));

  const missingFont = await runProbe('missing-font', 'missing-font');
  if (missingFont.exit.code === 0 || missingFont.result.ok || missingFont.result.assertions.bundledFontLoaded !== false) {
    throw new Error(JSON.stringify({ name: 'missing-font sensitivity', ...missingFont }));
  }

  const paletteCollapse = await runProbe('palette-collapse', 'palette-collapse');
  const failedPaletteAssertions = Object.entries(paletteCollapse.result.assertions).filter(([, passed]) => !passed).map(([assertion]) => assertion);
  if (paletteCollapse.exit.code === 0 || paletteCollapse.result.ok || JSON.stringify(failedPaletteAssertions) !== JSON.stringify(['realVisibleColor'])) {
    throw new Error(JSON.stringify({ name: 'palette-collapse sensitivity', failedPaletteAssertions, ...paletteCollapse }));
  }

  console.log(JSON.stringify({
    baseline: { exit: baseline.exit, assertions: baseline.result.assertions, integration: baseline.result.integration, fixture: baseline.result.fixture, realPi: baseline.result.realPi, recentGeometry: baseline.result.recentGeometry },
    sensitivities: {
      missingFont: { exit: missingFont.exit, failedAssertions: Object.entries(missingFont.result.assertions).filter(([, passed]) => !passed).map(([assertion]) => assertion) },
      paletteCollapse: { exit: paletteCollapse.exit, failedAssertions: failedPaletteAssertions, visible: paletteCollapse.result.realPi.visible },
    },
  }));
} finally {
  await rm(temporary, { recursive: true, force: true });
}
