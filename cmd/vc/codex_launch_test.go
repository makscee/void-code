package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/runtimechoice"
)

// No test in this package may download Codex (130–160 MB from GitHub) or open
// the first-launch menu on whatever terminal `go test` happens to run in. The
// same reason TestMain empties piRuntimeSources; set here, in init, so the
// defaults are replaced before any test — including the existing launch tests —
// reaches runSpawn. Tests that need either seam install their own and restore.
func init() {
	ensureCodexRuntime = func(io.Writer) (string, error) {
		return "", errors.New("test: Codex download is not allowed in cmd/vc tests")
	}
	runtimeTerminalAttached = func() bool { return false }
	chooseRuntime = func() (runtimechoice.Runtime, error) {
		return "", errors.New("test: runtime menu reached without a stub")
	}
}

const codexTestToken = "codex-launch-secret-token-5e1d"

type spawnRecord struct {
	calls int
	exe   string
	args  []string
	env   []string
}

func recordSpawn(t *testing.T) *spawnRecord {
	t.Helper()
	rec := &spawnRecord{}
	saved := spawnHarness
	spawnHarness = func(_ context.Context, exe string, args []string, env []string) error {
		rec.calls++
		rec.exe = exe
		rec.args = append([]string(nil), args...)
		rec.env = append([]string(nil), env...)
		return nil
	}
	t.Cleanup(func() { spawnHarness = saved })
	return rec
}

// noExit keeps a refused authGate from killing the test binary.
func noExit(t *testing.T) {
	t.Helper()
	saved := exitProcess
	exitProcess = func(int) {}
	t.Cleanup(func() { exitProcess = saved })
}

type codexEnsureStub struct {
	calls int
	path  string
	err   error
}

func stubEnsureCodex(t *testing.T, path string, err error) *codexEnsureStub {
	t.Helper()
	stub := &codexEnsureStub{path: path, err: err}
	saved := ensureCodexRuntime
	ensureCodexRuntime = func(io.Writer) (string, error) {
		stub.calls++
		return stub.path, stub.err
	}
	t.Cleanup(func() { ensureCodexRuntime = saved })
	return stub
}

type menuStub struct {
	calls int
}

// stubRuntimeMenu sets whether a terminal is attached and what the menu answers.
func stubRuntimeMenu(t *testing.T, attached bool, answer runtimechoice.Runtime, answerErr error) *menuStub {
	t.Helper()
	stub := &menuStub{}
	savedAttached, savedChoose, savedFlag := runtimeTerminalAttached, chooseRuntime, nonInteractiveFlag
	runtimeTerminalAttached = func() bool { return attached }
	chooseRuntime = func() (runtimechoice.Runtime, error) {
		stub.calls++
		return answer, answerErr
	}
	nonInteractiveFlag = false
	t.Cleanup(func() {
		runtimeTerminalAttached, chooseRuntime, nonInteractiveFlag = savedAttached, savedChoose, savedFlag
	})
	return stub
}

var codexGrants = []map[string]string{
	{"id": "deepseek-granted", "name": "DeepSeek", "type": "deepseek"},
	{"id": "chatgpt-new", "name": "ChatGPT", "type": "openai-codex-oauth"},
	{"id": "chatgpt-other", "name": "Other", "type": "openai-codex-oauth"},
}

// parentEnvPlanted is what the person's shell exports. None of it may reach Codex.
var parentEnvPlanted = map[string]string{
	"OPENAI_API_KEY":       "parent-openai-key",
	"OPENAI_BASE_URL":      "https://parent-openai.example",
	"CODEX_HOME":           "/parent/codex-home",
	"CODEX_API_KEY":        "parent-codex-key",
	"CODEX_SANDBOX":        "parent-sandbox",
	"VC_CODEX_PROVIDER":    "parent-provider",
	"VC_HARNESS":           "parent-harness",
	"VC_RELAY_PROVIDER_ID": "parent-relay-provider",
	"VC_AUTH_TOKEN":        "parent-token",
}

type codexLaunch struct {
	home      string
	fakeCodex string
	userPath  string
	spawn     *spawnRecord
	ensure    *codexEnsureStub
	providers int
}

