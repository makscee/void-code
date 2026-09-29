package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/welcome"
)

// void-works#78: a token the auth service rejects (expired, revoked) means the
// person is logged out. authGate used to answer the 401 with a fresh error that
// dropped auth.ErrNotLoggedIn, so nothing could tell a rejected token from a
// failed check, and the welcome screen kept offering «Запустить» — a launch that
// then stops on "Session token rejected". The screen for a logged-out person is
// the one with «Войти», which runs `vc login`.

func TestAuthGateRejectedTokenIsNotLoggedIn(t *testing.T) {
	withTempHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, reached, err := authGate("stale-token", srv.URL, srv.Client())
	if reached || !errors.Is(err, auth.ErrNotLoggedIn) {
		t.Fatalf("authGate on 401 = (reached=%v, %v), want an error matching auth.ErrNotLoggedIn", reached, err)
	}
	// The CLI prints this text as is; it stays word for word.
	const want = "Session token rejected by auth server (likely expired or revoked).\nRun `vc login` to re-authenticate."
	if err.Error() != want {
		t.Errorf("authGate on 401 says %q, want %q", err.Error(), want)
	}
}

func TestAuthGateOnlyRejectedTokenIsNotLoggedIn(t *testing.T) {
	withTempHome(t)
	for _, status := range []int{http.StatusPaymentRequired, http.StatusServiceUnavailable, http.StatusInternalServerError} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		_, _, err := authGate("token", srv.URL, srv.Client())
		srv.Close()
		if err == nil || errors.Is(err, auth.ErrNotLoggedIn) {
			t.Errorf("authGate on %d = %v, want an error that is not auth.ErrNotLoggedIn: the token was not rejected", status, err)
		}
	}
}

// rejectingMeServer answers /v1/vc/me with 401 once release is called.
func rejectingMeServer(t *testing.T) (host string, release func()) {
	t.Helper()
	gate := make(chan struct{})
	released := false
	release = func() {
		if !released {
			released = true
			close(gate)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(release)
	return srv.URL, release
}

// choose presses key on the welcome screen and returns what it chose.
func (s *welcomeSession) choose(key string) welcome.RunResult {
	s.t.Helper()
	var result welcome.RunResult
	s.once.Do(func() {
		go func() { _, _ = s.keys.Write([]byte(key)) }()
		select {
		case <-s.done:
			result = s.result
		case <-time.After(5 * time.Second):
			s.t.Fatalf("the welcome program did not end on %q", key)
		}
		_ = s.keys.Close()
	})
	return result
}

func TestWelcomeOffersLoginWhenTheLaunchRejectsTheToken(t *testing.T) {
	const signedOut, login, launch = "Вход не выполнен", "Войти", "Запустить"

	t.Run("arrives while the screen is up", func(t *testing.T) {
		host, release := rejectingMeServer(t)
		state, _, _ := welcomeLaunch(t, host)
		s := showWelcome(t, state)
		if screen, drawn := s.waitFor(launch, time.Second); !drawn {
			t.Fatalf("before /v1/vc/me answers, a local token shows the signed-in menu:\n%s", screen)
		}
		release()
		screen, ok := s.waitFor(signedOut, 3*time.Second)
		if !ok {
			t.Fatalf("/v1/vc/me rejected the token while the screen was up, and the screen never said %q:\n%s", signedOut, screen)
		}
		if after := screen[strings.LastIndex(screen, signedOut):]; !strings.Contains(after, login) || strings.Contains(after, launch) {
			t.Errorf("after the token was rejected the menu should offer %q and not %q:\n%s", login, launch, after)
		}
		if got := s.choose("\r"); got != welcome.RunLogin {
			t.Errorf("enter on the signed-out screen chose %v, want RunLogin", got)
		}
	})

	t.Run("already in", func(t *testing.T) {
		host, release := rejectingMeServer(t)
		release()
		state, token, authHost := welcomeLaunch(t, host)
		if _, _, err, reused := currentLaunchPreflight.awaitAuth(token, authHost); !reused || !errors.Is(err, auth.ErrNotLoggedIn) {
			t.Fatalf("preflight: reused=%v err=%v, want the rejection", reused, err)
		}
		s := showWelcome(t, state)
		screen, ok := s.waitFor(welcomeMenuPrompt, 2*time.Second)
		if !ok {
			t.Fatalf("the welcome screen was not drawn:\n%s", screen)
		}
		if !strings.Contains(screen, signedOut) || !strings.Contains(screen, login) || strings.Contains(screen, launch) {
			t.Errorf("the launch already knew the token was rejected; the screen should be the signed-out one:\n%s", screen)
		}
		if got := s.choose("\r"); got != welcome.RunLogin {
			t.Errorf("enter on the signed-out screen chose %v, want RunLogin", got)
		}
	})
}
