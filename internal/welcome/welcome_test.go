package welcome_test

import (
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/provider"
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

func TestPlainBanner_ShowsIdentity(t *testing.T) {
	out := welcome.PlainBannerForTest(welcome.AuthState{LoggedIn: true, Identity: "x@vk.com"})
	if !strings.Contains(out, "x@vk.com") {
		t.Errorf("plain banner must include identity: %q", out)
	}
}

func TestPlainBanner_NotLoggedIn(t *testing.T) {
	out := welcome.PlainBannerForTest(welcome.AuthState{LoggedIn: false})
	if !strings.Contains(out, "Not logged in") {
		t.Errorf("plain banner for logged-out must say 'Not logged in': %q", out)
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

func TestPlainBanner_ShowsBalance(t *testing.T) {
	bal := 9.5
	out := welcome.PlainBannerForTest(welcome.AuthState{
		LoggedIn:   true,
		Identity:   "u@x.com",
		BalanceUsd: &bal,
	})
	if !strings.Contains(out, "$9.50 left") {
		t.Errorf("plain banner must show balance at $9.50 left: %q", out)
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

func TestPlainBanner_NilBalanceShowsDash(t *testing.T) {
	out := welcome.PlainBannerForTest(welcome.AuthState{
		LoggedIn: true,
		Identity: "u@x.com",
		// BalanceUsd nil → should show dash, no $ figure
	})
	if strings.Contains(out, "$") {
		t.Errorf("plain banner must NOT show $ when BalanceUsd is nil: %q", out)
	}
	if !strings.Contains(out, "—") {
		t.Errorf("plain banner must show '—' when BalanceUsd is nil: %q", out)
	}
}

func TestPlainBanner_NoSubDaysNoBudgetPct(t *testing.T) {
	pct := 30.0
	out := welcome.PlainBannerForTest(welcome.AuthState{
		LoggedIn:    true,
		Identity:    "u@x.com",
		SubDaysLeft: 5,
		BudgetPct:   &pct,
	})
	// VCD-56: sub-days and budget-pct lines dropped from plain banner.
	if strings.Contains(out, "days left on subscription") {
		t.Errorf("plain banner must NOT show sub-days line: %q", out)
	}
	if strings.Contains(out, "budget left") {
		t.Errorf("plain banner must NOT show budget-left line: %q", out)
	}
}

// VCD-56: menu model tests.

func TestMenu_StartItemDefault(t *testing.T) {
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true, Identity: "a@b.com"})
	if m.Cursor() != 0 {
		t.Errorf("initial cursor = %d, want 0 (Start)", m.Cursor())
	}
	if got := m.ItemLabel(0); got != "Start" {
		t.Errorf("item 0 = %q, want Start", got)
	}
}

func TestMenu_NavigationWraps(t *testing.T) {
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true})
	n := m.ItemCount() // 4 when logged in: Start, Providers, Top up, Run doctor
	if n != 4 {
		t.Fatalf("item count = %d, want 4", n)
	}
	m = m.MoveCursor(-1) // up from 0 wraps to last
	if m.Cursor() != n-1 {
		t.Errorf("cursor after up-from-0 = %d, want %d", m.Cursor(), n-1)
	}
	m = m.MoveCursor(1) // down wraps back to 0
	if m.Cursor() != 0 {
		t.Errorf("cursor after wrap-down = %d, want 0", m.Cursor())
	}
}

func TestMenu_EnterStartReturnsSpawn(t *testing.T) {
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true})
	m = m.SetCursor(0)
	if got := m.Activate(); got != welcome.SpawnClaude {
		t.Errorf("activate Start = %v, want SpawnClaude", got)
	}
}

func TestMenu_EnterDoctorReturnsRunDoctor(t *testing.T) {
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true})
	m = m.SetCursor(3) // Start(0), Providers(1), Top up(2), Run doctor(3)
	if got := m.Activate(); got != welcome.RunDoctor {
		t.Errorf("activate Run doctor = %v, want RunDoctor", got)
	}
}

func TestMenu_TopUpIsInfoNotResult(t *testing.T) {
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true})
	m = m.SetCursor(2) // Start(0), Providers(1), Top up(2), Run doctor(3)
	// Top up does not exit the program — Activate returns the in-TUI sentinel.
	if got := m.Activate(); got != welcome.ShowTopUp {
		t.Errorf("activate Top up = %v, want ShowTopUp", got)
	}
}

