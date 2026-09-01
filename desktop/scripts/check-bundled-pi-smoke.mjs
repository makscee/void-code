// Behavioural smoke for the bundled Pi runtime.
//
// It checks BEHAVIOUR, not text. The contract check beside it (check-pi-bun-contract.mjs) proves the
// shape of an expression in Pi's source, and it will survive exactly until the first refactor that
// keeps the meaning and moves the line. This one asks a different question: on a bundled tree, does
// the thing the desktop app starts Pi for still work?
//
// Two silent failures of bundle mode, both named here by their consequence:
//
//  1. Outside bun mode Pi's extension loader builds jiti's aliases through require.resolve("typebox")
//     and friends -- eagerly, before an extension asks for anything. With no node_modules on disk
//     that kills EVERY extension, including the transport extension vc installs, and in the app it
//     shows up as "VC cannot see a provider". So the assertion is not that the extension loaded but
//     what follows from it: provider void-codex is registered and its models are listed.
//  2. In bundle mode getPackageDir() is computed from process.execPath. If PI_PACKAGE_DIR does not
//     arrive, Pi does NOT fail -- it reads somebody else's package.json or none at all, and becomes
//     version 0.0.0 with somebody else's app name and settings directory.
//
// The extension is the real one, from cmd/vc/pi_extension.go, not a toy: it is the one that breaks.
//
// This lives on the pinned-Pi provision path because Pi is vendored and pinned by hash: a silent
// failure is impossible in production and possible exactly at the moment somebody bumps the pin.
import { execFileSync } from 'node:child_process';
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { bundlePiRuntime, hoistPiBundledDependencies } from './resource-assembly-lib.mjs';

const desktop = path.resolve(import.meta.dirname, '..');
const repo = path.resolve(desktop, '..');

function die(what, detail) {
  console.error(`\nBUNDLED PI SMOKE: RED\n\n  Stopped working: ${what}\n\n${detail}\n`);
  process.exit(1);
}

// The extension's fake bootstrap is a shell script, and that limits this check to POSIX on purpose:
// the extension runs it through execFileSync without a shell, and Node has refused to spawn .cmd and
// .bat that way since 18.20. This runs in the macOS provision job, so refusing out loud beats
// checking something weaker on Windows and calling it the same check.
if (process.platform === 'win32') {
  die('the smoke itself', '  It needs a POSIX runner (macOS/Linux): the extension\'s fake bootstrap cannot be spawned\n  through execFileSync on Windows. It runs from desktop-tests.yml and desktop-mac-app.yml.');
}

const piSource = path.join(desktop, 'runtime/pi');
if (!existsSync(path.join(piSource, 'node_modules/@earendil-works/pi-coding-agent/package.json'))) {
  die('the smoke\'s own setup, not the bundle', `  Pi's tree is not installed: ${piSource}\n  Install it: npm ci --prefix runtime/pi --ignore-scripts --no-audit --no-fund`);
}

