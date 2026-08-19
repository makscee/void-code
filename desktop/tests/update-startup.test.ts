import { describe, expect, it, vi } from 'vitest';
import { beginStartupUpdateCheck } from '../src/main/update-startup';

describe('desktop stable update startup', () => {
  it('starts exactly one check after renderer creation and does not await it', async () => {
    let resolveCheck!: () => void;
    const check = vi.fn(() => new Promise<void>((resolve) => { resolveCheck = resolve; }));

    beginStartupUpdateCheck(check);

    expect(check).toHaveBeenCalledTimes(1);
    expect(resolveCheck).toBeTypeOf('function');
    resolveCheck();
  });
});
