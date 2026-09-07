import { describe, expect, it } from 'vitest';
import { desktopChildEnv, fixtureChildEnv } from '../src/main/desktop-child-env';

const poison = { NODE_OPTIONS: '--require evil', ELECTRON_RUN_AS_NODE: '1', VC_RELAY_HOST: 'evil', PI_CODING_AGENT_DIR: '/evil', ANTHROPIC_AUTH_TOKEN: 'secret', HTTPS_PROXY: 'evil', AWS_SECRET_ACCESS_KEY: 'secret', PATH: '/evil' };
describe('desktopChildEnv', () => {
  it('constructs an exact Darwin allowlist', () => {
    expect(desktopChildEnv('darwin', { ...poison, HOME: '/Users/real', TMPDIR: '/private/tmp/real' }, '/app/private/node', { path: '/status', chatId: 'chat', generation: 3 })).toEqual({
      HOME: '/Users/real', TMPDIR: '/private/tmp/real', PATH: '/app/private:/usr/bin:/bin', TERM: 'xterm-256color', COLORTERM: 'truecolor',
      VC_ACCESS_CHECK_HOST: 'https://relay.makscee.ru',
      VC_DESKTOP_STATUS_PATH: '/status', VC_DESKTOP_CHAT_ID: 'chat', VC_DESKTOP_STATUS_GENERATION: '3',
    });
  });
  it('constructs a case-insensitive Windows allowlist without status authority', () => {
    expect(desktopChildEnv('win32', { ...poison, userprofile: 'C:\\Users\\real', SYSTEMROOT: 'D:\\Windows', temp: 'T:\\temp', tmp: 'T:\\tmp', homedrive: 'C:', homepath: '\\Users\\real' }, 'C:\\app\\node.exe')).toEqual({
      USERPROFILE: 'C:\\Users\\real', SystemRoot: 'D:\\Windows', TEMP: 'T:\\temp', TMP: 'T:\\tmp', HOMEDRIVE: 'C:', HOMEPATH: '\\Users\\real',
      PATH: 'C:\\app;D:\\Windows\\System32', TERM: 'xterm-256color', COLORTERM: 'truecolor',
      VC_ACCESS_CHECK_HOST: 'https://relay.makscee.ru',
    });
  });
  it('rejects missing required homes, temp, or system root', () => {
    expect(() => desktopChildEnv('darwin', { TMPDIR: '/tmp' }, '/node')).toThrow('desktop session environment is unavailable');
    expect(() => desktopChildEnv('win32', { USERPROFILE: 'C:\\u', TEMP: 'T', TMP: 'T' }, 'C:\\node.exe')).toThrow('desktop session environment is unavailable');
  });
});

// ---------------------------------------------------------------------------
// The fixture session dies on Windows before it reaches a line of our code:
//
//     node.exe: node::InitializeOncePerProcessInternal at node.cc:1266
//     Assertion failed: ncrypto::CSPRNG(nullptr, 0)          exitCode 134
//
// Node seeds its generator at startup from a system source that it reaches through %SystemRoot%,
// and the fixture environment has never carried it. Measured on the installed application, over the
// fixture's whole round trip: as shipped it exits 134; plus SystemRoot alone it exits 0 and the
// round trip completes; plus SystemRoot, TEMP, TMP and USERPROFILE it does exactly the same. The
// extra variables buy nothing, which is why only one of them is pinned below -- aligning the two
// environments was considered and dropped once the numbers were in, rather than argued about.
//
// The trap worth naming, because it is the reason four weeks passed: the fixture already reads
// SystemRoot, with `?? 'C:\\Windows'` behind it. Somebody foresaw that the variable might be absent
// and guarded their own code against it. What they could not foresee is that Node does not start
// without it -- the guard sits one step past the place that breaks. Anyone who sees that fallback
// and concludes the question is settled will be wrong in the same way.
//
// The defect is four weeks old and was written next to its own fix: the fixture environment was
// added on 12.08, and SystemRoot went into desktopChildEnv the same day. One path was hardened and
// the other was written beside it without the variable. Nobody saw it because the fixture is
// exercised by the macOS check, where the variable changes nothing.
//
// Which is why this is a function taking the platform rather than reading process.platform: the
// suite runs on macos-14 and nowhere else (desktop-tests.yml), so a check that only fires on
// Windows would never fire at all.
// ---------------------------------------------------------------------------
describe('fixtureChildEnv', () => {
  it('carries SystemRoot on Windows, which Node needs before it runs a line of ours', () => {
    expect(fixtureChildEnv('win32', { ...poison, SystemRoot: 'D:\\Windows' })).toEqual({
      SystemRoot: 'D:\\Windows', PATH: 'D:\\Windows\\System32',
      TERM: 'xterm-256color', COLORTERM: 'truecolor', VOID_FIXTURE: 'owned',
    });
  });

  it('finds SystemRoot however Windows spelled it', () => {
    // Windows environment names are case-insensitive and arrive in whatever case the parent used;
    // desktopChildEnv already looks them up that way, and a fixture that did not would fail on the
    // machines that spell it SYSTEMROOT while passing everywhere this is ever run.
    expect(fixtureChildEnv('win32', { SYSTEMROOT: 'D:\\Windows' }).SystemRoot).toBe('D:\\Windows');
  });

  it('leaves the Darwin fixture environment exactly as it was', () => {
    // Scope: the variable means nothing here, and adding it would make the two platforms differ for
    // no reason -- which is the shape of mistake this whole test exists about.
    expect(fixtureChildEnv('darwin', { ...poison, HOME: '/Users/real', TMPDIR: '/private/tmp/real' })).toEqual({
      PATH: '/usr/bin:/bin', TERM: 'xterm-256color', COLORTERM: 'truecolor', VOID_FIXTURE: 'owned',
    });
  });

  it('still starts when the parent environment has nothing to give, unlike the session environment', () => {
    // The deliberate difference between the two. desktopChildEnv refuses without USERPROFILE,
    // SystemRoot, TEMP and TMP -- correctly, since a real session cannot work without them. The
    // fixture is a check that must run in bare places, and the measurement says it needs none of
    // them: the round trip passes on SystemRoot alone. So it falls back rather than refusing, and
    // this is pinned so that "align the two" is not later done by making this one throw as well.
    expect(() => desktopChildEnv('win32', {}, 'C:\\node.exe')).toThrow('desktop session environment is unavailable');
    const bare = fixtureChildEnv('win32', {});
    expect(bare.SystemRoot, 'the fixture has no system root to fall back on').toBeTruthy();
    expect(bare.PATH, 'the fallback root and the PATH built from it disagree').toContain(bare.SystemRoot);
  });
});
