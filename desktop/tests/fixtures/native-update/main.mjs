import { app } from 'electron';
import { randomUUID } from 'node:crypto';
import { createWriteStream, existsSync, readFileSync, renameSync, watch } from 'node:fs';
import { realpath } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import process from 'node:process';

const option = (name) => process.argv.find((value) => value.startsWith(`${name}=`))?.slice(name.length + 1);

function atomicJson(file, value) {
  const temporary = `${file}.${process.pid}.tmp`;
  return new Promise((resolveWrite, rejectWrite) => {
    const stream = createWriteStream(temporary, { encoding: 'utf8', mode: 0o600, flags: 'wx' });
    stream.once('error', rejectWrite);
    stream.once('finish', () => { try { renameSync(temporary, file); resolveWrite(); } catch (error) { rejectWrite(error); } });
    stream.end(JSON.stringify(value));
  });
}

function waitForExit(file) {
  return new Promise((resolveExit, rejectExit) => {
    let settled = false;
    const finish = (error) => {
      if (settled) return;
      settled = true;
      watcher?.close();
      if (error) rejectExit(error);
      else resolveExit();
    };
    let watcher = null;
    try {
      watcher = watch(dirname(file), () => { if (existsSync(file)) finish(); });
      // Subscribe before checking so creation between the first check and watch cannot be lost.
      if (existsSync(file)) finish();
    } catch (error) { finish(error); }
  });
}

async function bootstrap() {
  const configPath = option('--fixture-bootstrap');
  if (!configPath) throw new Error('missing fixture bootstrap path');
  const config = JSON.parse(readFileSync(configPath, 'utf8'));
  for (const key of ['transactionId', 'receiptDir', 'exitFile', 'userData', 'bootAttemptFile']) if (typeof config[key] !== 'string' || config[key] === '') throw new Error(`invalid bootstrap ${key}`);
  app.setPath('userData', config.userData);
  await app.whenReady();
  if (!app.isPackaged) throw new Error('fixture bootstrap must run from a packaged app');
  const resourcesPath = await realpath(process.resourcesPath);
  const execPath = await realpath(process.execPath);
  const fixtureMode = JSON.parse(readFileSync(join(resourcesPath, 'fixture-mode.json'), 'utf8')).mode;
  if (!['normal', 'missing', 'wrong-token', 'fault'].includes(fixtureMode)) throw new Error('invalid baked fixture mode');
  const bootAttempt = { v: 1, transactionId: config.transactionId, pid: process.pid, execPath, resourcesPath, packaged: true, mode: fixtureMode };
  await atomicJson(`${config.bootAttemptFile}.attempt-${process.pid}-${randomUUID()}.json`, bootAttempt);
  await atomicJson(config.bootAttemptFile, bootAttempt);
  if (fixtureMode === 'missing') return;
  const marker = readFileSync(join(resourcesPath, 'full-resource-marker.txt'), 'utf8');
  const identity = JSON.parse(readFileSync(join(resourcesPath, 'identity.json'), 'utf8'));
  await atomicJson(join(config.receiptDir, `${fixtureMode === 'wrong-token' ? `${config.transactionId}-wrong` : config.transactionId}.json`), {
    v: 1, transactionId: fixtureMode === 'wrong-token' ? `${config.transactionId}-wrong` : config.transactionId,
    identity: identity.appId, version: app.getVersion(), arch: process.arch, execPath, resourcesPath, marker,
    bootstrap: 'ok', pid: process.pid, packaged: true,
  });
  await waitForExit(config.exitFile);
}

bootstrap().then(() => app.quit()).catch((error) => {
  process.stderr.write(`fixture bootstrap failed: ${error instanceof Error ? error.stack ?? error.message : String(error)}\n`);
  app.exit(70);
});
