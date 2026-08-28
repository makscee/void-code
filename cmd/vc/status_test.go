package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
)

func TestStatusUsesLiveMeNotCachedIdentityOrBudget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var response = `{"userId":"old","email":"old@example.test","pct":10,"resetAt":"2026-01-01"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(json.RawMessage(response)) }))
	defer srv.Close()
	t.Setenv("VC_AUTH_HOST", srv.URL)
	t.Setenv("VC_ACCESS_CHECK_HOST", srv.URL)
	if err := auth.Save("status-token"); err != nil {
		t.Fatal(err)
	}
	writeMeCache(srv.URL, "status-token", auth.MeResult{UserID: "old", Email: "old@example.test"}, nowForTest())
	response = `{"userId":"new","email":"new@example.test","pct":77,"resetAt":"2026-02-02"}`
	out, err := captureStdout(t, func() error { return runStatus(nil, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "new@example.test") || !strings.Contains(out, "77%") || strings.Contains(out, "old@example.test") {
		t.Fatalf("status did not use live response: %s", out)
	}
	if strings.Contains(out, "status-token") {
		t.Fatal("status leaked token")
	}
}
func TestStatusReportsLiveRejectionAndUnreachableServer(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusInternalServerError} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
			defer srv.Close()
			t.Setenv("VC_AUTH_HOST", srv.URL)
			t.Setenv("VC_ACCESS_CHECK_HOST", srv.URL)
			if err := auth.Save("private-token"); err != nil {
				t.Fatal(err)
			}
			out, err := captureStdout(t, func() error { return runStatus(nil, nil) })
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "verification failed") || strings.Contains(out, "private-token") {
				t.Fatalf("unexpected status: %s", out)
			}
		})
	}
}
func nowForTest() time.Time { return time.Now() }