// prepareCodexLaunch builds a signed-in home whose auth host admits the token
// and answers /v1/vc/providers with grants. No Pi runtime is planted: choosing
// Codex must not depend on Pi being installed.
func prepareCodexLaunch(t *testing.T, grants []map[string]string, providersStatus int) *codexLaunch {
	t.Helper()
	l := &codexLaunch{home: withTempHome(t)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/vc/me":
			_, _ = w.Write([]byte(`{"userId":"u1","email":"u@example.test"}`))
		case "/v1/vc/providers":
			l.providers++
			if got := r.Header.Get("Authorization"); got != "Bearer "+codexTestToken {
				t.Errorf("providers asked with Authorization %q", got)
			}
			if providersStatus != http.StatusOK {
				w.WriteHeader(providersStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"providers": grants})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("VC_AUTH_HOST", server.URL)
	t.Setenv("VC_ACCESS_CHECK_HOST", server.URL)
	t.Setenv("VC_RELAY_HOST", "https://relay.test:9443")
	// A custom relay CA is its own case (refused for Codex in step 1); the
	// default is the public relay with no override at all.
	t.Setenv("VC_RELAY_CA", "")
	_ = os.Unsetenv("VC_RELAY_CA")
	for k, v := range parentEnvPlanted {
		t.Setenv(k, v)
	}
	l.userPath = filepath.Join(l.home, "user-tools", "bin")
	if err := os.MkdirAll(l.userPath, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", l.userPath)
	if err := auth.Save(codexTestToken); err != nil {
		t.Fatal(err)
	}

	l.fakeCodex = filepath.Join(l.home, "fake-codex-install", "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(l.fakeCodex), 0700); err != nil {
		t.Fatal(err)
	}
	writeExecutableFixture(t, l.fakeCodex, "#!/bin/sh\nexit 0\n")
	l.ensure = stubEnsureCodex(t, l.fakeCodex, nil)
	l.spawn = recordSpawn(t)
	noExit(t)
	return l
}

func saveRuntimeKey(t *testing.T, value string) {
	t.Helper()
	if err := config.WriteConfigFile(map[string]string{"runtime": value}); err != nil {
		t.Fatal(err)
	}
}

func envCount(env []string, key string) (string, int) {
	value, n := "", 0
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if k == key {
			value = v
			n++
		}
	}
	return value, n
}

func wantEnvOnce(t *testing.T, env []string, key, want string) {
	t.Helper()
	got, n := envCount(env, key)
	if n != 1 || got != want {
		t.Errorf("%s = %q (present %d times), want exactly one %s=%s", key, got, n, key, want)
	}
}

func TestCodexLaunchSpawnsTheInstalledCodexThroughTheRelay(t *testing.T) {
	l := prepareCodexLaunch(t, codexGrants, http.StatusOK)
	saveRuntimeKey(t, "codex")
	stubRuntimeMenu(t, false, "", nil)

	if err := runSpawn(nil, nil); err != nil {
		t.Fatalf("runSpawn with runtime=codex: %v", err)
	}
	if l.ensure.calls != 1 {
		t.Fatalf("ensureCodexRuntime called %d times, want 1", l.ensure.calls)
	}
	if l.spawn.calls != 1 {
		t.Fatalf("spawnHarness called %d times, want 1", l.spawn.calls)
	}
	if l.spawn.exe != l.fakeCodex {
		t.Fatalf("spawned %q, want the Codex that ensureCodexRuntime returned (%q)", l.spawn.exe, l.fakeCodex)
	}
	// --no-daemon, and nothing else: Codex 0.158's TUI otherwise starts a
	// `codex app-server --managed-daemon` that outlives the session and needs a
	// unix socket under CODEX_HOME, which fails on long homes with "path must be
	// shorter than SUN_LEN" (live run, 29.09).
	if len(l.spawn.args) != 1 || l.spawn.args[0] != "--no-daemon" {
		t.Fatalf("Codex was given arguments %q, want exactly [--no-daemon]", l.spawn.args)
	}

	env := l.spawn.env
	codexHome, n := envCount(env, "CODEX_HOME")
	wantHome := filepath.Join(l.home, ".void-code", "codex")
	if n != 1 || !sameDirectory(codexHome, wantHome) {
		t.Fatalf("CODEX_HOME = %q (present %d times), want exactly one pointing at %s", codexHome, n, wantHome)
	}
	wantEnvOnce(t, env, "VC_AUTH_TOKEN", codexTestToken)
	// The first openai-codex-oauth grant, in the order auth lists them.
	wantEnvOnce(t, env, "VC_CODEX_PROVIDER", "chatgpt-new")
	wantEnvOnce(t, env, "VC_HARNESS", "codex")
	for _, key := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_API_KEY", "CODEX_SANDBOX", "VC_RELAY_PROVIDER_ID"} {
		if v, n := envCount(env, key); n != 0 {
			t.Errorf("inherited %s=%q reached Codex", key, v)
		}
	}
	joined := strings.Join(env, "\n")
	for key, planted := range parentEnvPlanted {
		if strings.Contains(joined, planted) {
			t.Errorf("parent value of %s (%q) reached Codex", key, planted)
		}
	}
	// Codex is a native binary; its tools run in the person's own environment.
	wantEnvOnce(t, env, "PATH", l.userPath)

	configPath := filepath.Join(codexHome, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("$CODEX_HOME/config.toml was not written before launch: %v", err)
	}
	if strings.Contains(string(data), codexTestToken) {
		t.Fatal("config.toml carries the VC token; it must travel only in the environment")
	}
	if !strings.Contains(string(data), `"https://relay.test:9443/codex"`) {
		t.Errorf("config.toml does not point Codex at the configured relay:\n%s", data)
	}
	if !strings.Contains(string(data), "VC_CODEX_PROVIDER") || !strings.Contains(string(data), "VC_AUTH_TOKEN") {
		t.Errorf("config.toml does not name the env vars Codex reads its provider and token from:\n%s", data)
	}
	// The person's own ~/.codex is theirs; VC keeps to ~/.void-code/codex.
	if _, err := os.Lstat(filepath.Join(l.home, ".codex")); err == nil {
		t.Error("launch created ~/.codex")
	}
}

