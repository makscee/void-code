package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/welcome"
)

func TestLaunchDiagnostics_FakeClockMeasuresEachFixedPhaseOnceConcurrently(t *testing.T) {
	var ticks atomic.Int64
	base := time.Unix(100, 0)
	now := func() time.Time { return base.Add(time.Duration(ticks.Add(1)) * time.Millisecond) }
	var out bytes.Buffer
	d := newLaunchDiagnostics(true, now, &out)
	phases := []launchPhase{
		phaseLocalStateLoad, phaseFirstRender, phaseAuthComplete, phaseProvidersComplete,
		phaseUpdateComplete, phaseSelection, phaseSpawnHandoff,
	}
	var wg sync.WaitGroup
	for _, phase := range phases {
		for range 20 {
			wg.Add(1)
			go func(phase launchPhase) {
				defer wg.Done()
				d.record(phase, outcomeComplete, sourceLocal)
			}(phase)
		}
	}
	wg.Wait()
	d.flush()

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(phases) {
		t.Fatalf("records = %d, want %d\n%s", len(lines), len(phases), out.String())
	}
	for _, phase := range phases {
		needle := "phase=" + string(phase) + " "
		count := 0
		for _, line := range lines {
			if strings.Contains(line, needle) {
				count++
			}
			fields := strings.Fields(line)
			if len(fields) != 5 {
				t.Fatalf("invalid record %q", line)
			}
			var duration int64
			if _, err := fmt.Sscanf(fields[4], "duration_ms=%d", &duration); err != nil || duration < 0 {
				t.Fatalf("invalid duration record %q: duration=%d err=%v", line, duration, err)
			}
		}
		if count != 1 {
			t.Fatalf("%s records = %d, want 1", phase, count)
		}
	}
}

func TestLaunchDiagnostics_ClampsBackwardClockAndRejectsNonAllowlistedValues(t *testing.T) {
	times := []time.Time{time.Unix(10, 0), time.Unix(9, 0)}
	var call atomic.Int32
	now := func() time.Time { return times[int(call.Add(1))-1] }
	var out bytes.Buffer
	d := newLaunchDiagnostics(true, now, &out)
	d.record(launchPhase("token=secret"), launchOutcome("error=secret"), launchSource("url=secret"))
	d.record(phaseLocalStateLoad, outcomeComplete, sourceLocal)
	d.flush()
	if got, want := out.String(), "vc-launch phase=local_state_load outcome=complete source=local duration_ms=0\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestLaunchDiagnostics_DisabledWelcomeOutputIsByteIdentical(t *testing.T) {
	oldOptions, oldDiagnostics := welcomeProgramOptions, currentLaunchDiagnostics
	t.Cleanup(func() { welcomeProgramOptions, currentLaunchDiagnostics = oldOptions, oldDiagnostics })

	var ordinary bytes.Buffer
	welcomeProgramOptions = []tea.ProgramOption{tea.WithInput(bytes.NewBufferString("q")), tea.WithOutput(&ordinary)}
	currentLaunchDiagnostics = newLaunchDiagnostics(false, time.Now, &bytes.Buffer{})
	if _, err := runWelcomeScreen(welcome.AuthState{LoggedIn: true}, welcome.Callbacks{}); err != nil {
		t.Fatal(err)
	}

	var baseline bytes.Buffer
	welcomeProgramOptions = []tea.ProgramOption{tea.WithInput(bytes.NewBufferString("q")), tea.WithOutput(&baseline)}
	currentLaunchDiagnostics = nil
	if _, err := runWelcomeScreen(welcome.AuthState{LoggedIn: true}, welcome.Callbacks{}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ordinary.Bytes(), baseline.Bytes()) {
		t.Fatalf("disabled diagnostics changed welcome output\nordinary=%q\nbaseline=%q", ordinary.Bytes(), baseline.Bytes())
	}
}

func TestLaunchDiagnostics_OptInRequiresExactOneAndDisabledOutputIsEmpty(t *testing.T) {
	for _, value := range []string{"", "0", "true", "TRUE", " 1"} {
		t.Run(fmt.Sprintf("value_%q", value), func(t *testing.T) {
			t.Setenv("VC_LAUNCH_DIAGNOSTICS", value)
			var out bytes.Buffer
			d := newLaunchDiagnosticsFromEnv(time.Now, &out)
			d.record(phaseSpawnHandoff, outcomeComplete, sourceLocal)
			d.flush()
			if out.Len() != 0 {
				t.Fatalf("ordinary output changed: %q", out.String())
			}
		})
	}
	t.Setenv("VC_LAUNCH_DIAGNOSTICS", "1")
	var out bytes.Buffer
	d := newLaunchDiagnosticsFromEnv(time.Now, &out)
	d.record(phaseSpawnHandoff, outcomeComplete, sourceLocal)
	d.flush()
	if !strings.HasPrefix(out.String(), "vc-launch phase=spawn_handoff outcome=complete source=local duration_ms=") {
		t.Fatalf("opt-in output = %q", out.String())
	}
}

