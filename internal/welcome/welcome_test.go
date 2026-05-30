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

// --- VCD-49: budget warning tests ---

func TestBudgetWarning_NilPct(t *testing.T) {
	// nil pct = no budget set → must not warn.
	if w := welcome.BudgetWarning(nil); w != "" {
		t.Errorf("BudgetWarning(nil) = %q, want empty (no budget)", w)
	}
}

func TestBudgetWarning_BelowThreshold(t *testing.T) {
	pct := 50.0
	if w := welcome.BudgetWarning(&pct); w != "" {
		t.Errorf("BudgetWarning(50) = %q, want empty", w)
	}
	pct2 := 79.9
	if w := welcome.BudgetWarning(&pct2); w != "" {
		t.Errorf("BudgetWarning(79.9) = %q, want empty", w)
	}
}

func TestBudgetWarning_WarnBand(t *testing.T) {
	for _, p := range []float64{80.0, 85.0, 95.0, 99.9} {
		pct := p
		w := welcome.BudgetWarning(&pct)
		if w == "" {
			t.Errorf("BudgetWarning(%v) = empty, want warn string", p)
		}
		if !strings.Contains(w, "@makscee") {
			t.Errorf("BudgetWarning(%v) = %q, must name @makscee", p, w)
		}
		// Warn band must NOT say "reached" — that's the block copy.
		if strings.Contains(w, "reached") {
			t.Errorf("BudgetWarning(%v) = %q, warn should not say 'reached'", p, w)
		}
		// Operator constraint 2026-05-30: no dollar values in user-facing copy.
		if strings.Contains(w, "$") {
			t.Errorf("BudgetWarning(%v) = %q, must NOT contain '$' (percentages only)", p, w)
		}
	}
}

func TestBudgetWarning_BlockAt100(t *testing.T) {
	for _, p := range []float64{100.0, 110.0, 150.0} {
		pct := p
		w := welcome.BudgetWarning(&pct)
		if !strings.Contains(w, "reached") {
			t.Errorf("BudgetWarning(%v) = %q, must say 'reached' for block copy", p, w)
		}
		if !strings.Contains(w, "@makscee") {
			t.Errorf("BudgetWarning(%v) = %q, must name @makscee", p, w)
		}
		// Operator constraint 2026-05-30: no dollar values in block copy.
		if strings.Contains(w, "$") {
			t.Errorf("BudgetWarning(%v) = %q, must NOT contain '$' (percentages only)", p, w)
		}
	}
}

func TestPlainBanner_BudgetWarn(t *testing.T) {
	pct := 85.0
	out := welcome.PlainBannerForTest(welcome.AuthState{
		LoggedIn:    true,
		Identity:    "u@x.com",
		SubDaysLeft: -1,
		BudgetPct:   &pct,
	})
	if !strings.Contains(out, "@makscee") {
		t.Errorf("plain banner must show budget warning at 85%%: %q", out)
	}
}

func TestPlainBanner_NoBudgetWhenNilPct(t *testing.T) {
	out := welcome.PlainBannerForTest(welcome.AuthState{
		LoggedIn:    true,
		Identity:    "u@x.com",
		SubDaysLeft: -1,
		BudgetPct:   nil,
	})
	// Should NOT mention "budget" at all when nil pct.
	if strings.Contains(out, "budget") {
		t.Errorf("plain banner must NOT show budget line when pct nil: %q", out)
	}
}

// --- VCD-54: version + budget-left display tests ---

func TestPlainBanner_ShowsVersion(t *testing.T) {
	out := welcome.PlainBannerForTest(welcome.AuthState{
		LoggedIn:    true,
		Identity:    "u@x.com",
		SubDaysLeft: -1,
		BudgetPct:   nil,
	})
	// Banner must contain "void-code" and some version indicator.
	if !strings.Contains(out, "void-code") {
		t.Errorf("plain banner must contain 'void-code': %q", out)
	}
}

func TestPlainBanner_BudgetLeftLine_Healthy(t *testing.T) {
	pct := 30.0 // 30% used → 70% left
	out := welcome.PlainBannerForTest(welcome.AuthState{
		LoggedIn:    true,
		Identity:    "u@x.com",
		SubDaysLeft: -1,
		BudgetPct:   &pct,
	})
	// Must show "70% budget left" (period-agnostic, not "monthly").
	if !strings.Contains(out, "budget left") {
		t.Errorf("plain banner must show 'budget left' at pct=30: %q", out)
	}
	if !strings.Contains(out, "70%") {
		t.Errorf("plain banner must show '70%%' at pct=30: %q", out)
	}
	// Must NOT say "monthly".
	if strings.Contains(out, "monthly") || strings.Contains(out, "Monthly") {
		t.Errorf("budget-left line must not say 'monthly': %q", out)
	}
}

func TestPlainBanner_BudgetLeftLine_NilHidden(t *testing.T) {
	out := welcome.PlainBannerForTest(welcome.AuthState{
		LoggedIn:    true,
		Identity:    "u@x.com",
		SubDaysLeft: -1,
		BudgetPct:   nil,
	})
	if strings.Contains(out, "budget left") {
		t.Errorf("plain banner must NOT show 'budget left' when pct is nil: %q", out)
	}
}

func TestPlainBanner_BudgetLeftLine_Warn(t *testing.T) {
	pct := 85.0 // 85% used → 15% left — still in warn territory
	out := welcome.PlainBannerForTest(welcome.AuthState{
		LoggedIn:    true,
		Identity:    "u@x.com",
		SubDaysLeft: -1,
		BudgetPct:   &pct,
	})
	// At ≥80% a warning is shown (existing behavior kept); budget-left line also shown.
	if !strings.Contains(out, "budget left") {
		t.Errorf("plain banner must show 'budget left' even at warn-band pct=85: %q", out)
	}
	if !strings.Contains(out, "15%") {
		t.Errorf("plain banner must show '15%%' at pct=85: %q", out)
	}
}
