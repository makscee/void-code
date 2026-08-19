import { createHash } from 'node:crypto';

const BETA_VERSION = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)-beta\.([1-9]\d*)$/;
const KEY_ID = /^[a-z0-9][a-z0-9-]{0,63}$/;
const SHA256 = /^[0-9a-f]{64}$/;
const SHA512 = /^(?:[A-Za-z0-9+/]{4}){21}[A-Za-z0-9+/]{2}==$/;
const ISO_UTC = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/;
const MAX_LIFETIME_MS = 7 * 24 * 60 * 60 * 1000;
const MAX_INSTALLER_BYTES = 2 * 1024 * 1024 * 1024;

function canonicalDate(value, label) {
  if (typeof value !== 'string' || !ISO_UTC.test(value) || new Date(value).toISOString() !== value) throw new Error(`${label} must be canonical UTC ISO-8601 with milliseconds`);
  return new Date(value);
}
function requireField(input, field) {
  if (!Object.hasOwn(input, field)) throw new Error(`missing ${field}`);
  return input[field];
}

/** Builds exact UTF-8 payload bytes; signing is deliberately external to this process. */
export function buildClosedBetaPayload(input) {
  if (!input || typeof input !== 'object' || Array.isArray(input)) throw new Error('payload input must be an object');
  const version = requireField(input, 'version');
  const keyId = requireField(input, 'keyId');
  if (typeof version !== 'string' || !BETA_VERSION.test(version)) throw new Error('version must be a closed beta identity');
  if (typeof keyId !== 'string' || !KEY_ID.test(keyId)) throw new Error('keyId invalid');
  const sha256 = requireField(input, 'sha256'); const sha512 = requireField(input, 'sha512'); const size = requireField(input, 'size'); const sequence = requireField(input, 'sequence');
  if (typeof sha256 !== 'string' || !SHA256.test(sha256) || typeof sha512 !== 'string' || !SHA512.test(sha512)) throw new Error('artifact hash invalid');
  if (!Number.isSafeInteger(size) || size < 1 || size > MAX_INSTALLER_BYTES || !Number.isSafeInteger(sequence) || sequence < 1) throw new Error('size or sequence invalid');
  const published = canonicalDate(requireField(input, 'publishedAt'), 'publishedAt');
  const notBefore = canonicalDate(requireField(input, 'notBefore'), 'notBefore');
  const expires = canonicalDate(requireField(input, 'expiresAt'), 'expiresAt');
  if (published > notBefore || notBefore > expires || expires - published > MAX_LIFETIME_MS) throw new Error('manifest lifetime invalid');
  const filename = `Void-Code-${version}-windows-x64.exe`;
  const installerUrl = `https://vc.makscee.ru/download/windows/${filename}`;
  const immutableUrl = `https://github.com/makscee/void-code/releases/download/desktop-v${version}/${filename}`;
  return Buffer.from(JSON.stringify({ schema: 'vc-windows-update-v1', channel: 'closed-beta', keyId, version, platform: 'win32', architecture: 'x64', sequence, installerUrl, immutableUrl, size, sha256, sha512, publishedAt: input.publishedAt, notBefore: input.notBefore, expiresAt: input.expiresAt }), 'utf8');
}

/** Wraps a signer-produced detached signature without parsing or reserializing the signed payload. */
export function assembleClosedBetaEnvelope(payloadBytes, signatureBytes, keyId) {
  if (!Buffer.isBuffer(payloadBytes) || payloadBytes.length < 1 || payloadBytes.length > 64 * 1024) throw new Error('payload size invalid');
  if (!Buffer.isBuffer(signatureBytes) || signatureBytes.length !== 64) throw new Error('signature must be exactly 64 bytes');
  if (typeof keyId !== 'string' || !KEY_ID.test(keyId)) throw new Error('keyId invalid');
  return Buffer.from(`${JSON.stringify({ schema: 'vc-ed25519-envelope-v1', keyId, payload: payloadBytes.toString('base64url'), signature: signatureBytes.toString('base64url') })}\n`, 'utf8');
}

export function payloadSha256(payloadBytes) {
  if (!Buffer.isBuffer(payloadBytes)) throw new Error('payload bytes required');
  return createHash('sha256').update(payloadBytes).digest('hex');
}
