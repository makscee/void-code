package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

// Two hosts, two jobs, and until now one setting for both. In production the
// split is real and neither half can be moved to the other's host:
//
//	the access check      — Relay honours our token; the legacy service behind
//	                        the sign-in host answers 401 to the same bearer.
//	sign-in and providers — only the sign-in host serves them. Probed against
//	                        relay:443 on 2026-08-23, where a live route answers
//	                        401 and an absent one answers the CONNECT proxy:
//	                          GET  /v1/vc/me               -> 401  (live)
//	                          GET  /v1/vc/providers        -> 400 "This is a CONNECT proxy"
//	                          POST /v1/public/device/start -> 400 "This is a CONNECT proxy"
//	                          GET  /v1/nonsense/xyz        -> 400 "This is a CONNECT proxy"
//
// So the access check gets its own host and everything else keeps reading
// AuthHost. These tests give the two settings DIFFERENT servers and check which
// one each caller actually talks to — a single-server test would pass on a build
// that still reads one setting for everything.

// hostSpy is an httptest server that records whether it was reached at all, so
// "the request went to the other one" is a fact and not an inference from a
// downstream error message.
type hostSpy struct {
	server *httptest.Server
	hits   atomic.Int32
	paths  chan string
}

func newHostSpy(t *testing.T, status int, body string) *hostSpy {
	t.Helper()
	spy := &hostSpy{paths: make(chan string, 8)}
	spy.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spy.hits.Add(1)
		select {
		case spy.paths <- r.URL.Path:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(spy.server.Close)
	return spy
}

func (s *hostSpy) url() string { return s.server.URL }

func (s *hostSpy) assertReached(t *testing.T, what string) {
	t.Helper()
	if s.hits.Load() == 0 {
		t.Fatalf("%s never reached its host", what)
	}
}

func (s *hostSpy) assertUntouched(t *testing.T, what string) {
	t.Helper()
	if n := s.hits.Load(); n != 0 {
		t.Fatalf("%s host received %d request(s) it should never see — the caller is still reading one setting for both jobs", what, n)
	}
}

// prepareDesktopSession validates the runtime paths before it reaches the gate,
// so the gate is only observable behind two real files on disk.
func executableFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func regularFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("// pi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func signedIn(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := auth.Save("accepted-token"); err != nil {
		t.Fatal(err)
	}
}

// The status probe is the call the desktop makes to answer "am I signed in and
// let in". It is the one that must move.
func TestStatusJSONAsksTheAccessCheckHost(t *testing.T) {
	signedIn(t)
	check := newHostSpy(t, http.StatusOK, `{"userId":"u-1","email":"person@example.test"}`)
	signIn := newHostSpy(t, http.StatusOK, `{"userId":"wrong-host"}`)

	var buf bytes.Buffer
	if err := runStatusJSON(config.Config{AuthHost: signIn.url(), AccessCheckHost: check.url()}, &buf); err != nil {
		t.Fatalf("runStatusJSON: %v", err)
	}

	check.assertReached(t, "the status probe")
	signIn.assertUntouched(t, "the sign-in")
	if got := <-check.paths; got != "/v1/vc/me" {
		t.Errorf("path = %q, want /v1/vc/me", got)
	}
}

// The gate the chat session runs before Pi starts. Leaving this one behind is
// the failure mode worth a test of its own: the app would stop misreporting the
// sign-in and still refuse to open a chat, which is the whole point of the work.
func TestChatSessionGateAsksTheAccessCheckHost(t *testing.T) {
	check := newHostSpy(t, http.StatusOK, `{"userId":"u-1"}`)
	signIn := newHostSpy(t, http.StatusOK, `{"userId":"wrong-host"}`)

	var gateHost string
	deps := desktopSessionDeps{
		loadToken:     func() (string, error) { return "accepted-token", nil },
		resolveConfig: func() config.Config { return config.Config{AuthHost: signIn.url(), AccessCheckHost: check.url()} },
		authGate: func(token, host string, client *http.Client) (auth.MeResult, bool, error) {
			gateHost = host
			me, err := auth.FetchMe(host, token, client)
			return me, err == nil, err
		},
		// Stops the walk one step past the gate. Everything after this point
		// belongs to other tests, and leaving these nil would panic instead of
		// reporting which host the gate used.
		reconcilePi: func() (string, error) { return "", errors.New("stop after the gate") },
	}

	node, piEntry := executableFixture(t, "node"), regularFixture(t, "cli.js")
	// Expected to fail one step past the gate. What is asserted is the host the
	// gate used getting there, not whether a session was ever prepared.
	_, _ = prepareDesktopSession(node, piEntry, nil, deps)

	if gateHost != check.url() {
		t.Fatalf("chat session gate used host %q, want the access-check host %q", gateHost, check.url())
	}
	check.assertReached(t, "the chat session gate")
	signIn.assertUntouched(t, "the sign-in")
}

