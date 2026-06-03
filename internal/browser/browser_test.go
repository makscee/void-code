package browser_test

import (
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/browser"
)

// TestGOOSCommandMap verifies the GOOS → open-command mapping is correct for
// all three supported platforms. The command must exist in the returned slice
// so `exec.Command(cmd[0], append(cmd[1:], url)...)` is the correct call.
func TestGOOSCommandMap(t *testing.T) {
	cases := []struct {
		goos string
		want string // expected first element (the binary)
	}{
		{"darwin", "open"},
		{"linux", "xdg-open"},
		{"windows", "cmd"},
	}
	for _, tc := range cases {
		cmd := browser.CommandForGOOS(tc.goos)
		if len(cmd) == 0 {
			t.Errorf("GOOS=%q: got empty command slice", tc.goos)
			continue
		}
		if cmd[0] != tc.want {
			t.Errorf("GOOS=%q: got cmd[0]=%q, want %q", tc.goos, cmd[0], tc.want)
		}
	}
}

// TestWindowsCommandArgs verifies that the windows open command includes the
// /c and start args so that `cmd /c start <url>` is the effective shell call.
func TestWindowsCommandArgs(t *testing.T) {
	cmd := browser.CommandForGOOS("windows")
	joined := strings.Join(cmd, " ")
	for _, want := range []string{"cmd", "/c", "start"} {
		if !strings.Contains(joined, want) {
			t.Errorf("windows command %q missing %q", joined, want)
		}
	}
}

// TestUnknownGOOSFallback verifies that an unknown GOOS returns a nil/empty
// command (caller prints the URL instead of attempting exec).
func TestUnknownGOOSFallback(t *testing.T) {
	cmd := browser.CommandForGOOS("plan9")
	if len(cmd) != 0 {
		t.Errorf("unknown GOOS: expected empty cmd, got %v", cmd)
	}
}

// TestOpenURLFallbackOnError verifies that when the open command fails (or
// there is no command for the GOOS), OpenURL writes the URL to the provided
// writer and returns nil (not an error — URL-print is not a failure).
func TestOpenURLFallbackOnError(t *testing.T) {
	var out strings.Builder

	// Force fallback by passing a GOOS whose binary will not be found.
	err := browser.OpenURLWithGOOS("plan9", "https://profile.makscee.ru/profile", &out)
	if err != nil {
		t.Errorf("OpenURLWithGOOS fallback must return nil, got: %v", err)
	}
	if !strings.Contains(out.String(), "https://profile.makscee.ru/profile") {
		t.Errorf("URL not printed on fallback; got: %q", out.String())
	}
}

// TestOpenURLFallbackOnCommandFailure verifies that if CommandForGOOS returns
// a command but exec fails (e.g., binary does not exist in test env), the URL
// is printed and nil is returned (not the exec error).
func TestOpenURLFallbackOnCommandFailure(t *testing.T) {
	var out strings.Builder
	// "_no_such_binary_" is guaranteed not to be in $PATH.
	err := browser.OpenURLWithCommand([]string{"_no_such_binary_"}, "https://profile.makscee.ru/profile", &out)
	if err != nil {
		t.Errorf("OpenURLWithCommand fallback must return nil, got: %v", err)
	}
	if !strings.Contains(out.String(), "https://profile.makscee.ru/profile") {
		t.Errorf("URL not printed on command failure; got: %q", out.String())
	}
}

// TestProfileURL verifies the canonical profile URL constant is set correctly.
func TestProfileURL(t *testing.T) {
	const want = "https://profile.makscee.ru/profile"
	if browser.ProfileURL != want {
		t.Errorf("ProfileURL = %q, want %q", browser.ProfileURL, want)
	}
}

// TestRedeemURL verifies that RedeemURL derives the correct magic-link redeem URL.
// VCD-80: vc builds this URL itself because the void-auth server emits a broken
// URL (auth.makscee.ru/profile?ml=…, 404) — the live redeem handler is on
// profile.makscee.ru/api/profile/ml.
func TestRedeemURL(t *testing.T) {
	got := browser.RedeemURL("ABC123")
	want := "https://profile.makscee.ru/api/profile/ml?ml=ABC123"
	if got != want {
		t.Errorf("RedeemURL = %q, want %q", got, want)
	}
}

// TestRedeemURL_SpecialChars verifies that special characters in the token are
// percent-encoded so the URL remains valid.
func TestRedeemURL_SpecialChars(t *testing.T) {
	got := browser.RedeemURL("tok+en/special=val")
	// url.QueryEscape encodes + → %2B, / → %2F, = → %3D
	want := "https://profile.makscee.ru/api/profile/ml?ml=tok%2Ben%2Fspecial%3Dval"
	if got != want {
		t.Errorf("RedeemURL special chars = %q, want %q", got, want)
	}
}
