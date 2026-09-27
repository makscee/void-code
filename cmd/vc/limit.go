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

// formatLimit renders a limit as `limit 42% used, resets in 3 days`, without
// the reset when the server sent none, and "" for no limit.
func formatLimit(l *auth.Limit, now time.Time) string {
	if l == nil {
		return ""
	}
	text := fmt.Sprintf("limit %d%% used", limitPct(l))
	if l.ResetAt != nil {
		text += ", resets " + resetsIn(l.ResetAt.Sub(now))
	}
	return text
}

// resetsIn spells the wait until the reset: whole days from a day on, whole
// hours (rounded up) below that, and "soon" once it is due.
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

// formatAccount is the line `vc status` prints after "balance:", the desktop
// shows as walletText and the welcome screen shows next to the identity: the
// wallet, then the limit, each only when the server sent it. "" for neither.
func formatAccount(me auth.MeResult, now time.Time) string {
	var parts []string
	for _, part := range []string{formatWallet(me.Wallet), formatLimit(me.Limit, now)} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " · ")
}

// limitLaunchNotice warns a launch once the limit is limitWarnPct% used.
func limitLaunchNotice(l *auth.Limit) string {
	if l == nil || limitPct(l) < limitWarnPct {
		return ""
	}
	return fmt.Sprintf("Weekly limit %d%% used — upgrade: %s", limitPct(l), browser.PayURL)
}

// launchNotice is everything a launch tells the person: the wallet's notice,
// then the limit's, one per line, or "" for nothing.
func launchNotice(me auth.MeResult) string {
	var lines []string
	for _, line := range []string{walletLaunchNotice(me.Wallet), limitLaunchNotice(me.Limit)} {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
