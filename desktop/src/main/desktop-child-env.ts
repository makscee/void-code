import path from 'node:path';
import type { StatusWriteAuthority } from './status-channel';

export type DesktopPlatform = 'darwin' | 'win32';

function value(parent: NodeJS.ProcessEnv, name: string, platform: DesktopPlatform): string | undefined {
  if (platform !== 'win32') return parent[name]?.trim() ? parent[name] : undefined;
  const key = Object.keys(parent).find((candidate) => candidate.toLowerCase() === name.toLowerCase());
  const found = key ? parent[key] : undefined;
  return found?.trim() ? found : undefined;
}

export function desktopChildEnv(platform: DesktopPlatform, parent: NodeJS.ProcessEnv, privateNode: string, authority?: StatusWriteAuthority): Record<string, string> {
  const env: Record<string, string> = { TERM: 'xterm-256color', COLORTERM: 'truecolor' };
  if (platform === 'darwin') {
    const home = value(parent, 'HOME', platform);
    const temporary = value(parent, 'TMPDIR', platform);
    if (!home || !temporary) throw new Error('desktop session environment is unavailable');
    env.HOME = home;
    env.TMPDIR = temporary;
    env.PATH = `${path.dirname(privateNode)}:/usr/bin:/bin`;
  } else {
    const profile = value(parent, 'USERPROFILE', platform);
    const systemRoot = value(parent, 'SystemRoot', platform);
    const temporary = value(parent, 'TEMP', platform);
    const shortTemporary = value(parent, 'TMP', platform);
    if (!profile || !systemRoot || !temporary || !shortTemporary) throw new Error('desktop session environment is unavailable');
    env.USERPROFILE = profile;
    env.SystemRoot = systemRoot;
    env.TEMP = temporary;
    env.TMP = shortTemporary;
    const homeDrive = value(parent, 'HOMEDRIVE', platform); const homePath = value(parent, 'HOMEPATH', platform);
    if (homeDrive && homePath) { env.HOMEDRIVE = homeDrive; env.HOMEPATH = homePath; }
    env.PATH = `${path.win32.dirname(privateNode)};${path.win32.join(systemRoot, 'System32')}`;
  }
  if (authority) {
    env.VC_DESKTOP_STATUS_PATH = authority.path;
    env.VC_DESKTOP_CHAT_ID = authority.chatId;
    env.VC_DESKTOP_STATUS_GENERATION = String(authority.generation);
  }
  return env;
}