func TestMenu_LoggedOutSingleLoginItem(t *testing.T) {
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: false})
	if m.ItemCount() != 1 {
		t.Fatalf("logged-out item count = %d, want 1 (Login)", m.ItemCount())
	}
	m = m.SetCursor(0)
	if got := m.Activate(); got != welcome.RunLogin {
		t.Errorf("activate logged-out item = %v, want RunLogin", got)
	}
}

// VCD-56: View rendering tests.

func ptrF(v float64) *float64 { return &v }

func TestView_HeaderShowsBalanceAndIdentity(t *testing.T) {
	bal := 7.5
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true, Identity: "x@y.com", BalanceUsd: &bal})
	out := m.View()
	if !strings.Contains(out, "x@y.com") {
		t.Errorf("view missing identity:\n%s", out)
	}
	if !strings.Contains(out, "$7.50 left") {
		t.Errorf("view missing balance:\n%s", out)
	}
}

func TestView_NoSubDaysNoBudgetPct(t *testing.T) {
	bal := 7.5
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true, Identity: "x@y.com", BalanceUsd: &bal, SubDaysLeft: 5, BudgetPct: ptrF(80)})
	out := m.View()
	if strings.Contains(out, "days left on subscription") {
		t.Errorf("view still shows sub-days:\n%s", out)
	}
	if strings.Contains(out, "budget left") {
		t.Errorf("view still shows %% budget left:\n%s", out)
	}
}

func TestView_NilBalanceShowsDash(t *testing.T) {
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true, Identity: "x@y.com"}) // BalanceUsd nil
	out := m.View()
	if strings.Contains(out, "$") {
		t.Errorf("nil balance must not render a $ figure:\n%s", out)
	}
	if !strings.Contains(out, "—") {
		t.Errorf("nil balance should render dash:\n%s", out)
	}
}

func TestView_MenuListsItemsWithCursor(t *testing.T) {
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true, Identity: "x@y.com"})
	out := m.View()
	for _, it := range []string{"Start", "Top up", "Run doctor"} {
		if !strings.Contains(out, it) {
			t.Errorf("menu missing %q:\n%s", it, out)
		}
	}
}

func TestView_TopUpSubViewShowsTelegram(t *testing.T) {
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true})
	m = m.SetTopUpView()
	out := m.View()
	if !strings.Contains(out, "@makscee") {
		t.Errorf("top-up view missing @makscee instruction:\n%s", out)
	}
}

// VCD-56: clack rail layout tests.

func TestView_ClackRailStructure(t *testing.T) {
	bal := 12.4
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true, Identity: "test@void-code.dev", BalanceUsd: &bal})
	out := m.View()

	// Top cap with title
	if !strings.Contains(out, "┌") {
		t.Errorf("view missing top rail cap ┌:\n%s", out)
	}
	// Bottom cap with hint
	if !strings.Contains(out, "└") {
		t.Errorf("view missing bottom rail cap └:\n%s", out)
	}
	// Continuous rail
	if !strings.Contains(out, "│") {
		t.Errorf("view missing rail │:\n%s", out)
	}
	// Info marker ◇ (identity line)
	if !strings.Contains(out, "◇") {
		t.Errorf("view missing info marker ◇:\n%s", out)
	}
	// Prompt marker ◆ (What now?)
	if !strings.Contains(out, "◆") {
		t.Errorf("view missing prompt marker ◆:\n%s", out)
	}
	// Selected item marker ●
	if !strings.Contains(out, "●") {
		t.Errorf("view missing selected item marker ●:\n%s", out)
	}
	// Unselected item marker ○
	if !strings.Contains(out, "○") {
		t.Errorf("view missing unselected item marker ○:\n%s", out)
	}
	// void-code title
	if !strings.Contains(out, "void-code") {
		t.Errorf("view missing void-code title:\n%s", out)
	}
	// identity + balance on same ◇ line
	if !strings.Contains(out, "test@void-code.dev") {
		t.Errorf("view missing identity:\n%s", out)
	}
	if !strings.Contains(out, "$12.40 left") {
		t.Errorf("view missing balance:\n%s", out)
	}
	// What now? prompt text
	if !strings.Contains(out, "What now?") {
		t.Errorf("view missing 'What now?' prompt:\n%s", out)
	}
	// Bottom hint
	if !strings.Contains(out, "↑/↓") {
		t.Errorf("view missing navigation hint ↑/↓:\n%s", out)
	}
}

