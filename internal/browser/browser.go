// Package browser provides cross-platform URL-opening with a terminal fallback.
//
// On darwin  → `open <url>`
// On linux   → `xdg-open <url>`
// On windows → `cmd /c start <url>`
//
// If the open command fails (headless, no DISPLAY, SSH, or unknown GOOS), the
// URL is printed to the provided writer so the user can open it manually.
// The fallback is not an error — OpenURL always returns nil.
package browser

import (
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"runtime"
)

// ProfileURL is the canonical void-code profile page URL.
const ProfileURL = "https://profile.makscee.ru/profile"

// profileHost is the scheme+host of the profile web app, shared with ProfileURL.
const profileHost = "https://profile.makscee.ru"

// RedeemURL returns the magic-link redeem URL for a vc-web-session token.
// VCD-80: the void-auth server emits a broken magic link
// (auth.makscee.ru/profile?ml=…, 404), so vc builds the correct redeem URL
// itself — it hits void-client's GET /api/profile/ml handler, which validates
// the token, sets the vc_session cookie, and redirects to /profile.
func RedeemURL(mlToken string) string {
	return profileHost + "/api/profile/ml?ml=" + url.QueryEscape(mlToken)
}

// CommandForGOOS returns the open-browser command for the given GOOS value.
// Returns nil if no known command exists for the platform.
// The URL is appended by the caller: exec.Command(cmd[0], append(cmd[1:], url)...).
func CommandForGOOS(goos string) []string {
	switch goos {
	case "darwin":
		return []string{"open"}
	case "linux":
		return []string{"xdg-open"}
	case "windows":
		// cmd /c start <url>  — works in both cmd.exe and PowerShell contexts.
		return []string{"cmd", "/c", "start"}
	default:
		return nil
	}
}

// OpenURL attempts to open url in the system browser using the host's runtime.GOOS.
// If the attempt fails or no command is known for the platform, the URL is
// printed to w as a clickable fallback. Always returns nil.
func OpenURL(url string, w io.Writer) error {
	return OpenURLWithGOOS(runtime.GOOS, url, w)
}

// OpenURLWithGOOS is like OpenURL but accepts an explicit goos string.
// Intended for testing without mocking runtime.GOOS.
func OpenURLWithGOOS(goos, url string, w io.Writer) error {
	cmd := CommandForGOOS(goos)
	if len(cmd) == 0 {
		return printFallback(url, w)
	}
	return OpenURLWithCommand(cmd, url, w)
}

// OpenURLWithCommand runs the given command with url appended as the last arg.
// If the command fails (binary not found, non-zero exit), url is printed to w.
// Always returns nil.
func OpenURLWithCommand(cmd []string, url string, w io.Writer) error {
	args := append(cmd[1:], url) //nolint:gocritic // intentional: cmd[0] is the binary
	c := exec.Command(cmd[0], args...)
	if err := c.Run(); err != nil {
		return printFallback(url, w)
	}
	return nil
}

func printFallback(url string, w io.Writer) error {
	fmt.Fprintf(w, "\n  Profile: %s\n", url)
	return nil
}
