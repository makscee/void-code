package main

import (
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/welcome"
)

func TestWelcomeToSpawnAdmission_ReusesOneInflightAuthAndProvider(t *testing.T) {
	oldRunWelcome := runWelcome
	t.Cleanup(func() { runWelcome = oldRunWelcome })

	authStarted := make(chan struct{})
	providerStarted := make(chan struct{})
	release := make(chan struct{})
	var authCalls, providerCalls atomic.Int32
	deps := testPreflightDeps(time.Now)
	deps.auth = func(string, string, *http.Client) (auth.MeResult, bool, error) {
		authCalls.Add(1)
		close(authStarted)
		<-release
		return auth.MeResult{UserID: "u"}, true, nil
	}
	deps.providers = func(string, string, *http.Client) ([]auth.ProviderInfo, error) {
		providerCalls.Add(1)
		close(providerStarted)
		<-release
		return []auth.ProviderInfo{{ID: "p", Name: "ChatGPT"}}, nil
	}
	p := startLaunchPreflight("legacy", "host", false, deps)

	welcomeEntered := make(chan struct{})
	welcomeRelease := make(chan struct{})
	runWelcome = func(welcome.AuthState, welcome.Callbacks) (welcome.RunResult, error) {
		close(welcomeEntered) // deterministic first-render boundary
		<-welcomeRelease
		return welcome.SpawnClaude, nil
	}
	welcomeDone := make(chan struct{})
	go func() {
		_, _ = runWelcomeScreen(welcome.AuthState{LoggedIn: true}, welcome.Callbacks{})
		close(welcomeDone)
	}()

	<-welcomeEntered
	<-authStarted
	<-providerStarted
	if authCalls.Load() != 1 || providerCalls.Load() != 1 {
		t.Fatalf("at first render calls auth=%d providers=%d, want one each", authCalls.Load(), providerCalls.Load())
	}

	admitted := make(chan error, 1)
	go func() {
		_, _, err := awaitSpawnAdmission(p, "legacy", "host")
		admitted <- err
	}()
	close(release)
	if err := <-admitted; err != nil {
		t.Fatalf("spawn admission: %v", err)
	}
	<-p.providersDone
	if got := spawnCompatGrants(p, "legacy", "host"); len(got) != 1 || got[0].ID != "p" {
		t.Fatalf("spawn grants = %#v, want carried provider", got)
	}
	if authCalls.Load() != 1 || providerCalls.Load() != 1 {
		t.Fatalf("consumer duplicated calls auth=%d providers=%d", authCalls.Load(), providerCalls.Load())
	}
	close(welcomeRelease)
	<-welcomeDone
}

func TestWelcomeToSpawnAdmission_Reuses401(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	deps := testPreflightDeps(time.Now)
	deps.auth = func(string, string, *http.Client) (auth.MeResult, bool, error) {
		calls.Add(1)
		<-release
		return auth.MeResult{}, true, errors.New("Session token rejected — re-authenticate with `vc login`")
	}
	deps.providers = func(string, string, *http.Client) ([]auth.ProviderInfo, error) { return nil, nil }
	p := startLaunchPreflight("legacy", "host", false, deps)
	result := make(chan error, 1)
	go func() {
		_, _, err := awaitSpawnAdmission(p, "legacy", "host")
		result <- err
	}()
	close(release)
	if err := <-result; err == nil {
		t.Fatal("carried 401 did not block spawn admission")
	}
	if calls.Load() != 1 {
		t.Fatalf("401 auth calls = %d, want one", calls.Load())
	}
}

func TestWelcomeToSpawnAdmission_TimeoutDoesNotDuplicateInflightCalls(t *testing.T) {
	now := time.Unix(100, 0)
	blocked := make(chan struct{})
	authStarted := make(chan struct{})
	providerStarted := make(chan struct{})
	var authCalls, providerCalls atomic.Int32
	deps := testPreflightDeps(func() time.Time { return now })
	deps.auth = func(string, string, *http.Client) (auth.MeResult, bool, error) {
		authCalls.Add(1)
		close(authStarted)
		<-blocked
		return auth.MeResult{}, false, nil
	}
	deps.providers = func(string, string, *http.Client) ([]auth.ProviderInfo, error) {
		providerCalls.Add(1)
		close(providerStarted)
		<-blocked
		return nil, nil
	}
	p := startLaunchPreflight("legacy", "host", false, deps)
	<-authStarted
	<-providerStarted
	now = now.Add(authProbeTimeout)
	if _, reached, err := awaitSpawnAdmission(p, "legacy", "host"); err != nil || reached {
		t.Fatalf("timeout admission reached=%v err=%v", reached, err)
	}
	if got := spawnCompatGrants(p, "legacy", "host"); got != nil {
		t.Fatalf("inflight provider grants = %#v, want nil", got)
	}
	if authCalls.Load() != 1 || providerCalls.Load() != 1 {
		t.Fatalf("timeout duplicated calls auth=%d providers=%d", authCalls.Load(), providerCalls.Load())
	}
	close(blocked)
}