func TestLaunchDiagnostics_AdversarialInputsCannotReachAllowlistOutput(t *testing.T) {
	secrets := []string{
		"TOKEN_secret_123", "hash_deadbeef", "person-secret@example.test", "user-secret-42",
		"Authorization: Bearer header-secret", "otp=654321", "credential-secret", "auth-cache-secret.json",
		"https://auth.test/path?code=query-secret", "response-body-secret", "raw-error-secret",
	}
	var out bytes.Buffer
	base := time.Unix(1, 0)
	d := newLaunchDiagnostics(true, func() time.Time { return base }, &out)
	deps := testPreflightDeps(func() time.Time { return base })
	deps.diagnostics = d
	deps.auth = func(token, host string, client *http.Client) (auth.MeResult, bool, error) {
		_ = token
		_ = host
		_ = client
		return auth.MeResult{Email: secrets[2], UserID: secrets[3]}, false, errors.New(strings.Join(secrets, " "))
	}
	deps.providers = func(host, token string, client *http.Client) ([]auth.ProviderInfo, error) {
		_ = host
		_ = token
		_ = client
		return []auth.ProviderInfo{{ID: secrets[6], Name: secrets[9]}}, errors.New(strings.Join(secrets, " "))
	}
	deps.update = func() string { return strings.Join(secrets, " ") }
	p := startLaunchPreflight(strings.Join(secrets, " "), secrets[8], true, deps)
	<-p.authDone
	<-p.providersDone
	<-p.updateDone
	d.record(phaseLocalStateLoad, outcomeComplete, sourceStale)
	d.record(phaseFirstRender, outcomeComplete, sourceLocal)
	d.record(phaseSelection, outcomeComplete, sourceLocal)
	d.record(phaseSpawnHandoff, outcomeRejected, sourceRejected)
	d.flush()

	got := out.String()
	for _, secret := range secrets {
		if strings.Contains(got, secret) {
			t.Fatalf("diagnostics leaked %q in %q", secret, got)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 5 || fields[0] != "vc-launch" || !strings.HasPrefix(fields[1], "phase=") ||
			!strings.HasPrefix(fields[2], "outcome=") || !strings.HasPrefix(fields[3], "source=") ||
			!strings.HasPrefix(fields[4], "duration_ms=") {
			t.Fatalf("record contains non-allowlisted fields: %q", line)
		}
	}
}

func TestLaunchDiagnostics_BuffersUntilFlush(t *testing.T) {
	var out bytes.Buffer
	d := newLaunchDiagnostics(true, time.Now, &out)
	d.record(phaseFirstRender, outcomeComplete, sourceLocal)
	if out.Len() != 0 {
		t.Fatalf("diagnostic wrote while welcome may own terminal: %q", out.String())
	}
	d.record(phaseSpawnHandoff, outcomeComplete, sourceLocal)
	d.flush()
	if lines := strings.Count(out.String(), "\n"); lines != 2 {
		t.Fatalf("flushed records = %d, want 2", lines)
	}
}

func TestLaunchDiagnostics_DelayedPreflightCompletionWritesNoPostFlushBytes(t *testing.T) {
	var out bytes.Buffer
	base := time.Unix(100, 0)
	d := newLaunchDiagnostics(true, func() time.Time { return base }, &out)
	providerRelease := make(chan struct{})
	updateRelease := make(chan struct{})
	deps := testPreflightDeps(func() time.Time { return base })
	deps.diagnostics = d
	deps.providers = func(string, string, *http.Client) ([]auth.ProviderInfo, error) {
		<-providerRelease
		return nil, nil
	}
	deps.update = func() string {
		<-updateRelease
		return ""
	}

	p := startLaunchPreflight("token", "https://auth.test", true, deps)
	<-p.authDone
	d.record(phaseSpawnHandoff, outcomeComplete, sourceLocal)
	d.flush()
	flushed := append([]byte(nil), out.Bytes()...)

	close(providerRelease)
	close(updateRelease)
	<-p.providersDone
	<-p.updateDone

	if got := out.Bytes(); !bytes.Equal(got, flushed) {
		t.Fatalf("late provider/update completion wrote after terminal-safe flush\nbefore=%q\nafter=%q", flushed, got)
	}
	if strings.Contains(out.String(), "phase=providers_complete") || strings.Contains(out.String(), "phase=update_complete") {
		t.Fatalf("flush included records that were incomplete at handoff: %q", out.String())
	}
}
