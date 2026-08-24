const { chmodSync, existsSync, readdirSync } = require('node:fs');
const path = require('node:path');

exports.default = async function afterPack(context) {
  if (context.electronPlatformName !== 'darwin') return;
  // node-pty's spawn-helper arrives without its executable bit, and every
  // prebuild in the package is unpacked whichever architecture is being built --
  // so the bit is restored on each one that is there, rather than on the name of
  // the architecture this file happened to be written for.
  const prebuilds = path.join(context.appOutDir, `${context.packager.appInfo.productFilename}.app`, 'Contents/Resources/app.asar.unpacked/node_modules/node-pty/prebuilds');
  const helpers = readdirSync(prebuilds)
    .map((prebuild) => path.join(prebuilds, prebuild, 'spawn-helper'))
    .filter((helper) => existsSync(helper));
  if (helpers.length === 0) throw new Error(`no node-pty spawn-helper to make executable under ${prebuilds}`);
  for (const helper of helpers) chmodSync(helper, 0o755);
};