func TestCodexLaunchWithoutAChatGPTGrantDoesNotStartCodex(t *testing.T) {
	l := prepareCodexLaunch(t, []map[string]string{
		{"id": "deepseek-granted", "name": "DeepSeek", "type": "deepseek"},
	}, http.StatusOK)
	saveRuntimeKey(t, "codex")
	stubRuntimeMenu(t, false, "", nil)

	err := runSpawn(nil, nil)
	if err == nil {
		t.Fatal("runSpawn with runtime=codex and no ChatGPT grant returned nil")
	}
	if !strings.Contains(err.Error(), "vc runtime pi") {
		t.Errorf("error %q does not tell the person to switch with `vc runtime pi`", err)
	}
	if l.spawn.calls != 0 {
		t.Fatalf("Codex was spawned without a grant: %q", l.spawn.exe)
	}
	// A refused person must not pay for a 130–160 MB download first.
	if l.ensure.calls != 0 {
		t.Fatalf("Codex was installed (%d calls) for a person with no ChatGPT grant", l.ensure.calls)
	}
	if l.providers == 0 {
		t.Error("the grant decision was made without asking auth for the current providers")
	}
}

func TestCodexLaunchStopsWhenTheGrantListCannotBeRead(t *testing.T) {
	l := prepareCodexLaunch(t, nil, http.StatusInternalServerError)
	saveRuntimeKey(t, "codex")
	stubRuntimeMenu(t, false, "", nil)

	if err := runSpawn(nil, nil); err == nil {
		t.Fatal("runSpawn returned nil although the provider list failed")
	}
	if l.spawn.calls != 0 {
		t.Fatalf("Codex was spawned with no known grant: %q", l.spawn.exe)
	}
	if l.ensure.calls != 0 {
		t.Fatalf("Codex was installed (%d calls) before any grant was known", l.ensure.calls)
	}
}

func TestCodexLaunchStopsWhenCodexCannotBeInstalled(t *testing.T) {
	l := prepareCodexLaunch(t, codexGrants, http.StatusOK)
	l.ensure.path, l.ensure.err = "", errors.New("sha256 mismatch")
	saveRuntimeKey(t, "codex")
	stubRuntimeMenu(t, false, "", nil)

	if err := runSpawn(nil, nil); err == nil {
		t.Fatal("runSpawn returned nil although Codex could not be installed")
	}
	if l.spawn.calls != 0 {
		t.Fatalf("something was spawned without an installed Codex: %q", l.spawn.exe)
	}
}

// Step 1 supports only the publicly trusted relay. A custom CA is refused with
// a hint rather than left to fail as an opaque TLS error inside Codex.
func TestCodexLaunchRefusesACustomRelayCA(t *testing.T) {
	l := prepareCodexLaunch(t, codexGrants, http.StatusOK)
	caPath := filepath.Join(l.home, "relay-ca.pem")
	if err := os.WriteFile(caPath, []byte("test CA"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VC_RELAY_CA", caPath)
	saveRuntimeKey(t, "codex")
	stubRuntimeMenu(t, false, "", nil)

	err := runSpawn(nil, nil)
	if err == nil {
		t.Fatal("runSpawn with runtime=codex and VC_RELAY_CA returned nil")
	}
	if !strings.Contains(err.Error(), "VC_RELAY_CA") {
		t.Errorf("error %q does not name VC_RELAY_CA", err)
	}
	if l.spawn.calls != 0 {
		t.Fatalf("Codex was spawned against a custom-CA relay: %q", l.spawn.exe)
	}
	if l.ensure.calls != 0 {
		t.Fatalf("Codex was installed (%d calls) for a relay step 1 refuses", l.ensure.calls)
	}
}
