// The only trace of why a login silently did nothing is stderr text and malformed-output
// lines — auth-session.ts already routes both to a diagnostic sink instead of the renderer.
// This store is where that sink lands: retained past the moment the login attempt ends, so a
// person can hand the lines over, but bounded on every axis a long-running child's stderr could
// otherwise use to leak — line count per login, distinct logins retained, and a single line's length.

export interface LoginDiagnosticsStore {
  record(loginId: string, message: string): void;
  get(loginId: string): string[];
}

export function createLoginDiagnosticsStore(perLoginLineBound = 20, loginCountBound = 5, lineLengthBound = 500): LoginDiagnosticsStore {
  const logins = new Map<string, string[]>();
  return {
    record(loginId, message) {
      const line = message.length > lineLengthBound ? message.slice(0, lineLengthBound) : message;
      let lines = logins.get(loginId);
      if (!lines) {
        lines = [];
        logins.set(loginId, lines);
        // A brand-new login pushes the oldest one out once the retained-logins bound is
        // exceeded — recording more lines against an already-tracked login must never do this.
        if (logins.size > loginCountBound) {
          const oldest = logins.keys().next().value;
          if (oldest !== undefined) logins.delete(oldest);
        }
      }
      lines.push(line);
      if (lines.length > perLoginLineBound) lines.splice(0, lines.length - perLoginLineBound);
    },
    get(loginId) {
      return [...(logins.get(loginId) ?? [])];
    },
  };
}
