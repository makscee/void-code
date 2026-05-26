// Package version holds the build-time version string for vc.
package version

// Version is injected at build time via -ldflags "-X github.com/makscee/void-code/internal/version.Version=vX.Y.Z".
// Falls back to "dev" when running from source.
var Version = "dev"
