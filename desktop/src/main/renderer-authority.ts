import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

interface FrameLike { url: string }
interface WebContentsLike { mainFrame: FrameLike }
export interface IpcEventLike { sender: WebContentsLike; senderFrame: FrameLike | null }

export function rendererUrl(file: string, query?: Record<string, string>): string {
  const url = pathToFileURL(path.resolve(file));
  for (const [key, value] of Object.entries(query ?? {})) url.searchParams.set(key, value);
  return url.href;
}

export function rendererAuthority(active: WebContentsLike, expectedUrl: string): (event: IpcEventLike) => void {
  const expected = new URL(expectedUrl);
  if (expected.protocol !== 'file:' || expected.host || expected.hash) throw new Error('invalid renderer authority configuration');
  const expectedFile = path.resolve(fileURLToPath(expected));
  return (event) => {
    if (event.sender !== active || event.senderFrame !== event.sender.mainFrame) throw new Error('renderer authority rejected');
    let actual: URL;
    try { actual = new URL(event.senderFrame.url); } catch { throw new Error('renderer authority rejected'); }
    if (actual.protocol !== 'file:' || actual.host || actual.hash || actual.search !== expected.search || path.resolve(fileURLToPath(actual)) !== expectedFile) throw new Error('renderer authority rejected');
  };
}

interface NavigationEvent { preventDefault(): void }
interface NavigationWebContents {
  on(name: 'will-navigate' | 'will-frame-navigate' | 'will-attach-webview', listener: (event: NavigationEvent) => void): unknown;
  setWindowOpenHandler(handler: () => { action: 'deny' }): void;
}
export function installNavigationPolicy(contents: NavigationWebContents): void {
  contents.on('will-navigate', (event) => event.preventDefault());
  contents.on('will-frame-navigate', (event) => event.preventDefault());
  contents.on('will-attach-webview', (event) => event.preventDefault());
  contents.setWindowOpenHandler(() => ({ action: 'deny' }));
}
