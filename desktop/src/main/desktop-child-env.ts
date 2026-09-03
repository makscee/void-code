import path from 'node:path';
import type { StatusWriteAuthority } from './status-channel';
import { resolveAccessCheckHost } from './access-check-host';

export type DesktopPlatform = 'darwin' | 'win32';

function value(parent: NodeJS.ProcessEnv, name: string, platform: DesktopPlatform): string | undefined {
  if (platform !== 'win32') return parent[name]?.trim() ? parent[name] : undefined;
  const key = Object.keys(parent).find((candidate) => candidate.toLowerCase() === name.toLowerCase());
  const found = key ? parent[key] : undefined;
  return found?.trim() ? found : undefined;
}

export function desktopChildEnv(platform: DesktopPlatform, parent: NodeJS.ProcessEnv, privateNode: string, authority?: StatusWriteAuthority, piPackageDir?: string): Record<string, string> {
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
  // `vc desktop-session` runs its own access check before Pi ever starts, and this environment is
  // built from nothing, so without this the chat would fall back to the CLI default and refuse to
  // open — the app honest about being signed in and still unusable.
  //
  // The empty environment is the point: the parent gets no say. FetchMe sends
  // `Authorization: Bearer <token>` to whatever host it is given, so letting a parent variable name
  // that host would build a token-exfiltration channel — point the check at an attacker's host,
  // collect the bearer, answer 200 with any identity, and a session opens that should not have.
  // That is what this allowlist exists against, and why VC_RELAY_HOST is not in it either.
  // The price, stated so nobody rediscovers it as a bug: an operator can point the status probe at
  // a stand and not the chat. The fix for that is an application setting, not an environment
  // variable, and it is separate work.
  env.VC_ACCESS_CHECK_HOST = resolveAccessCheckHost({});
  // The Windows runtime is bundled into a single file, and in that mode Pi resolves its own
  // package directory from process.execPath -- the directory of node.exe. PI_PACKAGE_DIR is Pi's
  // own documented override (`pi --help` lists it) and config.js checks it FIRST. The value comes
  // from the manifest, never from the parent environment: the empty environment above is a defence,
  // not an oversight, and the parent must not get to point Pi at a package directory of its
  // choosing. Without the variable Pi does not fail -- it silently reports version 0.0.0.
  if (piPackageDir) env.PI_PACKAGE_DIR = piPackageDir;
  if (authority) {
    env.VC_DESKTOP_STATUS_PATH = authority.path;
    env.VC_DESKTOP_CHAT_ID = authority.chatId;
    env.VC_DESKTOP_STATUS_GENERATION = String(authority.generation);
  }
  return env;
}
