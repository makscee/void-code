import { createHash } from 'node:crypto';

export const PROCESS_NAMES = new Set(['Void Code', 'Void Code.exe', 'vc', 'vc.exe', 'node', 'node.exe', 'OpenConsole', 'OpenConsole.exe', 'conhost', 'conhost.exe']);
const phases = new Set(['preflight', 'during_launch', 'after_chat_close', 'after_quit', 'after_uninstall', 'support_report']);
const checks = new Set(['MANIFEST', 'PROCESS_OWNERSHIP', 'PROCESS_EXIT', 'SUPPORT_REPORT']);
const codes = new Set(['NONE', 'MANIFEST_INVALID', 'ARTIFACT_MISMATCH', 'OPERATOR_GATE_BLOCKED', 'ROOT_NOT_FOUND', 'OWNERSHIP_AMBIGUOUS', 'OWNED_PROCESS_REMAINS', 'SUPPORT_REPORT_INVALID']);
const sha = /^[a-f0-9]{64}$/;
const iso = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/;

function exact(value, keys) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  return Object.keys(value).sort().join('\0') === [...keys].sort().join('\0');
}
export function supportReportValid(value) {
  return exact(value, ['schema', 'app', 'system', 'generatedAt', 'state']) &&
    exact(value.app, ['name', 'version']) && exact(value.system, ['platform', 'architecture']) && exact(value.state, ['workspace', 'runtime', 'recoveryCode']) &&
    value.schema === 1 && value.app.name === 'Void Code' && /^\d+\.\d+\.\d+$/.test(value.app.version) &&
    ['windows', 'macos', 'linux', 'other'].includes(value.system.platform) && ['x64', 'arm64', 'other'].includes(value.system.architecture) &&
    iso.test(value.generatedAt) && !Number.isNaN(Date.parse(value.generatedAt)) && ['none', 'ready', 'missing'].includes(value.state.workspace) &&
    ['not_started', 'running', 'ended', 'start_failed'].includes(value.state.runtime) &&
    ['NONE', 'AUTH_PREFLIGHT_REQUIRED', 'SESSION_START_FAILED', 'RUNTIME_EXITED', 'WORKSPACE_MISSING', 'SESSION_MISSING'].includes(value.state.recoveryCode);
}
export function descendantSnapshot(rows, rootPid) {
  if (!Number.isInteger(rootPid) || rootPid <= 0 || rows.filter((row) => row.pid === rootPid).length !== 1) throw new Error('ROOT_NOT_FOUND');
  const root = rows.find((row) => row.pid === rootPid);
  if (!['Void Code', 'Void Code.exe'].includes(root.name)) throw new Error('OWNERSHIP_AMBIGUOUS');
  if (new Set(rows.map((row) => row.pid)).size !== rows.length) throw new Error('OWNERSHIP_AMBIGUOUS');
  const ids = new Set([rootPid]);
  let changed;
  do {
    changed = false;
    for (const row of rows) {
      if (ids.has(row.parentPid) && !ids.has(row.pid)) { ids.add(row.pid); changed = true; }
    }
  } while (changed);
  const result = rows.filter((row) => ids.has(row.pid)).sort((a, b) => a.pid - b.pid);
  if (result.some((row) => !PROCESS_NAMES.has(row.name) || row.pid === row.parentPid)) throw new Error('OWNERSHIP_AMBIGUOUS');
  return result.map((row) => ({ name: row.name.replace(/\.exe$/, ''), pid: row.pid, parentPid: row.parentPid }));
}
export function serializeEvidence(value) {
  if (!exact(value, ['schema', 'phase', 'occurredAt', 'result', 'check', 'coarseCode', 'candidate', 'processes', 'support']) || value.schema !== 1 || !phases.has(value.phase) || !iso.test(value.occurredAt) || !['PASS', 'STOP'].includes(value.result) || !checks.has(value.check) || !codes.has(value.coarseCode) || !Array.isArray(value.processes)) throw new Error('EVIDENCE_INVALID');
  if (value.candidate !== null && (!exact(value.candidate, ['installerBasename', 'expectedSha256', 'actualSha256', 'operatorGate', 'signature', 'motw']) || !sha.test(value.candidate.expectedSha256) || !sha.test(value.candidate.actualSha256) || value.candidate.operatorGate !== 'verified' || value.candidate.signature !== 'not_signed' || !['present', 'absent'].includes(value.candidate.motw))) throw new Error('EVIDENCE_INVALID');
  if (value.support !== null && (!exact(value.support, ['sha256', 'valid']) || !sha.test(value.support.sha256) || value.support.valid !== true)) throw new Error('EVIDENCE_INVALID');
  if (value.processes.some((row) => !exact(row, ['name', 'pid', 'parentPid']) || !Number.isInteger(row.pid) || !Number.isInteger(row.parentPid))) throw new Error('EVIDENCE_INVALID');
  return `${JSON.stringify(value)}\n`;
}
export function sha256Text(value) { return createHash('sha256').update(value).digest('hex'); }
