// The one place that decides which host answers "who am I, and am I let in".
//
// In production that is not the host that runs sign-in, and neither half can be moved to the
// other's host. Probed 2026-08-23 against relay:443, where a live route answers 401 and an absent
// one answers the CONNECT proxy:
//
//   GET  /v1/vc/me                → 401                            (live, honours our token)
//   GET  /v1/vc/providers         → 400 "This is a CONNECT proxy"
//   POST /v1/public/device/start  → 400 "This is a CONNECT proxy"
//
// while auth.makscee.ru serves providers and device sign-in and rejects our token on /v1/vc/me.
// So the desktop names this host and leaves VC_AUTH_HOST alone: sign-in and the Pi bootstrap keep
// the CLI's own default, which every hand-run `vc` already depends on.
//
// The name says the role, not the route. It follows ErrAccessNotGranted in the CLI, whose comment
// records the rule: name neither the protocol code nor the server mechanism, since both are
// expected to change. "/v1/vc/me" is today's spelling of an access check, not its meaning.
//
// Two seams spawn `vc` — auth-spawn.ts for status and login, desktop-child-env.ts for the chat
// session — and nothing in the type system holds their answers together. They share this module
// so they cannot drift apart.
export const DESKTOP_ACCESS_CHECK_HOST = 'https://relay.makscee.ru';

// `vc` builds the URL by concatenation — `host + "/v1/vc/me"` — so a trailing slash yields
// `//v1/vc/me`, and a path, a query or a fragment cannot survive the concatenation at all.
// A blank value is not a choice anyone made: `vc` reads an empty override as unset (config.go),
// so honouring one here would hand the check back to the legacy checker that 401s our token —
// the exact bug, restored silently by a variable that looks set. Anything unusable falls back
// rather than being repaired: the request built from this host carries a bearer token.
function usableHost(value: string | undefined): string | undefined {
  const trimmed = value?.trim().replace(/\/+$/, '');
  if (!trimmed) return undefined;
  try {
    const parsed = new URL(trimmed);
    if (parsed.protocol !== 'https:' && parsed.protocol !== 'http:') return undefined;
    if (parsed.pathname !== '/' || parsed.search !== '' || parsed.hash !== '') return undefined;
  } catch {
    return undefined;
  }
  return trimmed;
}

// Whether the parent environment gets a say is the caller's decision, and the two callers differ
// on purpose — see the comment at each call site.
export function resolveAccessCheckHost(parent: NodeJS.ProcessEnv): string {
  return usableHost(parent.VC_ACCESS_CHECK_HOST) ?? DESKTOP_ACCESS_CHECK_HOST;
}
