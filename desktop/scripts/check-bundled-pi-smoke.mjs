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
import { execFileSync, spawn } from 'node:child_process';
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { assemblyTarget, hoistPiBundledDependencies } from './resource-assembly-lib.mjs';
import { bundleForSmoke, inSmokeWorkspace, piSmokeBootstrapPlan, piSmokeRunEnv, piSmokeTarget } from './bundled-pi-smoke-lib.mjs';

const desktop = path.resolve(import.meta.dirname, '..');
const repo = path.resolve(desktop, '..');

// Throws rather than exiting, and the difference is not style. process.exit does not run `finally`
// -- proven, not assumed: `node -e 'try { process.exit(1) } finally { console.log("ran") }'` prints
// nothing -- so every one of the failures below used to leave its workspace on disk: an unpacked Pi
// tree, a bundle, a built stub. It leaked precisely when the check found what it is written to find,
// and the person who meets that path is somebody mid-bump of the Pi pin, iterating on a red run, on
// a machine that is not an ephemeral runner. One reporter at the bottom, one `finally` for cleanup,
// and a thirteenth failure added later inherits both instead of having to remember an `rm`.
class SmokeFailure extends Error {
  constructor(what, detail) {
    super(`\nBUNDLED PI SMOKE: RED\n\n  Stopped working: ${what}\n\n${detail}\n`);
    this.name = 'SmokeFailure';
  }
}

function die(what, detail) {
  throw new SmokeFailure(what, detail);
}