func TestView_TopUpRailStructure(t *testing.T) {
	m := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true})
	m = m.SetTopUpView()
	out := m.View()

	// Must have rail chars
	if !strings.Contains(out, "┌") {
		t.Errorf("top-up view missing ┌:\n%s", out)
	}
	if !strings.Contains(out, "◇") {
		t.Errorf("top-up view missing ◇:\n%s", out)
	}
	if !strings.Contains(out, "└") {
		t.Errorf("top-up view missing └:\n%s", out)
	}
	// Instruction text
	if !strings.Contains(out, "@makscee") {
		t.Errorf("top-up view missing @makscee:\n%s", out)
	}
}

// ─── VCD-58: delete-key integration tests ────────────────────────────────────

// TestDeleteKey_TUIRelayFallback verifies that when the TUI updateDeleteConfirm
// handler deletes the currently-active key, it calls OnSelect with Relay and
// resets the in-memory ActiveProvider to "relay".
func TestDeleteKey_TUIRelayFallback(t *testing.T) {
	var selectedProviderStr string
	var selectCalled bool

	cb := welcome.Callbacks{
		KeyNames:       []string{"alpha", "beta"},
		ActiveProvider: "key:beta", // beta is the active key
		OnDeleteKey:    func(name string) error { return nil },
		OnSelect: func(p provider.Provider) error {
			selectCalled = true
			selectedProviderStr = p.String()
			return nil
		},
	}

	// Simulate the TUI delete flow manually via the model.
	m := welcome.NewMenuModelForDeleteTest(cb)
	m = m.SimulateDeleteKey("beta", true)

	// After y confirm: ActiveProvider must be reset to relay.
	if m.ActiveProviderString() != "relay" {
		t.Errorf("after deleting active key, ActiveProvider = %q, want relay", m.ActiveProviderString())
	}
	// OnSelect must have been called with relay.
	if !selectCalled {
		t.Error("OnSelect was not called — relay fallback not triggered")
	}
	if selectedProviderStr != "relay" {
		t.Errorf("OnSelect called with provider %q, want relay", selectedProviderStr)
	}
	// beta must be removed from in-memory key list.
	keys := m.KeyNames()
	for _, k := range keys {
		if k == "beta" {
			t.Error("beta still in key list after delete")
		}
	}
}

// TestDeleteKey_NonActiveNoRelayFallback verifies that deleting a non-active key
// does NOT change the active provider.
func TestDeleteKey_NonActiveNoRelayFallback(t *testing.T) {
	var selectCalled bool
	cb := welcome.Callbacks{
		KeyNames:       []string{"alpha", "beta"},
		ActiveProvider: "key:alpha", // alpha is active, we delete beta
		OnDeleteKey:    func(name string) error { return nil },
		OnSelect: func(p provider.Provider) error {
			selectCalled = true
			return nil
		},
	}

	m := welcome.NewMenuModelForDeleteTest(cb)
	m = m.SimulateDeleteKey("beta", true)

	// Active provider unchanged — alpha still active.
	if m.ActiveProviderString() != "key:alpha" {
		t.Errorf("after deleting non-active key, ActiveProvider = %q, want key:alpha", m.ActiveProviderString())
	}
	if selectCalled {
		t.Error("OnSelect must NOT be called when deleting a non-active key")
	}
}

// VCD-56: FormatBalance helper tests.

func TestFormatBalance(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		name string
		in   *float64
		want string
	}{
		{"nil", nil, "—"},
		{"zero", f(0), "$0.00 left"},
		{"whole", f(12), "$12.00 left"},
		{"cents", f(12.4), "$12.40 left"},
		// 3.005 is stored as 3.00499… in float64 → prints $3.00 (not $3.01)
		{"rounding", f(3.005), "$3.00 left"},
		{"large", f(1234.5), "$1234.50 left"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := welcome.FormatBalance(c.in)
			if got != c.want {
				t.Errorf("FormatBalance(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
