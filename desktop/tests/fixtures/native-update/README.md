# Native update fixture recipe

From `desktop`, package and inspect the real initial fixture without running updater tests:

```sh
node --experimental-strip-types --input-type=module <<'NODE'
import { arch, platform } from 'node:process';
import { createCapsule, packageVariant, copyInitialMac, installInitialNsis, launchFixture } from './tests/fixtures/native-update/tooling.ts';

const capsule = await createCapsule('/absolute/private/capsule');
const target = platform === 'darwin' ? 'mac' : 'win';
const initial = await packageVariant(capsule, 'N', target);
console.log({ artifact: initial.artifact, target: capsule.target, hostArch: arch });
// Inspect initial.artifact, then install it only at capsule.target:
// await copyInitialMac(capsule, initial.artifact, capsule.target);
// await installInitialNsis(capsule, initial.artifact, capsule.target);
// Launch the installed executable with a unique transaction and a bootAttemptFile under capsule.receiptDir.
NODE
```

On macOS the builder target is explicit for the host architecture: `release/mac-arm64` on arm64 and `release/mac` on x64. The packaged app writes its boot-attempt witness before a baked `missing` mode exits.
