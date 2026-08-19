import type { StableUpdateStatus } from '../shared/contract';

type TextElement = { textContent: string | null };
type ActionElement = TextElement & { hidden: boolean };
type Translate = (message: string, ...args: Array<string | number | boolean>) => string;
const fallback: Translate = (message, ...args) => message.replace(/\{(\d+)\}/g, (_match, index: string) => String(args[Number(index)]));

export function unavailableUpdateStatus(currentVersion: string): StableUpdateStatus {
  return { state: 'unavailable', currentVersion, canRetry: true };
}

export function updateStatusText(update: StableUpdateStatus, t: Translate = fallback): { current: string; status: string; action: string } {
  const status = update.state === 'checking' ? t('Checking for updates…')
    : update.state === 'up-to-date' ? t('Up to date')
      : update.state === 'available' ? t('Version {0} is available', update.availableVersion)
        : update.state === 'downloading' ? t('Downloading {0}% ({1} of {2} bytes)', Math.round(update.percent), update.transferred, update.total)
          : update.state === 'verifying' ? t('Verifying update…')
            : update.state === 'installing' ? t('Installing update and restarting…')
              : update.state === 'failed' ? t('Update failed. You can retry.') : t('Update unavailable');
  const current = /-beta\.[1-9]\d*$/.test(update.currentVersion)
    ? t('Current version {0}. Closed beta: Ed25519 verifies update origin, but does not establish Windows publisher trust.', update.currentVersion)
    : t('Current version {0}', update.currentVersion);
  return { current, status, action: update.state === 'failed' ? t('Retry update') : t('Update now') };
}

export function renderUpdateStatus(elements: { current: TextElement; status: TextElement; action: ActionElement }, update: StableUpdateStatus, t: Translate = fallback): void {
  const text = updateStatusText(update, t);
  elements.current.textContent = text.current; elements.status.textContent = text.status; elements.action.textContent = text.action;
  elements.action.hidden = update.state !== 'available' && update.state !== 'failed';
}
