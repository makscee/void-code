import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { RUNTIME_PLATFORMS } from '../src/main/private-runtime';

// One table, written twice. private-runtime.js has to stay plain JavaScript -- a worker thread
// loads it and node reads no TypeScript -- so its types live in private-runtime.d.ts, and the list
// of platforms is spelled in both. The header of the .d.ts admits it outright: "nothing mechanical
// keeps the two in step".
//
// The two directions of drift are not equally dangerous, which is why this compares sets rather
// than checking one way. A platform in the .js and missing from the .d.ts fails to compile, loudly,
// here. A platform in the .d.ts and missing from the .js compiles perfectly: the compiler believes
// the declaration, every call site type-checks, and the refusal arrives on somebody's machine as
// "no private runtime is built for <their platform>".
//
// Half of this is a real link and half is not, and the halves are worth separating. RUNTIME_PLATFORMS
// is the array the running code actually reads, so a change there is caught for real. A type has no
// runtime form, so the .d.ts side is parsed out of its text -- and a union written in some shape
// this parser does not recognise would read as empty, which is why it is asserted non-empty first.

const declaration = readFileSync(new URL('../src/main/private-runtime.d.ts', import.meta.url), 'utf8');

function declaredPlatforms(): string[] {
  const union = /export type RuntimePlatform\s*=([^;]*);/.exec(declaration)?.[1] ?? '';
  return [...union.matchAll(/'([^']+)'/g)].map((match) => match[1]);
}

describe('the platform table cannot drift between the two files that spell it', () => {
  it('declares in private-runtime.d.ts exactly what private-runtime.js accepts', () => {
    const declared = declaredPlatforms();
    expect(declared, 'no RuntimePlatform union could be read out of private-runtime.d.ts').not.toHaveLength(0);
    expect(RUNTIME_PLATFORMS, 'private-runtime.js exports no RUNTIME_PLATFORMS to compare against').toBeInstanceOf(Array);

    const accepted = [...RUNTIME_PLATFORMS];
    expect(declared.filter((platform) => !accepted.includes(platform)),
      'declared in private-runtime.d.ts but not accepted by private-runtime.js -- this compiles everywhere and fails on the machine it names').toEqual([]);
    expect(accepted.filter((platform) => !declared.includes(platform)),
      'accepted by private-runtime.js but not declared in private-runtime.d.ts').toEqual([]);
  });
});