// Sign-in must not follow. The device-authorization routes exist only on the
// sign-in host, so a build that points this at the access check reports
// start_failed and never puts a pairing code on screen.
func TestDeviceLoginStaysOnTheSignInHost(t *testing.T) {
	check := newHostSpy(t, http.StatusOK, `{"userId":"wrong-host"}`)
	signIn := newHostSpy(t, http.StatusCreated, `{"deviceCode":"d-1","userCode":"ABCD1234","verificationPath":"/device","intervalSeconds":1}`)

	deps := newDeviceLoginDeps(config.Config{AuthHost: signIn.url(), AccessCheckHost: check.url()})
	if _, err := deps.start(); err != nil {
		t.Fatalf("device start: %v", err)
	}

	signIn.assertReached(t, "device start")
	check.assertUntouched(t, "the access check")
	if got := <-signIn.paths; got != "/v1/public/device/start" {
		t.Errorf("path = %q, want /v1/public/device/start", got)
	}
}

// The provider list must not follow either — it is served on the sign-in host
// and answers the CONNECT proxy on the access check's host.
func TestProviderListStaysOnTheSignInHost(t *testing.T) {
	signedIn(t)
	check := newHostSpy(t, http.StatusOK, `{"providers":[]}`)
	signIn := newHostSpy(t, http.StatusOK, `{"providers":[]}`)
	t.Setenv(config.EnvAuthHost, signIn.url())
	t.Setenv(config.EnvAccessCheckHost, check.url())

	// Only which host was asked matters here; whatever the bootstrap makes of
	// an empty list is another test's business.
	_, _ = currentPiBootstrap()

	signIn.assertReached(t, "the provider list")
	check.assertUntouched(t, "the access check")
	if got := <-signIn.paths; got != "/v1/vc/providers" {
		t.Errorf("path = %q, want /v1/vc/providers", got)
	}
}

// The rule these four share: an access check goes to the access-check host, no
// matter which command reached it. Two paths were moved first (the desktop's
// status probe and the chat session gate) and the command line was left behind,
// which is worse than not moving at all — the same token would get one verdict
// through the app and another through `vc`, from the same machine.

// The plain `vc` launch: no sub-command, Pi about to start. This is the chat
// from a terminal, and it is the sibling of the desktop session gate.
func TestSpawnGateAsksTheAccessCheckHost(t *testing.T) {
	signedIn(t)
	// 401 stops runSpawn at the gate. Anything else and it would go on to start Pi.
	check := newHostSpy(t, http.StatusUnauthorized, `{}`)
	signIn := newHostSpy(t, http.StatusOK, `{"userId":"wrong-host"}`)
	t.Setenv(config.EnvAuthHost, signIn.url())
	t.Setenv(config.EnvAccessCheckHost, check.url())

	saved := exitProcess
	exitProcess = func(int) {}
	t.Cleanup(func() { exitProcess = saved })

	_ = runSpawn(nil, nil)

	check.assertReached(t, "the spawn gate")
	signIn.assertUntouched(t, "the sign-in")
}

// The launch preflight runs the same gate in the background while the banner
// draws: resolveLocalAuthStateWithSource hands a host to startLaunchPreflight,
// which passes it to deps.auth — authGate. So the host returned here IS the host
// the access check uses, whether or not a token is present.
func TestLaunchPreflightIsHandedTheAccessCheckHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(config.EnvAuthHost, "https://identity.example")
	t.Setenv(config.EnvAccessCheckHost, "https://check.example")

	for _, signedInFirst := range []bool{false, true} {
		if signedInFirst {
			if err := auth.Save("accepted-token"); err != nil {
				t.Fatal(err)
			}
		}
		_, _, host := resolveLocalAuthState()
		if host != "https://check.example" {
			t.Errorf("signed in=%v: launch preflight host = %q, want the access-check host — it is handed straight to authGate", signedInFirst, host)
		}
	}
}

