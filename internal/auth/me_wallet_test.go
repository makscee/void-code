package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Wallet and tariff on /v1/vc/me — spec 2026-09-23-client-wallet-days, "Клиент".
//
// Relay answers with
//
//	"wallet": {
//	  "balanceUsd": 18,
//	  "tariff": { "tier": "t1", "monthlyPriceUsd": 60, "dailyRateUsd": 2 } | null,
//	  "todayPaid":  true | false | null,
//	  "fundedDays": 9 | null
//	}
//
// and no longer sends pct / resetAt. The seam these tests need, and the only
// new names in this package:
//
//	type Wallet struct {
//		BalanceUsd float64
//		Tariff     *Tariff // nil: no tariff assigned
//		TodayPaid  *bool   // nil: no tariff (Keys answers null)
//		FundedDays *int    // nil: no tariff
//	}
//	type Tariff struct {
//		Tier            string  // as sent by the server ("t1"), not reformatted
//		MonthlyPriceUsd float64
//		DailyRateUsd    float64
//	}
//	MeResult.Wallet *Wallet // nil: the server sent no usable wallet
//
// TodayPaid is a pointer on purpose: `false` blocks a launch and `null` does
// not, so the two must not collapse into one zero value.

func fetchMeBody(t *testing.T, body string) (MeResult, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	return FetchMe(srv.URL, "tok", srv.Client())
}

func TestFetchMe_WalletWithTariff(t *testing.T) {
	res, err := fetchMeBody(t, `{"subject_id":"subj-1","wallet":{"balanceUsd":18,"tariff":{"tier":"t1","monthlyPriceUsd":60,"dailyRateUsd":2},"todayPaid":true,"fundedDays":9}}`)
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.UserID != "subj-1" {
		t.Errorf("UserID = %q, want subj-1 — the wallet must not disturb identity", res.UserID)
	}
	w := res.Wallet
	if w == nil {
		t.Fatal("Wallet = nil, want the wallet the server sent")
	}
	if w.BalanceUsd != 18 {
		t.Errorf("BalanceUsd = %v, want 18", w.BalanceUsd)
	}
	if w.Tariff == nil {
		t.Fatal("Tariff = nil, want t1")
	}
	if w.Tariff.Tier != "t1" {
		t.Errorf("Tier = %q, want %q as sent — display formatting belongs to the printer, not the parser", w.Tariff.Tier, "t1")
	}
	if w.Tariff.MonthlyPriceUsd != 60 || w.Tariff.DailyRateUsd != 2 {
		t.Errorf("Tariff = %+v, want monthly 60 / daily 2", *w.Tariff)
	}
	if w.TodayPaid == nil || *w.TodayPaid != true {
		t.Errorf("TodayPaid = %v, want true", w.TodayPaid)
	}
	if w.FundedDays == nil || *w.FundedDays != 9 {
		t.Errorf("FundedDays = %v, want 9", w.FundedDays)
	}
}

// false is the one value that blocks a launch. It must survive the parse as
// false, not as "absent".
func TestFetchMe_WalletTodayUnpaidIsFalseNotNil(t *testing.T) {
	res, err := fetchMeBody(t, `{"subject_id":"subj-1","wallet":{"balanceUsd":1.5,"tariff":{"tier":"t3","monthlyPriceUsd":230,"dailyRateUsd":7.67},"todayPaid":false,"fundedDays":0}}`)
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.Wallet == nil {
		t.Fatal("Wallet = nil")
	}
	if res.Wallet.TodayPaid == nil {
		t.Fatal("TodayPaid = nil, want false — an unpaid day read as 'unknown' would never block")
	}
	if *res.Wallet.TodayPaid {
		t.Error("TodayPaid = true, want false")
	}
	if res.Wallet.FundedDays == nil || *res.Wallet.FundedDays != 0 {
		t.Errorf("FundedDays = %v, want 0 (present, not absent)", res.Wallet.FundedDays)
	}
	if res.Wallet.Tariff == nil || res.Wallet.Tariff.DailyRateUsd != 7.67 {
		t.Errorf("Tariff = %+v, want t3 at 7.67/day", res.Wallet.Tariff)
	}
}

// No tariff: Keys answers null for everything tariff-derived. The balance is
// still a balance.
func TestFetchMe_WalletWithoutTariff(t *testing.T) {
	res, err := fetchMeBody(t, `{"subject_id":"subj-1","wallet":{"balanceUsd":18,"tariff":null,"todayPaid":null,"fundedDays":null}}`)
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	w := res.Wallet
	if w == nil {
		t.Fatal("Wallet = nil, want a tariff-less wallet")
	}
	if w.BalanceUsd != 18 {
		t.Errorf("BalanceUsd = %v, want 18", w.BalanceUsd)
	}
	if w.Tariff != nil {
		t.Errorf("Tariff = %+v, want nil", *w.Tariff)
	}
	if w.TodayPaid != nil {
		t.Errorf("TodayPaid = %v, want nil — null is not false", *w.TodayPaid)
	}
	if w.FundedDays != nil {
		t.Errorf("FundedDays = %v, want nil", *w.FundedDays)
	}
}

