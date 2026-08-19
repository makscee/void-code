# Stable Desktop one-click update contract v1

VC Desktop reads the bounded canonical JSON at `https://vc.makscee.ru/desktop/stable-v1.json`. Pending publication is exactly:

```json
{"schemaVersion":1,"status":"unavailable"}
```

An accepted document has exactly these fields (no unknown fields):

```json
{
  "schemaVersion": 1,
  "status": "accepted",
  "version": "0.2.0",
  "platform": "win32",
  "architecture": "x64",
  "feedUrl": "https://vc.makscee.ru/download/windows/",
  "artifactUrl": "https://vc.makscee.ru/download/windows/Void-Code-0.2.0-windows-x64.exe",
  "immutableUrl": "https://github.com/makscee/void-code/releases/download/desktop-v0.2.0/Void-Code-0.2.0-windows-x64.exe",
  "sha256": "64 lowercase hexadecimal characters",
  "sha512": "base64-encoded 64-byte SHA-512",
  "size": 1,
  "publisherName": "certificate-derived Authenticode publisher",
  "publishedAt": "2026-08-17T00:00:00.000Z"
}
```

The version is strict stable `MAJOR.MINOR.PATCH`. URLs must equal the derived values above with no credentials, port, query, or fragment. Size is a positive safe integer and publication time is canonical UTC ISO 8601 with milliseconds.

## One-click trust and packaging prerequisites

`electron-updater` uses the generic feed and NSIS. It never auto-downloads or installs on ordinary app quit; prerelease, downgrade, web installers, and differential downloads are disabled. Before download, VC requires generated `latest.yml` to name the same version and exactly one full EXE URL, with matching SHA-512 and an exact numeric size equal to the accepted manifest; missing size is rejected. The packaged `app-update.yml` must say `provider: generic`, name the exact feed, and contain a nonempty `publisherName` equal to this manifest. Electron-updater's Authenticode verification remains enabled. VC then independently checks size and SHA-256 before installation.

No publisher is pinned in source. Electron-builder must derive `app-update.yml`'s `publisherName` from the actual approved signing certificate. Windows package qualification separately requires the operator to supply that certificate's expected signer common name in the non-secret `VC_DESKTOP_EXPECTED_PUBLISHER` environment variable; both the frozen installer and packaged application executable must have Authenticode status `Valid` and an exact certificate `SimpleName` match before generated update metadata is compared. Missing signing input, expectation, certificate, or signature fails closed without certificate-detail logging. Electron-updater's default `verifyUpdateCodeSignature` implementation remains enabled, and executable signing remains available when real approved certificate inputs are supplied.

Frozen 0.1.1: `RETIRED_INTERNAL_REVIEW_FAILED`. That package is unsigned, contains two S1 findings discovered during internal review, and must never publish or accept. Only 0.1.2 may become the post-fix candidate; the 0.1.1 identity and assets must not be overwritten or reused.

Consequently the current unsigned 0.1.2 candidate remains blocked pending signing and cannot satisfy this contract, be enabled for accepted one-click release, or make the portal available. A signed package must generate `latest.yml` and certificate-derived `app-update.yml` without publishing, pass signature and metadata validation, and then pass the separate installed older-to-newer Windows E2E before any portal/release mutation.

Network access is HTTPS-only. First-party paths are limited to the canonical JSON, `latest.yml`, and the exact versioned EXE. Redirects are accepted only to the exact immutable GitHub release path or `release-assets.githubusercontent.com/github-production-release-asset/`. Failed or partial downloads are cleaned up and leave the current app usable and retryable.
