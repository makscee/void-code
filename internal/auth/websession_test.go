package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/makscee/void-code/internal/auth"
)

func TestMintWebSession_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/vc/web-session" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok123" {
			t.Fatalf("auth header = %q", got)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"url":"https://auth.makscee.ru/profile?ml=ML","token":"ML","expiresAt":"2026-06-04T00:00:00Z"}`))
	}))
	defer srv.Close()

	ws, err := auth.MintWebSession(srv.URL, "tok123", srv.Client())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ws.Token != "ML" {
		t.Errorf("token = %q, want ML", ws.Token)
	}
}

func TestMintWebSession_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()
	if _, err := auth.MintWebSession(srv.URL, "bad", srv.Client()); err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestMintWebSession_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()
	if _, err := auth.MintWebSession(srv.URL, "tok", srv.Client()); err == nil {
		t.Fatal("expected error on 503")
	}
}
