package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
)

// The wallet as void-relay#28 sends it (void-board#234): kopecks, paidUntil,
// the tariff's rouble prices, no dollar field.
func fetchWallet(t *testing.T, wallet string) *auth.Wallet {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"userId":"u-1","email":"person@example.test","wallet":` + wallet + `}`))
	}))
	defer srv.Close()
	me, err := auth.FetchMe(srv.URL, "tok", nil)
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	return me.Wallet
}

func TestFetchMeReadsRoubleWallet(t *testing.T) {
	w := fetchWallet(t, `{"balanceKopecks":100000,"paidUntil":"2026-10-25T17:42:24.673Z","tariff":{"tier":"t2","weekPriceKopecks":300000,"packPriceKopecks":1000000},"todayPaid":true,"fundedDays":28,"chargeRequired":false,"periodEndsAt":"2026-10-04T17:42:24.673Z"}`)
	if w == nil {
		t.Fatal("Wallet = nil, want the rouble wallet")
	}
	if w.BalanceKopecks == nil || *w.BalanceKopecks != 100000 {
		t.Errorf("BalanceKopecks = %v, want 100000", w.BalanceKopecks)
	}
	if w.BalanceUsd != nil {
		t.Errorf("BalanceUsd = %v, want nil: none was sent", *w.BalanceUsd)
	}
	want := time.Date(2026, 10, 25, 17, 42, 24, 673000000, time.UTC)
	if w.PaidUntil == nil || !w.PaidUntil.Equal(want) {
		t.Errorf("PaidUntil = %v, want %v", w.PaidUntil, want)
	}
	if w.Tariff == nil || w.Tariff.Tier != "t2" || w.Tariff.WeekPriceKopecks == nil || *w.Tariff.WeekPriceKopecks != 300000 || w.Tariff.PackPriceKopecks == nil || *w.Tariff.PackPriceKopecks != 1000000 {
		t.Errorf("Tariff = %+v, want t2 at 300000 / 1000000 kopecks", w.Tariff)
	}
	if w.ChargeRequired == nil || *w.ChargeRequired {
		t.Errorf("ChargeRequired = %v, want false", w.ChargeRequired)
	}
}

func TestFetchMeRoubleWalletEdges(t *testing.T) {
	for _, tc := range []struct {
		name, wallet string
		ok           bool
		paidUntil    bool
	}{
		{"no paid time", `{"balanceKopecks":0,"paidUntil":null,"tariff":null}`, true, false},
		{"unreadable date costs only the date", `{"balanceKopecks":5,"paidUntil":"4 Oct"}`, true, false},
		{"fraction of a kopeck", `{"balanceKopecks":1.5,"paidUntil":null}`, false, false},
		{"kopecks as a string", `{"balanceKopecks":"100","paidUntil":null}`, false, false},
		{"date of the wrong type", `{"balanceKopecks":100,"paidUntil":7}`, false, false},
		{"fractional week price", `{"balanceKopecks":100,"tariff":{"tier":"t1","weekPriceKopecks":1.5}}`, false, false},
		{"no balance at all", `{"paidUntil":"2026-10-25T17:42:24Z","tariff":{"tier":"t1"}}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := fetchWallet(t, tc.wallet)
			if (w != nil) != tc.ok {
				got, _ := json.Marshal(w)
				t.Fatalf("Wallet = %s, want read=%v", got, tc.ok)
			}
			if w != nil && (w.PaidUntil != nil) != tc.paidUntil {
				t.Errorf("PaidUntil = %v, want set=%v", w.PaidUntil, tc.paidUntil)
			}
		})
	}
}
