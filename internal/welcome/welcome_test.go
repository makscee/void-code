package welcome_test

import (
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/welcome"
)

// Legacy sentinel shims — these functions are now no-ops but must compile.

func TestSentinelMissing(t *testing.T) {
	// SentinelExists always returns false now (banner is always shown).
	if welcome.SentinelExists("/nonexistent/path") {
		t.Fatal("SentinelExists must return false")
	}
}

func TestSentinelCreate(t *testing.T) {
	// TouchSentinel is a no-op; must not error.
	if err := welcome.TouchSentinel("/any/path"); err != nil {
		t.Fatalf("TouchSentinel: %v", err)
	}
}

func TestSentinelIdempotent(t *testing.T) {
	// TouchSentinel is a no-op; calling twice must not error.
	if err := welcome.TouchSentinel("/any/path"); err != nil {
		t.Fatalf("first Touch: %v", err)
	}
	if err := welcome.TouchSentinel("/any/path"); err != nil {
		t.Fatalf("second Touch must be idempotent: %v", err)
	}
}

func TestDefaultSentinelPath(t *testing.T) {
	// DefaultSentinelPath returns empty string now (banner always shown).
	got := welcome.DefaultSentinelPath()
	if got != "" {
		t.Errorf("DefaultSentinelPath() = %q; want empty", got)
	}
}

func TestNeedsWelcome(t *testing.T) {
	// NeedsWelcome always returns true — banner is always shown.
	if !welcome.NeedsWelcome("") {
		t.Fatal("NeedsWelcome must always return true")
	}
	if !welcome.NeedsWelcome("/some/path") {
		t.Fatal("NeedsWelcome must always return true regardless of path")
	}
}

// TestAuthState_LoggedIn ensures the AuthState struct captures state correctly.
func TestAuthState_LoggedIn(t *testing.T) {
	state := welcome.AuthState{
		LoggedIn:    true,
		Identity:    "user@example.com",
		SubDaysLeft: 14,
	}
	if !state.LoggedIn {
		t.Error("LoggedIn must be true")
	}
	if state.SubDaysLeft != 14 {
		t.Errorf("SubDaysLeft = %d, want 14", state.SubDaysLeft)
	}
}

// TestAuthState_LoggedOut ensures zero-value AuthState = logged out.
func TestAuthState_LoggedOut(t *testing.T) {
	state := welcome.AuthState{}
	if state.LoggedIn {
		t.Error("zero-value AuthState must be logged out")
	}
}

// TestAuthState_Unlimited ensures -1 represents unlimited sub.
func TestAuthState_Unlimited(t *testing.T) {
	state := welcome.AuthState{
		LoggedIn:    true,
		Identity:    "admin@example.com",
		SubDaysLeft: -1,
	}
	if state.SubDaysLeft != -1 {
		t.Errorf("SubDaysLeft = %d, want -1 (unlimited)", state.SubDaysLeft)
	}
}

// --- VCD-44: subscription warning tests ---

func TestSubscriptionWarning_Band(t *testing.T) {
	if welcome.SubscriptionWarning(0) != "" {
		t.Error("days=0 should not produce a banner warning (handled by hard block)")
	}
	if welcome.SubscriptionWarning(4) != "" {
		t.Error("days=4 should not warn")
	}
	if welcome.SubscriptionWarning(-1) != "" {
		t.Error("unlimited should not warn")
	}
	for _, d := range []int{1, 2, 3} {
		w := welcome.SubscriptionWarning(d)
		if w == "" {
			t.Errorf("days=%d should warn", d)
		}
		if !strings.Contains(w, "@makscee") {
			t.Errorf("days=%d warning %q must name @makscee", d, w)
		}
	}
}

func TestPlainBanner_ShowsWarningInBand(t *testing.T) {
	out := welcome.PlainBannerForTest(welcome.AuthState{LoggedIn: true, Identity: "x@vk.com", SubDaysLeft: 2})
	if !strings.Contains(out, "@makscee") {
		t.Errorf("plain banner for 2 days must include warning: %q", out)
	}
}

func TestPlainBanner_NoWarningAboveBand(t *testing.T) {
	out := welcome.PlainBannerForTest(welcome.AuthState{LoggedIn: true, Identity: "x@vk.com", SubDaysLeft: 10})
	if strings.Contains(out, "@makscee") {
		t.Errorf("plain banner for 10 days must NOT warn: %q", out)
	}
}
