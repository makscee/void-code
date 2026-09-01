// Asserts the bundle contract against the Pi that is actually vendored here.
//
// The library beside this file is pure and answers "is the check built right"; this half answers
// "what does today's Pi say", and it is the only half that needs Pi on disk. runtime/pi/node_modules
// is gitignored, so this runs from provision-pinned-pi-smoke.sh, right after the install that
// creates it -- the first moment the file exists, on the one path a version bump goes through.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { assertPiBunContract } from './pi-bun-contract-lib.mjs';

const pi = path.resolve(import.meta.dirname, '../runtime/pi/node_modules/@earendil-works/pi-coding-agent');
const read = (relative) => {
  try {
    return readFileSync(path.join(pi, relative), 'utf8');
  } catch (error) {
    throw new Error(`vendored Pi is not installed, so its bundle contract cannot be checked: ${relative}\nInstall it: npm ci --prefix runtime/pi --ignore-scripts --no-audit --no-fund\n(${error.message})`);
  }
};

assertPiBunContract({ config: read('dist/config.js'), loader: read('dist/core/extensions/loader.js') });
console.log(`pi bundle contract: Pi ${JSON.parse(read('package.json')).version} still switches on its own module URL and serves extensions from the bundle`);
