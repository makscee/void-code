const { chmodSync } = require('node:fs');
const path = require('node:path');

exports.default = async function afterPack(context) {
  if (context.electronPlatformName !== 'darwin') return;
  const helper = path.join(context.appOutDir, `${context.packager.appInfo.productFilename}.app`, 'Contents/Resources/app.asar.unpacked/node_modules/node-pty/prebuilds/darwin-arm64/spawn-helper');
  chmodSync(helper, 0o755);
};