// Old servers: no wallet at all, or the retired pct/resetAt budget and the
// void-auth era top-level balanceUsd. None of that is a wallet.
func TestFetchMe_NoWalletFromOldServers(t *testing.T) {
	for name, body := range map[string]string{
		"absent":                  `{"subject_id":"subj-1"}`,
		"null":                    `{"subject_id":"subj-1","wallet":null}`,
		"pct budget only":         `{"subject_id":"subj-1","pct":100,"resetAt":"2026-10-01T00:00:00Z"}`,
		"top-level balance":       `{"userId":"u-1","email":"u@example.test","pct":null,"balanceUsd":12.4,"resetAt":""}`,
		"retired fields together": `{"userId":"u-1","pct":85,"resetAt":"2026-10-01T00:00:00Z","balanceUsd":3.5,"subDaysLeft":36500}`,
	} {
		t.Run(name, func(t *testing.T) {
			res, err := fetchMeBody(t, body)
			if err != nil {
				t.Fatalf("FetchMe: %v — an old server's answer must still sign the user in", err)
			}
			if res.UserID == "" {
				t.Error("identity lost")
			}
			if res.Wallet != nil {
				t.Errorf("Wallet = %+v, want nil — only a `wallet` object is a wallet", *res.Wallet)
			}
		})
	}
}

// A wallet the client cannot read is no wallet — and never an auth failure.
// If a malformed wallet failed the whole decode, FetchMe would error, authGate
// would answer "Session verification unavailable", and every launch would be
// refused over a display field. Strict means "not coerced": "false" as a
// string is not false, "18" is not 18.
func TestFetchMe_MalformedWalletIsNilAndIdentitySurvives(t *testing.T) {
	for name, wallet := range map[string]string{
		"not an object":         `"yes"`,
		"array":                 `[18]`,
		"balance as string":     `{"balanceUsd":"18","tariff":null,"todayPaid":null,"fundedDays":null}`,
		"balance missing":       `{"tariff":null,"todayPaid":null,"fundedDays":null}`,
		"balance null":          `{"balanceUsd":null,"tariff":null,"todayPaid":null,"fundedDays":null}`,
		"todayPaid as string":   `{"balanceUsd":18,"tariff":{"tier":"t1","monthlyPriceUsd":60,"dailyRateUsd":2},"todayPaid":"false","fundedDays":0}`,
		"fundedDays as string":  `{"balanceUsd":18,"tariff":{"tier":"t1","monthlyPriceUsd":60,"dailyRateUsd":2},"todayPaid":true,"fundedDays":"9"}`,
		"tariff not an object":  `{"balanceUsd":18,"tariff":"t1","todayPaid":true,"fundedDays":9}`,
		"tier not a string":     `{"balanceUsd":18,"tariff":{"tier":1,"monthlyPriceUsd":60,"dailyRateUsd":2},"todayPaid":true,"fundedDays":9}`,
		"daily rate as string":  `{"balanceUsd":18,"tariff":{"tier":"t1","monthlyPriceUsd":60,"dailyRateUsd":"2"},"todayPaid":true,"fundedDays":9}`,
		"monthly price as bool": `{"balanceUsd":18,"tariff":{"tier":"t1","monthlyPriceUsd":true,"dailyRateUsd":2},"todayPaid":true,"fundedDays":9}`,
	} {
		t.Run(name, func(t *testing.T) {
			res, err := fetchMeBody(t, `{"subject_id":"subj-1","email":"person@example.test","wallet":`+wallet+`}`)
			if err != nil {
				t.Fatalf("FetchMe: %v — a wallet the client cannot read must not become a failed sign-in", err)
			}
			if res.UserID != "subj-1" || res.Email != "person@example.test" {
				t.Errorf("identity = %q/%q, want subj-1/person@example.test", res.UserID, res.Email)
			}
			if res.Wallet != nil {
				t.Errorf("Wallet = %+v, want nil — a malformed wallet must not be half-read", *res.Wallet)
			}
		})
	}
}

// Strict about types, not about the key set: the wallet will grow (weekly
// quota, v2 wallet) before every client is updated.
func TestFetchMe_WalletToleratesUnknownFields(t *testing.T) {
	res, err := fetchMeBody(t, `{"subject_id":"subj-1","wallet":{"balanceUsd":18,"currency":"USD","tariff":{"tier":"t2","monthlyPriceUsd":120,"dailyRateUsd":4,"label":"Team"},"todayPaid":true,"fundedDays":4,"weeklyQuota":{"x":1}}}`)
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.Wallet == nil || res.Wallet.Tariff == nil || res.Wallet.Tariff.Tier != "t2" || res.Wallet.FundedDays == nil || *res.Wallet.FundedDays != 4 {
		t.Fatalf("Wallet = %+v, want the known fields read despite unknown ones", res.Wallet)
	}
}

// A refusal is not a session; a wallet in the refusal body is nobody's wallet.
func TestFetchMe_AccessNotGrantedCarriesNoWallet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"budget_exceeded","subject_id":"u-1","wallet":{"balanceUsd":1,"tariff":{"tier":"t1","monthlyPriceUsd":60,"dailyRateUsd":2},"todayPaid":false,"fundedDays":0}}`))
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if !errors.Is(err, ErrAccessNotGranted) {
		t.Fatalf("err = %v, want ErrAccessNotGranted", err)
	}
	if res.Wallet != nil {
		t.Errorf("Wallet = %+v, want nil — nothing in a refusal payload is verified state", *res.Wallet)
	}
}
