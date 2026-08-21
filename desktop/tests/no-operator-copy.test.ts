import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { RECOVERY_GUIDANCE } from '../src/renderer/recovery';

// This product has no operator: a person who is stuck reads this copy with no one else to turn
// to. Copy that tells them to find another human, or to open a terminal and run a command, tells
// them nothing they can act on — that is the exact defect the sign-in button was built to remove.
// This guards every place copy reaches a person, not just the string that shipped the bug.

// Hands the person's problem to a role the app cannot name for them (there is no operator,
// admin, or support desk on the other end of this software) or waves at an unnamed "someone".
// Deliberately narrow to role-nouns and hand-off verbs so it does not fire on the "Support
// Report" feature name, which is a button, not a person.
const HUMAN_HANDOFF =
  /\b(operator|administrator|admin|developer)\b|\b(support|it)\s+(team|staff|department|desk)\b|\b(ask|find|contact|call|reach out to)\s+(someone|a person|your\s+(?:it|admin|support))\b/i;

// Tells the person to operate a terminal or shell themselves — the thing this app exists to do
// for them. Requires an instructive verb directly against the noun so it does not fire on
// descriptive copy like "no shell was opened" or the Support Report's "excludes ... terminal
// output", neither of which asks the reader to do anything.
const TERMINAL_INSTRUCTION =
  /\b(open|use|run|start|type)\s+(a\s+|the\s+|your\s+)?(terminal|shell|command(?:\s+(?:line|prompt))?)\b|\brun\s+(a|this|the)\s+command\b/i;

const BANNED_PATTERNS: [RegExp, string][] = [
  [HUMAN_HANDOFF, 'hands the problem to another human instead of telling the person what to do'],
  [TERMINAL_INSTRUCTION, 'instructs the person to operate a terminal or run a command themselves'],
];

function violations(source: string, text: string): string[] {
  const found: string[] = [];
  for (const [pattern, reason] of BANNED_PATTERNS) {
    const match = text.match(pattern);
    if (match) found.push(`${source}: "${match[0]}" — ${reason}`);
  }
  return found;
}

describe('no copy sends a person to an operator or a terminal', () => {
  it('recovery guidance never hands the problem to a human or a shell', () => {
    // Every RecoveryCode's heading and detail is copy a stopped/failed chat can put on screen.
    const found = Object.entries(RECOVERY_GUIDANCE).flatMap(([code, guidance]) => [
      ...violations(`RECOVERY_GUIDANCE.${code}.heading`, guidance.heading),
      ...violations(`RECOVERY_GUIDANCE.${code}.detail`, guidance.detail),
    ]);
    expect(found).toEqual([]);
  });

  it('the trusted-folder announcement never hands the problem to a human or a shell', () => {
    // index.ts drives announce(...) with string literals; scan the source so a new announce()
    // call carrying the same defect is caught wherever it is added, not only at today's line.
    const renderer = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');
    expect(violations('src/renderer/index.ts', renderer)).toEqual([]);
  });

  it('the static shell (index.html) never hands the problem to a human or a shell', () => {
    const html = readFileSync(new URL('../src/renderer/index.html', import.meta.url), 'utf8');
    expect(violations('src/renderer/index.html', html)).toEqual([]);
  });
});
