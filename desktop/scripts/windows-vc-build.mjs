import { execFileSync } from 'node:child_process';
import { shaFile } from './resource-assembly-lib.mjs';

export function gitHead(repo) {
  return execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repo, encoding: 'utf8' }).trim();
}

export async function buildWindowsVc({ repo, destination, sourceCommit, version, executableRunner = process.platform === 'win32' ? [] : null }) {
  if (!/^[0-9a-f]{40}$/.test(sourceCommit ?? '')) throw new Error('VOID_DESKTOP_VC_SOURCE_COMMIT must be a full commit SHA');
  if (!version) throw new Error('VOID_DESKTOP_VC_VERSION must identify this workflow ref');
  const checkoutCommit = gitHead(repo);
  if (checkoutCommit !== sourceCommit) throw new Error(`Windows vc source mismatch: checkout is ${checkoutCommit}, workflow bound ${sourceCommit}`);

  execFileSync('go', [
    'build', '-trimpath', '-buildvcs=false',
    '-ldflags', `-X github.com/makscee/void-code/internal/version.Version=${version}`,
    '-o', destination, './cmd/vc',
  ], {
    cwd: repo,
    env: { ...process.env, CGO_ENABLED: '0', GOOS: 'windows', GOARCH: 'amd64' },
    stdio: 'inherit',
  });

  if (executableRunner === null) throw new Error('built Windows vc requires an executable version oracle');
  const [runner, ...runnerArgs] = executableRunner;
  const command = runner || destination;
  const args = runner ? [...runnerArgs, destination, '--version'] : ['--version'];
  const builtVersion = execFileSync(command, args, { encoding: 'utf8' }).trim();
  const expectedOutput = `vc ${version}`;
  if (builtVersion !== expectedOutput) throw new Error(`built Windows vc version is ${builtVersion}, expected ${expectedOutput}`);
  return {
    assetName: 'vc-windows-amd64.exe',
    version: builtVersion,
    sourceCommit: checkoutCommit,
    path: 'vc/vc.exe',
    sha256: await shaFile(destination),
  };
}
