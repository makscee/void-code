package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/harnesschoice"
	"github.com/makscee/void-code/internal/welcome"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func unauthorizedClient() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(bytes.NewBuffer(nil)), Header: make(http.Header)}, nil
	})}
}

type firstWriteBuffer struct {
	bytes.Buffer
	once  sync.Once
	wrote chan struct{}
}

func (w *firstWriteBuffer) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	w.once.Do(func() { close(w.wrote) })
	return n, err
}

type launchIntegration struct {
	input         *io.PipeWriter
	firstRender   <-chan struct{}
	done          <-chan error
	spawnCalls    *atomic.Int32
	authCalls     *atomic.Int32
	providerCalls *atomic.Int32
}

func startLaunchIntegration(t *testing.T, token string, now func() time.Time, authProbe func() (auth.MeResult, bool, error), providerProbe func() ([]auth.ProviderInfo, error)) launchIntegration {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VC_AUTH_HOST", "https://auth.test")
	caPath := filepath.Join(home, "relay-ca.pem")
	if err := os.WriteFile(caPath, []byte("test ca"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VC_RELAY_CA", caPath)
	if err := auth.Save(token); err != nil {
		t.Fatal(err)
	}
	if err := harnesschoice.Save(harnesschoice.Choice{Kind: harnesschoice.Pi}); err != nil {
		t.Fatal(err)
	}

	oldOptions, oldSpawn, oldPi, oldExit, oldPreflight := welcomeProgramOptions, spawnHarness, piIsInstalled, exitProcess, currentLaunchPreflight
	t.Cleanup(func() {
		welcomeProgramOptions, spawnHarness, piIsInstalled, exitProcess, currentLaunchPreflight = oldOptions, oldSpawn, oldPi, oldExit, oldPreflight
	})

	var authCalls, providerCalls, spawnCalls atomic.Int32
	deps := testPreflightDeps(now)
	deps.auth = func(string, string, *http.Client) (auth.MeResult, bool, error) {
		authCalls.Add(1)
		return authProbe()
	}
	deps.providers = func(string, string, *http.Client) ([]auth.ProviderInfo, error) {
		providerCalls.Add(1)
		return providerProbe()
	}
	currentLaunchPreflight = startLaunchPreflight(token, "https://auth.test", false, deps)

	reader, writer := io.Pipe()
	output := &firstWriteBuffer{wrote: make(chan struct{})}
	welcomeProgramOptions = []tea.ProgramOption{tea.WithInput(reader), tea.WithOutput(output)}
	piIsInstalled = func() bool { return true }
	spawnHarness = func(context.Context, string, []string, []string) error {
		spawnCalls.Add(1)
		return nil
	}
	exitProcess = func(int) {}

	done := make(chan error, 1)
	go func() {
		done <- runWelcomeCommandTransition(welcome.AuthState{LoggedIn: true}, welcome.Callbacks{PiInstalled: true}, rootCmd, nil)
	}()
	t.Cleanup(func() { _ = writer.Close() })
	return launchIntegration{writer, output.wrote, done, &spawnCalls, &authCalls, &providerCalls}
}

func TestWelcomeRunToSpawnAdmission_ReusesInflightAuthAndProviderAfterImmediateRender(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	flow := startLaunchIntegration(t, "legacy", time.Now,
		func() (auth.MeResult, bool, error) {
			started <- struct{}{}
			<-release
			return auth.MeResult{UserID: "u"}, true, nil
		},
		func() ([]auth.ProviderInfo, error) {
			started <- struct{}{}
			<-release
			return []auth.ProviderInfo{{ID: "p", Name: "ChatGPT"}}, nil
		},
	)
	<-started
	<-started
	<-flow.firstRender
	if flow.authCalls.Load() != 1 || flow.providerCalls.Load() != 1 {
		t.Fatalf("at first production render calls auth=%d providers=%d", flow.authCalls.Load(), flow.providerCalls.Load())
	}
	close(release)
	if _, err := flow.input.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	if err := <-flow.done; err != nil {
		t.Fatal(err)
	}
	if flow.spawnCalls.Load() != 1 || flow.authCalls.Load() != 1 || flow.providerCalls.Load() != 1 {
		t.Fatalf("calls spawn=%d auth=%d providers=%d", flow.spawnCalls.Load(), flow.authCalls.Load(), flow.providerCalls.Load())
	}
}

func TestWelcomeRunToSpawnAdmission_Legacy401IsAuthoritative(t *testing.T) {
	release := make(chan struct{})
	flow := startLaunchIntegration(t, "legacy", time.Now,
		func() (auth.MeResult, bool, error) {
			<-release
			return authGate("legacy", "https://auth.test", unauthorizedClient())
		},
		func() ([]auth.ProviderInfo, error) { return nil, nil },
	)
	<-flow.firstRender
	close(release)
	_, _ = flow.input.Write([]byte("\r"))
	if err := <-flow.done; err == nil || flow.spawnCalls.Load() != 0 {
		t.Fatalf("legacy 401 err=%v spawn=%d", err, flow.spawnCalls.Load())
	}
	if flow.authCalls.Load() != 1 {
		t.Fatalf("auth calls=%d", flow.authCalls.Load())
	}
}

func TestWelcomeRunToSpawnAdmission_TransientTimeoutDoesNotDuplicate(t *testing.T) {
	now := time.Unix(100, 0)
	blocked := make(chan struct{})
	flow := startLaunchIntegration(t, "legacy", func() time.Time { return now },
		func() (auth.MeResult, bool, error) { <-blocked; return auth.MeResult{}, false, nil },
		func() ([]auth.ProviderInfo, error) { <-blocked; return nil, nil },
	)
	<-flow.firstRender
	now = now.Add(authProbeTimeout)
	_, _ = flow.input.Write([]byte("\r"))
	if err := <-flow.done; err != nil {
		t.Fatal(err)
	}
	if flow.spawnCalls.Load() != 1 || flow.authCalls.Load() != 1 || flow.providerCalls.Load() != 1 {
		t.Fatalf("calls spawn=%d auth=%d providers=%d", flow.spawnCalls.Load(), flow.authCalls.Load(), flow.providerCalls.Load())
	}
	close(blocked)
}

func TestWelcomeRunToSpawnAdmission_DottedIdentity401DefersToRelay(t *testing.T) {
	release := make(chan struct{})
	flow := startLaunchIntegration(t, "session.secret", time.Now,
		func() (auth.MeResult, bool, error) {
			<-release
			return authGate("session.secret", "https://auth.test", unauthorizedClient())
		},
		func() ([]auth.ProviderInfo, error) { return nil, nil },
	)
	<-flow.firstRender
	close(release)
	_, _ = flow.input.Write([]byte("\r"))
	if err := <-flow.done; err != nil {
		t.Fatal(err)
	}
	if flow.spawnCalls.Load() != 1 || flow.authCalls.Load() != 1 {
		t.Fatalf("dotted identity calls spawn=%d auth=%d", flow.spawnCalls.Load(), flow.authCalls.Load())
	}
}
