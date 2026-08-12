import { describe, expect, it } from 'vitest';
import { desktopChildEnv } from '../src/main/desktop-child-env';

const poison = { NODE_OPTIONS: '--require evil', ELECTRON_RUN_AS_NODE: '1', VC_RELAY_HOST: 'evil', PI_CODING_AGENT_DIR: '/evil', ANTHROPIC_AUTH_TOKEN: 'secret', HTTPS_PROXY: 'evil', AWS_SECRET_ACCESS_KEY: 'secret', PATH: '/evil' };
describe('desktopChildEnv', () => {
  it('constructs an exact Darwin allowlist', () => {
    expect(desktopChildEnv('darwin', { ...poison, HOME: '/Users/real', TMPDIR: '/private/tmp/real' }, '/app/private/node', { path: '/status', chatId: 'chat', generation: 3 })).toEqual({
      HOME: '/Users/real', TMPDIR: '/private/tmp/real', PATH: '/app/private:/usr/bin:/bin', TERM: 'xterm-256color', COLORTERM: 'truecolor',
      VC_DESKTOP_STATUS_PATH: '/status', VC_DESKTOP_CHAT_ID: 'chat', VC_DESKTOP_STATUS_GENERATION: '3',
    });
  });
  it('constructs a case-insensitive Windows allowlist without status authority', () => {
    expect(desktopChildEnv('win32', { ...poison, userprofile: 'C:\\Users\\real', SYSTEMROOT: 'D:\\Windows', temp: 'T:\\temp', tmp: 'T:\\tmp', homedrive: 'C:', homepath: '\\Users\\real' }, 'C:\\app\\node.exe')).toEqual({
      USERPROFILE: 'C:\\Users\\real', SystemRoot: 'D:\\Windows', TEMP: 'T:\\temp', TMP: 'T:\\tmp', HOMEDRIVE: 'C:', HOMEPATH: '\\Users\\real',
      PATH: 'C:\\app;D:\\Windows\\System32', TERM: 'xterm-256color', COLORTERM: 'truecolor',
    });
  });
  it('rejects missing required homes, temp, or system root', () => {
    expect(() => desktopChildEnv('darwin', { TMPDIR: '/tmp' }, '/node')).toThrow('desktop session environment is unavailable');
    expect(() => desktopChildEnv('win32', { USERPROFILE: 'C:\\u', TEMP: 'T', TMP: 'T' }, 'C:\\node.exe')).toThrow('desktop session environment is unavailable');
  });
});
