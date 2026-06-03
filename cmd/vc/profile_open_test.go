package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOpenProfile_MintSuccessOpensRedeemURL verifies that when the mint endpoint
// returns a valid token, openProfile opens the /api/profile/ml redeem URL (not
// the bare ProfileURL and not the broken auth.makscee.ru URL).
func TestOpenProfile_MintSuccessOpensRedeemURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"url":"https://auth.makscee.ru/profile?ml=ML","token":"ML","expiresAt":"2026-06-04T00:00:00Z"}`))
	}))
	defer srv.Close()

	var opened string
	opener := func(u string) { opened = u }
	openProfile(srv.URL, "tok123", srv.Client(), opener)

	if want := "https://profile.makscee.ru/api/profile/ml?ml=ML"; opened != want {
		t.Errorf("opened = %q, want redeem URL %q", opened, want)
	}
}

// TestOpenProfile_NoTokenFallsBackToBareURL verifies that with an empty token
// (not logged in) openProfile falls back to the bare ProfileURL without error.
func TestOpenProfile_NoTokenFallsBackToBareURL(t *testing.T) {
	var opened string
	opener := func(u string) { opened = u }
	// Empty token → never calls server, falls back to bare ProfileURL.
	openProfile("https://unused.example", "", http.DefaultClient, opener)

	if !strings.HasPrefix(opened, "https://profile.makscee.ru/profile") {
		t.Errorf("opened = %q, want bare ProfileURL fallback", opened)
	}
}

// TestOpenProfile_MintFailureFallsBackToBareURL verifies that when the mint
// endpoint returns a non-200, openProfile falls back to the bare ProfileURL.
func TestOpenProfile_MintFailureFallsBackToBareURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	var opened string
	opener := func(u string) { opened = u }
	openProfile(srv.URL, "tok123", srv.Client(), opener)

	if want := "https://profile.makscee.ru/profile"; opened != want {
		t.Errorf("opened = %q, want bare ProfileURL on mint failure", opened)
	}
}
