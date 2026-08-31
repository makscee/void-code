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
  // Widened from ^\d+\.\d+\.\d+$ deliberately: a build off the tag reports
  // 0.2.50-3-gabc1234, and a support report that cannot be generated for a
  // branch build fails exactly when support is needed. Normalizing it down to
  // the tag was rejected -- the field exists to identify the build in front of
  // the person, and rounding sends the reader to the wrong source tree.
  // scripts/windows-pilot-rehearsal-lib.mjs holds this rule too, spelled
  // identically, because neither file can import the other.
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(input.appVersion)) throw new Error('invalid app version');
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