const pins = JSON.parse(await readFile(path.join(desktop, 'scripts/resource-pins.json'), 'utf8'));
const work = await mkdtemp(path.join(os.tmpdir(), 'bundled-pi-smoke-'));
try {
  // The copy is not optional: bundlePiRuntime deletes node_modules, and doing that in the working
  // tree would mean repairing it by hand after every run.
  const piRoot = path.join(work, 'pi');
  await cp(piSource, piRoot, { recursive: true, verbatimSymlinks: true });
  await hoistPiBundledDependencies(piRoot);
  const bundle = await bundlePiRuntime(piRoot, `${process.platform}-${process.arch}`);

  // The premise everything below rests on. If node_modules survived, green means nothing: the
  // extension could have loaded the old way, through aliases resolved from disk.
  if (existsSync(path.join(piRoot, 'node_modules'))) {
    die('the smoke\'s premise', '  node_modules survived the bundling, so the checks below prove nothing:\n  the loader\'s aliases can still be resolved from disk.');
  }

  const entry = path.join(piRoot, bundle.entry);
  const packageDir = path.join(piRoot, bundle.packageDir);
  const home = path.join(work, 'home');
  await mkdir(home, { recursive: true });

  // Exactly the extension text vc writes for the user. A Go raw string cannot contain a backtick, so
  // the bounds are unambiguous.
  const extensionGo = await readFile(path.join(repo, 'cmd/vc/pi_extension.go'), 'utf8');
  const marker = 'const piVoidCodexExtensionSource = `';
  const opens = extensionGo.indexOf(marker);
  const closes = opens < 0 ? -1 : extensionGo.indexOf('`', opens + marker.length);
  if (closes < 0) die('the smoke\'s own setup, not the bundle', '  cmd/vc/pi_extension.go has no raw string piVoidCodexExtensionSource.\n  If it was renamed, fix it here rather than switching the check off.');
  const extensionSource = extensionGo.slice(opens + marker.length, closes);
  if (!extensionSource.startsWith('// void-code-managed-pi-extension:v1')) {
    die('the smoke\'s own setup, not the bundle', '  The extracted extension does not start with its own version marker -- the Go parse has drifted.');
  }
  const extension = path.join(work, 'extension.ts');
  await writeFile(extension, extensionSource);

  // The extension asks vc which providers are granted, through
  // execFileSync(VC_BOOTSTRAP_EXECUTABLE, ['pi-bootstrap']). Hand it an answer: what this needs is a
  // registered provider, not a live relay.
  const bootstrap = path.join(work, 'bootstrap.sh');
  const models = ['gpt-5.6-terra', 'gpt-5.6-sol', 'gpt-5.6-luna'];
  await writeFile(bootstrap, `#!/bin/sh\n[ "$1" = "pi-bootstrap" ] || exit 1\nprintf '%s' '${JSON.stringify({ version: 1, relayUrl: 'https://relay.invalid', authToken: 'smoke', providers: [{ kind: 'codex', relayProviderId: 'smoke-provider', models }] })}'\n`, { mode: 0o700 });

  const run = (args, env) => {
    try {
      return execFileSync(process.execPath, [entry, ...args], {
        cwd: work,
        encoding: 'utf8',
        stdio: ['ignore', 'pipe', 'pipe'],
        timeout: 120000,
        env: { PATH: '/usr/bin:/bin', HOME: home, TERM: 'dumb', PI_PACKAGE_DIR: packageDir, ...env },
      });
    } catch (error) {
      return { failed: `${error.stdout ?? ''}${error.stderr ?? ''}${error.message ?? ''}` };
    }
  };

  // 1. The provider the app connects a model through.
  const listed = run(['-e', extension, '--offline', '--list-models'], { VC_BOOTSTRAP_EXECUTABLE: bootstrap });
  if (listed.failed !== undefined) {
    die('running the bundle with the real extension', `  The entry point did not survive --list-models. Output:\n${listed.failed.split('\n').slice(0, 12).map((line) => `    ${line}`).join('\n')}`);
  }
  // No regular expressions: a row of the model table starts with the provider name and then the
  // model id. Look for that pair rather than a substring anywhere in the output -- otherwise an
  // error message that happens to name the provider would pass.
  const rows = listed.split('\n').map((line) => line.trim().split(/\s+/));
  const missing = models.filter((model) => !rows.some(([provider, id]) => provider === 'void-codex' && id === model));
  if (missing.length > 0) {
    die('the real extension registering its provider', `  void-codex was not registered: models ${missing.join(', ')} are not listed.\n  In the app this looks like "VC cannot see a provider" and like nothing else.\n\n  The usual cause: Pi stopped entering bundle mode by file name (isBunBinary), so the loader\n  went back to aliases built with require.resolve -- and node_modules is gone.\n  Look at config.js, the line about "$bunfs" / "~BUN" / "%7EBUN".`);
  }

  // 2. The other silent failure of the same mode: the package directory is not found.
  const version = run(['--version'], {});
  if (version.failed !== undefined) die('running the bundle', `  --version did not survive:\n${version.failed.split('\n').slice(0, 8).map((line) => `    ${line}`).join('\n')}`);
  const printed = version.trim();
  if (printed === '0.0.0') {
    die('Pi finding its own package directory', '  The entry point reports version 0.0.0 -- that is config.js\'s placeholder for a package.json\n  it could not read, not a version. The package name and the settings directory go with it.\n  Either PI_PACKAGE_DIR did not reach Pi, or the package directory moved inside the bundle.');
  }
  if (printed !== pins.pi.version) {
    die('the bundle matching the pin', `  The entry point reports ${printed}; resource-pins.json pins ${pins.pi.version}.`);
  }

  console.log(`bundled pi smoke: void-codex registered by the real extension (${models.join(', ')}), version ${printed}, node_modules gone`);
} finally {
  await rm(work, { recursive: true, force: true });
}
