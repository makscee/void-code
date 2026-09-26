package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const admissionMeBody = `{"userId":"u1","email":"u@example.com"}`

// A /me that takes 3s is a slow network, not a refusal: the launch client
// (authProbeTimeout, as runSpawn, the preflight and the desktop gate build it)
// waits it out. v0.2.60 allowed 2s here and refused the launch.
func TestAuthGate_SlowAnswerWithinProbeTimeoutIsAdmitted(t *testing.T) {
	withTempHome(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(3 * time.Second)
		_, _ = w.Write([]byte(admissionMeBody))
	}))
	defer srv.Close()

	me, reached, err := authGate("t", srv.URL, &http.Client{Timeout: authProbeTimeout})
	if err != nil || !reached || me.Email != "u@example.com" {
		t.Fatalf("me=%+v reached=%v err=%v, want admitted", me, reached, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", hits.Load())
	}
}

// A network error on the first try is asked once more, and the answer admits.
func TestAuthGate_NetworkErrorThenAnswerIsAdmitted(t *testing.T) {
	withTempHome(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close() // the client sees a dropped connection, not a status
			return
		}
		_, _ = w.Write([]byte(admissionMeBody))
	}))
	defer srv.Close()

	_, reached, err := authGate("t", srv.URL, &http.Client{Timeout: authProbeTimeout})
	if err != nil || !reached {
		t.Fatalf("reached=%v err=%v, want admitted on the retry", reached, err)
	}
	if hits.Load() != 2 {
		t.Fatalf("hits = %d, want 2", hits.Load())
	}
}

// A gateway status (502, 503, 504) means the auth service was not reached, so
// it is asked once more like a network error, and the answer admits.
func TestAuthGate_GatewayStatusThenAnswerIsAdmitted(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			withTempHome(t)
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if hits.Add(1) == 1 {
					w.WriteHeader(status)
					return
				}
				_, _ = w.Write([]byte(admissionMeBody))
			}))
			defer srv.Close()

			_, reached, err := authGate("t", srv.URL, &http.Client{Timeout: authProbeTimeout})
			if err != nil || !reached {
				t.Fatalf("reached=%v err=%v, want admitted on the retry", reached, err)
			}
			if hits.Load() != 2 {
				t.Fatalf("hits = %d, want 2", hits.Load())
			}
		})
	}
}

// A gateway status on both tries is still "unavailable", after exactly two.
func TestAuthGate_GatewayStatusTwiceIsUnavailable(t *testing.T) {
	withTempHome(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	_, reached, err := authGate("t", srv.URL, &http.Client{Timeout: authProbeTimeout})
	if err == nil || reached || !strings.Contains(err.Error(), "Session verification unavailable") || !strings.Contains(err.Error(), "status 502") {
		t.Fatalf("reached=%v err=%v, want unavailable with status 502", reached, err)
	}
	if hits.Load() != 2 {
		t.Fatalf("hits = %d, want 2", hits.Load())
	}
}

// A status that is the auth service's own answer is not asked again: 401 and
// 402 are verdicts, and a 500 came from the service itself.
func TestAuthGate_StatusAnswersAreNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			withTempHome(t)
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(status)
			}))
			defer srv.Close()

			if _, reached, err := authGate("t", srv.URL, &http.Client{Timeout: authProbeTimeout}); err == nil || reached {
				t.Fatalf("reached=%v err=%v, want refused", reached, err)
			}
			if hits.Load() != 1 {
				t.Fatalf("hits = %d, want 1 (no retry)", hits.Load())
			}
		})
	}
}

// A /me that never answers is tried twice and refused within the admission
// bound, however long the caller's client would wait. The bound is shrunk so
// the test does not sit out the real 10s.
func TestAuthGate_NoAnswerFailsWithinBound(t *testing.T) {
	withTempHome(t)
	saved := authAdmissionBound
	authAdmissionBound = 600 * time.Millisecond
	t.Cleanup(func() { authAdmissionBound = saved })

	var hits atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	for _, tc := range []struct {
		name     string
		client   *http.Client
		wantHits int32
	}{
		// 400ms, then the 200ms left of the bound.
		{"probe timeout under the bound", &http.Client{Timeout: 400 * time.Millisecond}, 2},
		// The bound itself is the first attempt's timeout; nothing is left to retry with.
		{"client with no timeout", &http.Client{}, 1},
	} {
		hits.Store(0)
		start := time.Now()
		_, reached, err := authGate("t", srv.URL, tc.client)
		elapsed := time.Since(start)
		if err == nil || reached || !strings.Contains(err.Error(), "Session verification unavailable") {
			t.Fatalf("%s: reached=%v err=%v, want unavailable", tc.name, reached, err)
		}
		if elapsed > authAdmissionBound+300*time.Millisecond {
			t.Fatalf("%s: took %v, want within the %v bound", tc.name, elapsed, authAdmissionBound)
		}
		if hits.Load() != tc.wantHits {
			t.Fatalf("%s: hits = %d, want %d", tc.name, hits.Load(), tc.wantHits)
		}
	}
}