// The welcome screen's identity and budget line. It reads MeResult, so it is an
// access check like any other, and a wrong host here renders "not logged in" to
// someone who is.
func TestWelcomeAuthStateAsksTheAccessCheckHost(t *testing.T) {
	signedIn(t)
	check := newHostSpy(t, http.StatusOK, `{"userId":"u-1","email":"person@example.test"}`)
	signIn := newHostSpy(t, http.StatusOK, `{"userId":"wrong-host"}`)
	t.Setenv(config.EnvAuthHost, signIn.url())
	t.Setenv(config.EnvAccessCheckHost, check.url())

	_ = resolveAuthState()

	check.assertReached(t, "the welcome auth state")
	signIn.assertUntouched(t, "the sign-in")
}

// `vc status` without --json. Its --json sibling was moved; a human running the
// bare command must not get a different verdict from the same token.
func TestHumanStatusAsksTheAccessCheckHost(t *testing.T) {
	signedIn(t)
	check := newHostSpy(t, http.StatusOK, `{"userId":"u-1","email":"person@example.test"}`)
	signIn := newHostSpy(t, http.StatusOK, `{"userId":"wrong-host"}`)
	t.Setenv(config.EnvAuthHost, signIn.url())
	t.Setenv(config.EnvAccessCheckHost, check.url())

	if err := runStatus(nil, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}

	check.assertReached(t, "vc status")
	signIn.assertUntouched(t, "the sign-in")
}

// authGate has a branch for a rejected token and nothing for a refused account,
// so a refusal falls into "Session verification unavailable; try again". That is
// wrong twice over: the check was not unavailable — it ran and answered — and
// trying again cannot change the answer, because what is missing is an operator
// action. runStatusJSON already learned this; the gate every launch goes through
// did not.
func TestAuthGateReportsARefusedAccountAsItsOwnOutcome(t *testing.T) {
	refusing := newHostSpy(t, http.StatusPaymentRequired, `{"error":"budget_exceeded"}`)

	_, _, err := authGate("accepted-token", refusing.url(), refusing.server.Client())
	if err == nil {
		t.Fatal("a refused account passed the gate")
	}

	// The sentinel travels, so a caller can branch on the outcome instead of
	// matching prose that changes with whatever the server happened to send.
	if !errors.Is(err, auth.ErrAccessNotGranted) {
		t.Errorf("error does not carry ErrAccessNotGranted: %v", err)
	}

	message := strings.ToLower(err.Error())
	for _, wrong := range []string{"unavailable", "try again"} {
		if strings.Contains(message, wrong) {
			t.Errorf("error = %q says %q — the check ran and answered, and repeating it changes nothing", err.Error(), wrong)
		}
	}
	for _, advice := range []string{"vc login", "log in", "sign in", "re-authenticate"} {
		if strings.Contains(message, advice) {
			t.Errorf("error = %q sends the human back to sign-in; signing in is what already worked", err.Error())
			break
		}
	}
}

// The neighbouring branches must keep their own wording: a rejected token still
// means sign in again, and a genuinely unreachable server still means try later.
func TestAuthGateKeepsItsOtherOutcomesDistinct(t *testing.T) {
	rejecting := newHostSpy(t, http.StatusUnauthorized, `{}`)
	_, _, err := authGate("stale-token", rejecting.url(), rejecting.server.Client())
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "login") {
		t.Errorf("a rejected token should still advise signing in, got %v", err)
	}
	if errors.Is(err, auth.ErrAccessNotGranted) {
		t.Error("a rejected token was reported as a refused account — 401 and 402 are opposite facts")
	}

	broken := newHostSpy(t, http.StatusInternalServerError, `{}`)
	_, _, err = authGate("accepted-token", broken.url(), broken.server.Client())
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "try again") {
		t.Errorf("an unreachable check should still say try again, got %v", err)
	}
	if errors.Is(err, auth.ErrAccessNotGranted) {
		t.Error("a broken server was reported as a refused account — the new branch swallowed the one it was supposed to sit beside")
	}
}
