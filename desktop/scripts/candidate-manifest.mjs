import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import {
  assertCandidateManifest, assertRepositoryReady, buildCandidateManifest, serializeCandidateManifest, verifyCandidateArtifacts,
} from './candidate-manifest-lib.mjs';

const usage = `usage:
  npm run candidate:generate -- --installer PATH --resources PATH --arch x64 --build-timestamp ISO --predecessor-ref REF --predecessor-sha256 HEX --operator-gate blocked|verified --gate-evidence REF [--gate-verified-at ISO] --output PATH
  npm run candidate:check -- --manifest PATH --installer PATH --resources PATH`;
const [command, ...argv] = process.argv.slice(2);
if (command !== 'generate' && command !== 'check') throw new Error(usage);
function options(args) {
  const result = {};
  for (let index = 0; index < args.length; index += 2) {
    const key = args[index]; const value = args[index + 1];
    if (!key?.startsWith('--') || value === undefined || value.startsWith('--') || key in result) throw new Error(usage);
    result[key.slice(2)] = value;
  }
  return result;
}
function requireKeys(value, required, optional = []) {
  const allowed = new Set([...required, ...optional]);
  for (const key of Object.keys(value)) if (!allowed.has(key)) throw new Error(`unknown option --${key}`);
  for (const key of required) if (!(key in value)) throw new Error(`missing option --${key}`);
}
function git(repo, args) { return execFileSync('git', ['-C', repo, ...args], { encoding: 'utf8' }).trim(); }
function repositoryFacts(repo) {
  const remoteLine = git(repo, ['ls-remote', '--exit-code', 'origin', 'refs/heads/main']);
  const remoteHead = remoteLine.split(/\s+/)[0] ?? '';
  return {
    branch: git(repo, ['branch', '--show-current']), upstream: git(repo, ['rev-parse', '--abbrev-ref', '@{upstream}']), originUrl: git(repo, ['remote', 'get-url', 'origin']),
    status: git(repo, ['status', '--porcelain=v1', '--untracked-files=all']), head: git(repo, ['rev-parse', 'HEAD']),
    upstreamHead: git(repo, ['rev-parse', '@{upstream}']), remoteHead,
  };
}
const args = options(argv);
const repo = git(process.cwd(), ['rev-parse', '--show-toplevel']);
const repoFacts = repositoryFacts(repo);
const sourceCommit = assertRepositoryReady(repoFacts);
const packageJson = JSON.parse(readFileSync(path.join(repo, 'desktop/package.json'), 'utf8'));
if (packageJson.build?.productName !== 'Void Code' || packageJson.build?.appId !== 'works.voidcode.desktop' || packageJson.build?.nsis?.artifactName !== 'Void-Code-windows-${arch}.${ext}') throw new Error('authoritative package identity mismatch');

if (command === 'generate') {
  requireKeys(args, ['installer', 'resources', 'arch', 'build-timestamp', 'predecessor-ref', 'predecessor-sha256', 'operator-gate', 'gate-evidence', 'output'], ['gate-verified-at']);
  const output = path.resolve(args.output);
  if (existsSync(output)) throw new Error('candidate manifest output already exists');
  const manifest = buildCandidateManifest({
    productName: packageJson.build.productName, sourceCommit, sourceOrigin: repoFacts.originUrl,
    installerPath: path.resolve(args.installer), resourceManifestPath: path.resolve(args.resources), arch: args.arch,
    buildTimestamp: args['build-timestamp'], predecessorReference: args['predecessor-ref'], predecessorSha256: args['predecessor-sha256'],
    operatorGate: args['operator-gate'], gateEvidence: args['gate-evidence'], gateVerifiedAt: args['gate-verified-at'] ?? null,
  });
  writeFileSync(output, serializeCandidateManifest(manifest), { encoding: 'utf8', mode: 0o600, flag: 'wx' });
  console.log(JSON.stringify({ action: 'generated', manifest: path.basename(output), sourceCommit, installerSha256: manifest.installer.sha256 }));
} else {
  requireKeys(args, ['manifest', 'installer', 'resources']);
  const manifest = assertCandidateManifest(JSON.parse(readFileSync(path.resolve(args.manifest), 'utf8')));
  // The version is not compared against desktop/package.json: that file carries
  // a placeholder, and the candidate's version comes from the build. What ties
  // the manifest to these artifacts is verifyCandidateArtifacts below.
  if (manifest.source.commit !== sourceCommit || manifest.product.name !== packageJson.build.productName) throw new Error('manifest does not match synchronized source identity');
  verifyCandidateArtifacts(manifest, path.resolve(args.installer), path.resolve(args.resources));
  console.log(JSON.stringify({ action: 'verified', sourceCommit, installerSha256: manifest.installer.sha256, operatorGate: manifest.operatorGate.status }));
}
