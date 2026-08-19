import { app } from 'electron';
import type { Session } from 'electron';
import { rm } from 'node:fs/promises';
import path from 'node:path';
import { autoUpdater } from 'electron-updater';
import type { ProgressInfo, UpdateInfo } from 'electron-updater';
import { CancellationToken } from 'builder-util-runtime';
import { fileSha256, fileSize, readAppUpdateConfiguration, removeUpdateFile, UPDATE_FEED_URL, UPDATE_MANIFEST_MAX_BYTES, UpdaterRequestAuthorizer, type UpdaterAdapter } from './stable-update';

export function installUpdaterRequestBoundary(updaterSession: Session, authorizer: UpdaterRequestAuthorizer): void {
  updaterSession.webRequest.onBeforeRequest({ urls: ['https://*/*', 'http://*/*'] }, (details, callback) => {
    if ((details.webContentsId ?? -1) >= 0) return callback({ cancel: true });
    callback({ cancel: !authorizer.allowRequest(details.url) });
  });
  updaterSession.webRequest.onBeforeRedirect({ urls: ['https://*/*', 'http://*/*'] }, (details) => {
    if ((details.webContentsId ?? -1) < 0) authorizer.observeRedirect(details.url, details.redirectURL);
  });
  updaterSession.webRequest.onHeadersReceived({ urls: ['https://*/*', 'http://*/*'] }, (details, callback) => {
    if ((details.webContentsId ?? -1) >= 0) return callback({ cancel: true });
    const authorization = authorizer.consumeResponseAuthorization(details.url);
    if (!authorization) return callback({ cancel: true });
    const headers = Object.entries(details.responseHeaders ?? {});
    const headerValue = (name: string): string | undefined => {
      const matches = headers.filter(([header]) => header.toLowerCase() === name);
      return matches.length === 1 && matches[0][1].length === 1 ? matches[0][1][0] : undefined;
    };
    const encoded = headers.some(([name]) => ['transfer-encoding', 'content-encoding'].includes(name.toLowerCase()));
    const contentLength = headerValue('content-length');
    if (authorization.kind === 'metadata') {
      const length = contentLength !== undefined && /^[1-9]\d*$/.test(contentLength) ? Number(contentLength) : Number.NaN;
      return callback({ cancel: details.statusCode < 200 || details.statusCode >= 300 || encoded || !Number.isSafeInteger(length) || length > UPDATE_MANIFEST_MAX_BYTES });
    }
    if (details.statusCode >= 200 && details.statusCode < 300) {
      const length = contentLength !== undefined && /^(?:0|[1-9]\d*)$/.test(contentLength) ? Number(contentLength) : Number.NaN;
      return callback({ cancel: encoded || !Number.isSafeInteger(length) || length !== authorization.size });
    }
    if (![301, 302, 303, 307, 308].includes(details.statusCode)) return callback({ cancel: true });
    const location = headerValue('location');
    let validHttpsLocation = false;
    try { validHttpsLocation = location !== undefined && new URL(location).protocol === 'https:'; } catch { /* invalid redirect URL */ }
    callback({ cancel: encoded || contentLength !== '0' || !validHttpsLocation });
  });
}

export function createElectronUpdaterAdapter(cleanupOwnedSessions: () => Promise<void>): UpdaterAdapter {
  const authorizer = new UpdaterRequestAuthorizer();
  // electron-updater owns this cache-disabled `electron-updater` partition.
  installUpdaterRequestBoundary(autoUpdater.netSession, authorizer);
  return {
    configure(settings) {
      autoUpdater.autoDownload = settings.autoDownload; autoUpdater.autoInstallOnAppQuit = settings.autoInstallOnAppQuit;
      autoUpdater.allowPrerelease = settings.allowPrerelease; autoUpdater.allowDowngrade = settings.allowDowngrade;
      autoUpdater.disableWebInstaller = settings.disableWebInstaller; autoUpdater.disableDifferentialDownload = settings.disableDifferentialDownload;
      autoUpdater.setFeedURL({ provider: 'generic', url: UPDATE_FEED_URL });
    },
    authorize(manifest) { authorizer.authorize(manifest); },
    packageConfiguration: () => readAppUpdateConfiguration(process.resourcesPath),
    async checkForUpdates() {
      const result = await autoUpdater.checkForUpdates(); if (!result) throw new Error('updater returned no metadata');
      const info = result.updateInfo as UpdateInfo;
      return { version: info.version, files: info.files.map((file) => {
        if (typeof file.size !== 'number') throw new Error('updater metadata omitted artifact size');
        return { url: new URL(String(file.url), UPDATE_FEED_URL).toString(), sha512: file.sha512, size: file.size };
      }) };
    },
    onProgress(listener) { autoUpdater.on('download-progress', (progress) => listener({ percent: progress.percent, transferred: progress.transferred, total: progress.total })); },
    async downloadUpdate(maxBytes) {
      if (!Number.isSafeInteger(maxBytes) || maxBytes < 1) throw new Error('invalid download bound');
      const cancellation = new CancellationToken();
      const enforceBound = (progress: ProgressInfo) => { if (progress.transferred > maxBytes || progress.total > maxBytes) cancellation.cancel(); };
      autoUpdater.on('download-progress', enforceBound);
      try { return await autoUpdater.downloadUpdate(cancellation); } finally { autoUpdater.removeListener('download-progress', enforceBound); }
    },
    sha256: fileSha256, size: fileSize, remove: removeUpdateFile,
    cleanupPartials: () => rm(path.join(process.env.LOCALAPPDATA ?? app.getPath('appData'), 'void-code-desktop-updater', 'pending'), { recursive: true, force: true }),
    cleanupOwnedSessions, quitAndInstall: (silent, forceRun) => autoUpdater.quitAndInstall(silent, forceRun),
  };
}
