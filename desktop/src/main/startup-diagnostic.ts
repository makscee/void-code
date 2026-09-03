import { closeSync, fsyncSync, mkdirSync, openSync, renameSync, writeFileSync } from 'node:fs';
import path from 'node:path';
// Straight from private-runtime.js rather than through resources.ts: resources.ts is now the worker
// wrapper, and this file needs the words the validation throws, not a way to run it off-thread.
import { MISSING_RUNTIME_ASSET_MESSAGES } from './private-runtime';

// A value, not a union: a union is erased before anything can look at it, and this list is exactly
// what a test needs to be able to read. The type is derived from it, so the two cannot disagree.
//
// The two page loads are separate stages because they fail for different reasons and send the
// reader to different places: 'loading-page-load' is a window and a local scriptless page,
// 'renderer-load' is the built application bundle.
export const STARTUP_STAGES = ['readiness', 'runtime-validation', 'window-creation', 'loading-page-load', 'renderer-load'] as const;
export type StartupStage = (typeof STARTUP_STAGES)[number];
export interface StartupDiagnostic {
  schema: 1;
  code: 'STARTUP_FAILED';
  occurredAt: string;
  stage: StartupStage;
  appVersion: string;
  platform: string;
  error: { name: string; message: string; stack: string };
}

const safeMessages = new Set([
  // Taken from resources.ts rather than retyped: these are the messages that tell a person their
  // file is gone, and a copy here that fell out of step would put the app back to answering
  // "Unexpected startup error" while both files looked correct.
  ...MISSING_RUNTIME_ASSET_MESSAGES,
  'private executables must be outside asar',
  'unsupported private runtime manifest',
  'vc resource hash mismatch',
  'Node resource hash mismatch',
  'fixture resource hash mismatch',
  'Pi resource hash mismatch',
  'Pi entrypoint unavailable',
  'renderer failed to load',
  'window creation failed',
]);

const safeErrorNames = new Set(['Error', 'TypeError', 'RangeError', 'ReferenceError', 'SyntaxError']);

function safeStack(error: unknown): string {
  if (!(error instanceof Error) || !error.stack) return 'stack unavailable';
  const safe = error.stack.split('\n').slice(1).flatMap((line) => {
    if (!/^\s*at /.test(line)) return [];
    const location = /:(\d+):(\d+)\)?$/.exec(line);
    return location ? [`at ${location[1]}:${location[2]}`] : [];
  }).slice(0, 12);
  return safe.length ? safe.join('\n') : 'stack unavailable';
}

export function startupDiagnostic(stage: StartupStage, error: unknown, appVersion: string, occurredAt = new Date().toISOString()): StartupDiagnostic {
  const name = error instanceof Error && safeErrorNames.has(error.name) ? error.name : 'Error';
  const message = error instanceof Error && safeMessages.has(error.message) ? error.message : 'Unexpected startup error';
  return { schema: 1, code: 'STARTUP_FAILED', occurredAt, stage, appVersion, platform: `${process.platform}-${process.arch}`, error: { name, message, stack: safeStack(error) } };
}

const MISSING_ASSET = new Set<string>(MISSING_RUNTIME_ASSET_MESSAGES);

/**
 * What the person reads. The only part of a startup failure anybody sees.
 *
 * The argument is optional, and that is not politeness: the caller that has no diagnostic to give --
 * and the test that checks this text cannot leak a secret -- both call it with nothing, and making
 * the parameter required would have turned that check red for a reason that has nothing to do with
 * what it guards.
 *
 * Only a file that is gone gets its own wording. Everything else keeps the message it had, because a
 * renderer that failed to load is not a missing file and telling that person to suspect their
 * antivirus would send them somewhere there is nothing to find.
 *
 * The wording names the likely cause rather than the cause. A missing file is not proof of an
 * antivirus: a failed install and a disk cleaner leave the same hole, and the answer is the same for
 * all three. Saying "antivirus removed it" as though we knew would be wrong in two cases out of
 * three and would send somebody to fight software that did nothing.
 *
 * It is dispatched on the whitelisted message, so nothing an error carries can reach this text: an
 * error that is not on the list is already "Unexpected startup error" by the time it arrives here.
 */
export function startupDialogMessage(diagnostic?: StartupDiagnostic): string {
  if (diagnostic !== undefined && MISSING_ASSET.has(diagnostic.error.message)) {
    return 'Void Code could not open: one of the files it installed is missing. Antivirus software is usually what removed it, though a failed installation or a disk cleaner can leave the same hole. Reinstall the application. A startup-error.json diagnostic was saved in Void Code application data.';
  }
  return 'Void Code could not open. Reinstall the application and try again. A startup-error.json diagnostic was saved in Void Code application data.';
}

export interface StartupFailureReport { diagnostic: StartupDiagnostic; dialogMessage: string; }

/**
 * Both answers a startup failure raises, from one call: the record to keep and the words to show.
 *
 * They were two calls, and the caller made the join -- which it got wrong in the only way that
 * mattered, handing the dialog nothing and telling every person the same general sentence whatever
 * had happened. The function was pinned and its use was not, and reverting the fix passed every test
 * and the compiler alike.
 *
 * A pair rather than two exports is the answer to that: the text and the record are answers to the
 * same question, so they cannot disagree about it, and the caller is left no wiring of its own to
 * get wrong. It is the third time in this branch that the way to close a seam was to remove the
 * level above it rather than to pin it.
 */
export function startupFailureReport(stage: StartupStage, error: unknown, appVersion: string, occurredAt?: string): StartupFailureReport {
  const diagnostic = startupDiagnostic(stage, error, appVersion, occurredAt);
  return { diagnostic, dialogMessage: startupDialogMessage(diagnostic) };
}

export function writeStartupDiagnostic(userData: string, diagnostic: StartupDiagnostic): string {
  mkdirSync(userData, { recursive: true, mode: 0o700 });
  const output = path.join(userData, 'startup-error.json');
  const temporary = path.join(userData, `.startup-error-${process.pid}.tmp`);
  writeFileSync(temporary, `${JSON.stringify(diagnostic, null, 2)}\n`, { encoding: 'utf8', mode: 0o600 });
  const file = openSync(temporary, 'r+');
  try { fsyncSync(file); } finally { closeSync(file); }
  renameSync(temporary, output);
  if (process.platform !== 'win32') {
    const directory = openSync(userData, 'r');
    try { fsyncSync(directory); } finally { closeSync(directory); }
  }
  return output;
}
