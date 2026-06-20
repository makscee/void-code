package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchProvidersHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/vc/providers" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok123" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"providers":[{"id":"deepseek","name":"DeepSeek","type":"universal"},{"id":"plat-2","name":"Platform 2","type":"anthropic-oauth"}]}`))
	}))
	defer srv.Close()

	got, err := FetchProviders(srv.URL, "tok123", srv.Client())
	if err != nil {
		t.Fatalf("FetchProviders: %v", err)
	}
	if len(got) != 2 || got[0].ID != "deepseek" || got[1].ID != "plat-2" {
		t.Fatalf("providers = %+v", got)
	}
	if got[0].Name != "DeepSeek" || got[1].Name != "Platform 2" {
		t.Fatalf("fields = %+v", got)
	}
}

func TestFetchProvidersEmptyDegradesToEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"providers":[]}`))
	}))
	defer srv.Close()
	got, err := FetchProviders(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

func TestFetchProvidersUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := FetchProviders(srv.URL, "tok", srv.Client()); err != ErrNotLoggedIn {
		t.Fatalf("err = %v, want ErrNotLoggedIn", err)
	}
}
