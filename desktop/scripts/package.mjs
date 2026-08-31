import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { electronBuilderVersionArgs, readBuildVersion } from './build-version.mjs';

// electron-builder, with the version of this build handed to it on the command
// line.
//
// The version cannot come from desktop/package.json -- that file has said 0.1.0
// since July and a checkout has no idea which tag it will be released under --
// and it must not be written INTO desktop/package.json either, because then a
// local package leaves a modified tree behind and the next commit carries a
// version nobody chose. `-c.extraMetadata.version` exists exactly so neither is
// necessary.
//
// This is a Node wrapper rather than a flag appended to the npm script because
// npm runs scripts through %COMSPEC% on Windows: `$(node -p ...)` is not
// expanded there, and electron-builder would be handed the literal characters.
// Windows is the platform whose installer matters most here.

const desktop = path.resolve(import.meta.dirname, '..');
const cli = path.join(desktop, 'node_modules/electron-builder/cli.js');

const versionArgs = electronBuilderVersionArgs(readBuildVersion());
if (!versionArgs.every((argument) => argument.startsWith('-c.extraMetadata.version='))) throw new Error('the packer was not handed -c.extraMetadata.version');

execFileSync(process.execPath, [cli, ...process.argv.slice(2), ...versionArgs], { cwd: desktop, stdio: 'inherit' });
