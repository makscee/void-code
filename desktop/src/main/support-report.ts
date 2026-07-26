import type { RecoveryCode, RuntimeSupportState } from '../shared/contract';

export type SupportPlatform = 'windows' | 'macos' | 'linux' | 'other';
export type SupportArchitecture = 'x64' | 'arm64' | 'other';
export type WorkspaceSupportState = 'none' | 'ready' | 'missing';

export interface SupportReport {
  schema: 1;
  app: { name: 'Void Code'; version: string };
  system: { platform: SupportPlatform; architecture: SupportArchitecture };
  generatedAt: string;
  state: { workspace: WorkspaceSupportState; runtime: RuntimeSupportState; recoveryCode: RecoveryCode };
}

export interface SupportReportInput {
  appVersion: string;
  platform: string;
  architecture: string;
  generatedAt: string;
  workspace: WorkspaceSupportState;
  runtime: RuntimeSupportState;
  recoveryCode: RecoveryCode;
}

function platform(value: string): SupportPlatform {
  return value === 'win32' ? 'windows' : value === 'darwin' ? 'macos' : value === 'linux' ? 'linux' : 'other';
}
function architecture(value: string): SupportArchitecture { return value === 'x64' || value === 'arm64' ? value : 'other'; }

export function buildSupportReport(input: SupportReportInput): SupportReport {
  if (!/^\d+\.\d+\.\d+$/.test(input.appVersion)) throw new Error('invalid app version');
  if (!Number.isFinite(Date.parse(input.generatedAt))) throw new Error('invalid report timestamp');
  return {
    schema: 1,
    app: { name: 'Void Code', version: input.appVersion },
    system: { platform: platform(input.platform), architecture: architecture(input.architecture) },
    generatedAt: new Date(input.generatedAt).toISOString(),
    state: { workspace: input.workspace, runtime: input.runtime, recoveryCode: input.recoveryCode },
  };
}

export function serializeSupportReport(report: SupportReport): string { return `${JSON.stringify(report, null, 2)}\n`; }

export function copySupportReport(report: SupportReport, writeText: (text: string) => void): { action: 'copied' } {
  writeText(serializeSupportReport(report));
  return { action: 'copied' };
}

export async function saveSupportReport(
  report: SupportReport,
  chooseFile: () => Promise<string | null>,
  writeReport: (file: string, text: string) => void,
): Promise<{ action: 'saved' | 'cancelled' }> {
  const file = await chooseFile();
  if (file === null) return { action: 'cancelled' };
  writeReport(file, serializeSupportReport(report));
  return { action: 'saved' };
}