async function main() {
  // Which platform is being checked comes from the caller, never from the host: bundling for
  // `${process.platform}-${process.arch}` is what let a macOS runner report success for a darwin
  // bundle production does not ship. The host still decides one thing, and only this one -- whether
  // what was bundled can be started here. assemblyTarget refuses a cross-operating-system pair with
  // that reason, and refusing is right: this check runs what it bundles, and a check that stops
  // running things is the weakening the plan forbids.
  let target;
  try {
    target = piSmokeTarget({ argv: process.argv.slice(2), env: process.env });
    assemblyTarget(target, `${process.platform}-${process.arch}`);
  } catch (error) {
    die('the smoke\'s own setup, not the bundle', `  ${error.message.split('\n').join('\n  ')}`);
  }

  const piSource = path.join(desktop, 'runtime/pi');
  if (!existsSync(path.join(piSource, 'node_modules/@earendil-works/pi-coding-agent/package.json'))) {
    die('the smoke\'s own setup, not the bundle', `  Pi's tree is not installed: ${piSource}\n  Install it: npm ci --prefix runtime/pi --ignore-scripts --no-audit --no-fund`);
  }

  const pins = JSON.parse(await readFile(path.join(desktop, 'scripts/resource-pins.json'), 'utf8'));
  // The workspace and its removal are one decision, held in one place, so that a thirteenth failure
  // added later inherits the cleanup instead of having to remember it.
  return inSmokeWorkspace({
    create: () => mkdtemp(path.join(os.tmpdir(), 'bundled-pi-smoke-')),
    remove: (workspace) => rm(workspace, { recursive: true, force: true }),
  }, async (work) => {
    // The copy is not optional: bundlePiRuntime deletes node_modules, and doing that in the working
    // tree would mean repairing it by hand after every run.
    const piRoot = path.join(work, 'pi');
    await cp(piSource, piRoot, { recursive: true, verbatimSymlinks: true });
    await hoistPiBundledDependencies(piRoot);
    // Built through bundleForSmoke rather than by calling the bundler directly: it is what compares the
    // platform the bundler was handed, and the native module the finished bundle carries, against the
    // target that was asked for. Bundling for the host instead was a mutation the entire suite missed.
    const bundle = await bundleForSmoke({ piRoot, target });

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
    // registered provider, not a live relay. One stub for every platform -- see piSmokeBootstrapPlan
    // for why the shell script it replaced could not be one.
    const models = ['gpt-5.6-terra', 'gpt-5.6-sol', 'gpt-5.6-luna'];
    // The relay is a real local process now, not an unreachable name. Registering a provider turned
    // out to prove nothing about holding a conversation: the extension resolves
    // `@earendil-works/pi-ai/compat` on disk and loads a file beside it, and it does that while
    // building the request -- so a bundle lists its models and dies the moment somebody types.
    // Checking the answer instead of the registration is the only way that door gets watched.
    const reply = 'VOID-SMOKE-PONG-6f21';
    const relay = spawn(process.execPath, [path.join(desktop, 'scripts/pi-smoke-relay.mjs')], {
      env: { ...process.env, VC_SMOKE_RELAY_REPLY: reply },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    // Killed however this ends, and in one place rather than after each step. A run that fails on
    // an earlier check used to leave the relay listening -- the same shape as the workspace the
    // smoke used to leave on disk, and worth fixing once rather than remembering at every exit.
    try {
      const relayUrl = await new Promise((resolve, reject) => {
        const failed = setTimeout(() => reject(new Error('the canned relay did not report a port within 30 seconds')), 30000);
        relay.stdout.on('data', (chunk) => {
          const port = /PORT (\d+)/.exec(String(chunk));
          if (port) { clearTimeout(failed); resolve(`http://127.0.0.1:${port[1]}`); }
        });
        relay.once('error', (error) => { clearTimeout(failed); reject(error); });
        relay.once('exit', (code) => { clearTimeout(failed); reject(new Error(`the canned relay exited before answering (code ${code})`)); });
      }).catch((error) => die('the smoke\'s own setup, not the bundle', `  ${error.message}`));
      const bootstrapAnswer = JSON.stringify({ version: 1, relayUrl, authToken: 'smoke', providers: [{ kind: 'codex', relayProviderId: 'smoke-provider', models }] });
      const stub = piSmokeBootstrapPlan({ target, directory: work });
      // Built for the machine this runs on, not for the target: the stub is the test's own fixture and
      // has to start here. The bundle is the thing built for the target.
      const host = assemblyTarget(`${process.platform}-${process.arch}`, `${process.platform}-${process.arch}`);
      try {
        execFileSync('go', ['build', '-o', stub.output, stub.source], {
          cwd: repo,
          stdio: ['ignore', 'pipe', 'pipe'],
          env: { ...process.env, CGO_ENABLED: '0', GOOS: host.goos, GOARCH: host.goarch },
        });
      } catch (error) {
        die('building the bootstrap stub, not the bundle', `  go build failed for ${stub.source}:\n${`${error.stdout ?? ''}${error.stderr ?? ''}${error.message ?? ''}`.split('\n').slice(0, 8).map((line) => `    ${line}`).join('\n')}`);
      }

      const run = (args, env) => {
        try {
          return execFileSync(process.execPath, [entry, ...args], {
            cwd: work,
            encoding: 'utf8',
            stdio: ['ignore', 'pipe', 'pipe'],
            timeout: 120000,
            env: { ...piSmokeRunEnv({ target, home, packageDir }), ...env },
          });
        } catch (error) {
          return { failed: `${error.stdout ?? ''}${error.stderr ?? ''}${error.message ?? ''}` };
        }
      };

      // 1. The provider the app connects a model through.
      const listed = run(['-e', extension, '--offline', '--list-models'], { VC_BOOTSTRAP_EXECUTABLE: stub.output, VC_SMOKE_BOOTSTRAP_JSON: bootstrapAnswer });
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

      // 3. The turn itself, which is the whole point and was the thing nobody checked. Registration and
      // the version are still asserted above, because they name a different failure: "no provider at
      // all" and "a provider that cannot answer" send a reader to different places.
      const spoken = run(['-e', extension, '--provider', 'void-codex', '--model', models[0], '-p', 'PING'], {
        VC_BOOTSTRAP_EXECUTABLE: stub.output,
        VC_SMOKE_BOOTSTRAP_JSON: bootstrapAnswer,
        // The same thing vc sets for the desktop, and for the same reason: a check that reaches the
        // internet to ask about versions is a check that fails when the internet does.
        PI_SKIP_VERSION_CHECK: '1',
      });
        if (spoken.failed !== undefined) {
        die('holding a conversation on the bundled runtime', `  The bundle registers its provider and lists its models, and then cannot answer. Output:\n${spoken.failed.split('\n').slice(0, 12).map((line) => `    ${line}`).join('\n')}\n\n  The usual cause: something in the extension resolves a module path on disk -- the transport\n  extension calls import.meta.resolve('@earendil-works/pi-ai/compat') and loads a file next to\n  it -- and a bundle has no disk to resolve against. Registration does not touch that path;\n  building the request does.`);
      }
      if (!spoken.includes(reply)) {
        die('holding a conversation on the bundled runtime', `  The turn ended without the answer the canned relay sent. Expected ${reply} in:\n${spoken.split('\n').slice(0, 12).map((line) => `    ${line}`).join('\n')}`);
      }

      console.log(`bundled pi smoke: ${target} bundle, void-codex registered by the real extension (${models.join(', ')}), a turn answered ${reply}, version ${printed}, node_modules gone`);
    } finally {
      relay.kill();
    }
  });
}

// The one place a failure is reported and the process is ended. Everything above signals by throwing,
// so cleanup runs on the way out no matter which check said no.
try {
  await main();
} catch (error) {
  if (error instanceof SmokeFailure) {
    console.error(error.message);
    process.exit(1);
  }
  throw error;
}
