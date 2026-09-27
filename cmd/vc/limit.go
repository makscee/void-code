package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/browser"
)

// The weekly limit (void-board#224): how much of it is used, as a share and
// never in money, on the same line as the wallet and apart from it — a
// wallet the client cannot read leaves the limit line standing, and an older
// Relay that sends no limit leaves the wallet line exactly as it was.

// limitWarnPct is the share used at or above which a launch is warned.
const limitWarnPct = 80

// limitPct is the share shown, in whole percent: floored, so the number on
// screen reaches 80 exactly when the warning starts, and kept within 0..100.
func limitPct(l *auth.Limit) int {
	return int(math.Floor(min(max(l.Pct, 0), 100)))
}

// formatLimit renders a limit as `лимит использован на 42%, сброс через 3 дня`,
// without the reset when the server sent none, and "" for no limit. The line
// is Russian, like the rest of the wallet line (void-board#234).
func formatLimit(l *auth.Limit, now time.Time) string {
	if l == nil {
		return ""
	}
	text := fmt.Sprintf("лимит использован на %d%%", limitPct(l))
	if l.ResetAt != nil {
		text += ", сброс " + ruResetsIn(l.ResetAt.Sub(now))
	}
	return text
}

// ruResetsIn is resetsIn in Russian, for the status line: `через 3 дня`,
// `через 5 часов`, `скоро`.
func ruResetsIn(d time.Duration) string {
	switch {
	case d <= 0:
		return "скоро"
	case d >= 24*time.Hour:
		return "через " + ruPlural(int(d/(24*time.Hour)), "день", "дня", "дней")
	default:
		return "через " + ruPlural(int((d+time.Hour-1)/time.Hour), "час", "часа", "часов")
	}
}

// ruPlural picks the Russian form for n: 1, 21 день; 2–4, 22 дня; 5–20, 11–14 дней.
func ruPlural(n int, one, few, many string) string {
	form := many
	switch m10, m100 := n%10, n%100; {
	case m10 == 1 && m100 != 11:
		form = one
	case m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14):
		form = few
	}
	return fmt.Sprintf("%d %s", n, form)
}

// resetsIn spells the wait until the reset for a launch notice: whole days
// from a day on, whole hours (rounded up) below that, and "soon" once it is due.
func resetsIn(d time.Duration) string {
	switch {
	case d <= 0:
		return "soon"
	case d >= 24*time.Hour:
		return "in " + plural(int(d/(24*time.Hour)), "day")
	default:
		return "in " + plural(int((d+time.Hour-1)/time.Hour), "hour")
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// formatAccount is the line `vc status` prints after "plan:", the desktop
// shows as walletText and the welcome screen shows next to the identity: the
// wallet, then the limit, each only when the server sent it. "" for neither.
func formatAccount(me auth.MeResult, now time.Time) string {
	var parts []string
	for _, part := range []string{formatWallet(me.Wallet, now), formatLimit(me.Limit, now)} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " · ")
}

// limitTopTier is the highest tier: nothing to upgrade to, so its warning
// says when the limit resets instead of linking the pay page.
const limitTopTier = "t3"

// limitLaunchNotice warns a launch once the limit is limitWarnPct% used:
// `Weekly limit 85% used — upgrade: <pay page>`, or on the top tier
// `Weekly limit 85% used — resets in 3 days` (just the share when no reset
// was sent). A wallet vc cannot read, or no tariff, keeps the link.
func limitLaunchNotice(me auth.MeResult, now time.Time) string {
	l := me.Limit
	if l == nil || limitPct(l) < limitWarnPct {
		return ""
	}
	text := fmt.Sprintf("Weekly limit %d%% used", limitPct(l))
	if me.Wallet == nil || me.Wallet.Tariff == nil || !strings.EqualFold(me.Wallet.Tariff.Tier, limitTopTier) {
		return text + " — upgrade: " + browser.PayURL
	}
	if l.ResetAt != nil {
		text += " — resets " + resetsIn(l.ResetAt.Sub(now))
	}
	return text
}

// launchNotice is everything a launch tells the person: the wallet's notice,
// then the limit's, one per line, or "" for nothing.
func launchNotice(me auth.MeResult, now time.Time) string {
	var lines []string
	for _, line := range []string{walletLaunchNotice(me.Wallet), limitLaunchNotice(me, now)} {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
