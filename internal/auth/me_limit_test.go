package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The weekly limit (void-board#224) is read from its own top-level "limit"
// object, apart from the wallet: a wallet vc cannot read keeps the limit, a
// limit vc cannot read keeps the wallet, and an older Relay with no "limit"
// leaves both as they were.
func fetchBody(t *testing.T, body string) MeResult {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	me, err := FetchMe(srv.URL, "tok", nil)
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	return me
}

const limitWallet = `"wallet":{"balanceUsd":18,"tariff":{"tier":"t1","monthlyPriceUsd":60,"dailyRateUsd":2},"todayPaid":true,"fundedDays":9}`

func TestFetchMeReadsTheLimit(t *testing.T) {
	me := fetchBody(t, `{"userId":"u-1","limit":{"pct":42.5,"resetAt":"2026-10-01T00:00:00.000Z"},`+limitWallet+`}`)
	if me.Limit == nil || me.Limit.Pct != 42.5 {
		t.Fatalf("Limit = %+v, want pct 42.5", me.Limit)
	}
	want := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if me.Limit.ResetAt == nil || !me.Limit.ResetAt.Equal(want) {
		t.Errorf("ResetAt = %v, want %v", me.Limit.ResetAt, want)
	}
	if me.Wallet == nil {
		t.Error("the wallet beside the limit was dropped")
	}
}

func TestFetchMeLimitSurvivesAWalletItCannotRead(t *testing.T) {
	for _, tariff := range []string{
		`{"tier":"t1","dailyRateUsd":2}`,     // no monthlyPriceUsd
		`{"tier":"t1","monthlyPriceUsd":60}`, // no dailyRateUsd
	} {
		me := fetchBody(t, `{"userId":"u-1","limit":{"pct":85,"resetAt":"2026-10-01T00:00:00Z"},"wallet":{"balanceUsd":18,"tariff":`+tariff+`}}`)
		if me.Wallet != nil {
			t.Errorf("tariff %s: Wallet = %+v, want nil (unchanged all-or-nothing rule)", tariff, me.Wallet)
		}
		if me.Limit == nil || me.Limit.Pct != 85 {
			t.Errorf("tariff %s: Limit = %+v, want pct 85 — the limit went with the wallet", tariff, me.Limit)
		}
	}
}

func TestFetchMeLimitAbsentOrUnreadable(t *testing.T) {
	for _, tc := range []struct{ name, member string }{
		{"absent (older Relay)", ``},
		{"null", `"limit":null,`},
		{"pct missing", `"limit":{"resetAt":"2026-10-01T00:00:00Z"},`},
		{"pct as a string", `"limit":{"pct":"85"},`},
		{"not an object", `"limit":85,`},
		{"the retired top-level pct", `"pct":85,"resetAt":"2026-10-01T00:00:00Z",`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			me := fetchBody(t, `{"userId":"u-1",`+tc.member+limitWallet+`}`)
			if me.Limit != nil {
				t.Errorf("Limit = %+v, want nil", me.Limit)
			}
			if me.Wallet == nil {
				t.Error("an unreadable limit took the wallet with it")
			}
		})
	}
}

func TestFetchMeLimitWithoutAReadableReset(t *testing.T) {
	for _, reset := range []string{``, `,"resetAt":null`, `,"resetAt":"next week"`, `,"resetAt":1790000000`} {
		me := fetchBody(t, `{"userId":"u-1","limit":{"pct":42`+reset+`}}`)
		if me.Limit == nil || me.Limit.Pct != 42 || me.Limit.ResetAt != nil {
			t.Errorf("resetAt member %q: Limit = %+v, want pct 42 and no reset", reset, me.Limit)
		}
	}
}
