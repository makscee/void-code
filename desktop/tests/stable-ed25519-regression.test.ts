import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { CeremonyLockedStableController, updateTrustSatisfied } from '../src/main/update-trust';

describe('stable Authenticode plus Ed25519 regression', () => {
  it.each([
    ['stable unsigned', 'stable' as const, false, true, false],
    ['stable wrong publisher', 'stable' as const, true, false, false],
    ['stable both', 'stable' as const, true, true, true],
    ['beta Ed25519', 'closed-beta' as const, true, false, true],
    ['beta unsigned', 'closed-beta' as const, false, true, false],
  ])('%s', (_name, mode, ed25519, authenticode, accepted) => expect(updateTrustSatisfied(mode, { ed25519, authenticode })).toBe(accepted));

  it('keeps runtime stable mode ceremony-locked instead of falling back to unsigned legacy manifests', async () => {
    const controller = new CeremonyLockedStableController('0.1.3');
    await expect(controller.check()).resolves.toMatchObject({ state: 'unavailable' });
    await expect(controller.updateNow()).resolves.toBe(false);
    const main = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');
    expect(main).toContain('new CeremonyLockedStableController(app.getVersion())');
  });
});
