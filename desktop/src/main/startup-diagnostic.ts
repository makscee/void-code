import { closeSync, fsyncSync, mkdirSync, openSync, renameSync, writeFileSync } from 'node:fs';
import path from 'node:path';

export type StartupStage = 'readiness' | 'runtime-validation' | 'window-creation' | 'renderer-load';
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

export function startupDialogMessage(t: (message: string) => string = (message) => message): string {
  return t('Void Code could not open. Reinstall the application and try again. A startup-error.json diagnostic was saved in Void Code application data.');
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
