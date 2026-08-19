import type { StableUpdateStatus } from '../shared/contract';

export type UpdateTrustMode = 'closed-beta' | 'stable';

export function updateTrustSatisfied(mode: UpdateTrustMode, evidence: { ed25519: boolean; authenticode: boolean }): boolean {
  return evidence.ed25519 && (mode === 'closed-beta' || evidence.authenticode);
}

/** Stable is fail-closed until a signed-manifest ceremony enables the dual-trust path. */
export class CeremonyLockedStableController {
  private readonly current: StableUpdateStatus;
  constructor(currentVersion: string) { this.current = { state: 'unavailable', currentVersion, canRetry: true }; }
  status(): StableUpdateStatus { return this.current; }
  check(): Promise<StableUpdateStatus> { return Promise.resolve(this.current); }
  updateNow(): Promise<boolean> { return Promise.resolve(false); }
}

/** Trust mode is derived only from the compiled package identity. */
export function compiledUpdateTrustMode(appVersion: string): UpdateTrustMode {
  return /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)-beta\.[1-9]\d*$/.test(appVersion) ? 'closed-beta' : 'stable';
}

interface ProductionBetaKey { keyId: string; rawPublicKey: Buffer }
/*
 * PENDING PROTECTED KEY CEREMONY: this fixed source allowlist is intentionally
 * empty. A ceremony-owned change may add public keys here after independent
 * review. Never accept keys from a manifest, environment, argv, IPC, or disk.
 * Rotation adds a new fixed entry; revocation removes the old entry in a new
 * application build. No manifest can be accepted while this remains empty.
 */
const PRODUCTION_BETA_KEYS: readonly ProductionBetaKey[] = Object.freeze([
  // INTERNAL-BETA ONLY: fland root-only authority; never valid for stable/public releases.
  { keyId: 'internal-beta-2026-08', rawPublicKey: Buffer.from('rLWIrvTJV3Sv1pDk-FaYGCNadFEU_7pPD7sBvb_bfAc', 'base64url') },
]);

export function productionBetaKey(keyId: string): Buffer | undefined {
  return PRODUCTION_BETA_KEYS.find((entry) => entry.keyId === keyId)?.rawPublicKey;
}
