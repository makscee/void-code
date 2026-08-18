package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/provider"
)

func captureRepairStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out), runErr
}
func TestStatusVerifiesSubscriptionAndShowsBudget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/vc/me" || r.Header.Get("Authorization") != "Bearer good" {
			t.Fatalf("unexpected verification request %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"userId":"u-1","email":"user@example.test","pct":27.4,"resetAt":"2026-06-01T00:00:00Z"}`)
	}))
	defer server.Close()
	t.Setenv("VC_AUTH_HOST", server.URL)
	if err := auth.Save("good"); err != nil {
		t.Fatal(err)
	}
	out, err := captureRepairStdout(t, func() error { return runStatus(nil, nil) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"runtime:", "Pi", "logged in as user@example.test", "27% used", "resets Jun 1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q: %s", want, out)
		}
	}
}
func TestStatusDoesNotTreatTokenAsVerified(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusUnauthorized) }))
	defer server.Close()
	t.Setenv("VC_AUTH_HOST", server.URL)
	if err := auth.Save("bad"); err != nil {
		t.Fatal(err)
	}
	out, _ := captureRepairStdout(t, func() error { return runStatus(nil, nil) })
	if !strings.Contains(out, "verification failed") || strings.Contains(out, "logged in as") {
		t.Fatalf("status falsely authenticated token: %s", out)
	}
}
func TestPiBootstrapUsesCurrentSubscriptionRatherThanSavedSelection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/vc/providers" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"providers":[{"id":"grant-a","name":"A","type":"openai-codex-oauth"},{"id":"grant-b","name":"B","type":"deepseek"}]}`)
	}))
	defer server.Close()
	t.Setenv("VC_AUTH_HOST", server.URL)
	t.Setenv("VC_RELAY_HOST", "relay.test:443")
	if err := auth.Save("token"); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := currentPiBootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(bootstrap.Providers) != 2 || bootstrap.Providers[0].RelayProviderID != "grant-a" || bootstrap.Providers[1].RelayProviderID != "grant-b" {
		t.Fatalf("bootstrap did not expose current grants: %#v", bootstrap.Providers)
	}
}
func TestPiExtensionUsesOnlyTrustedBootstrapPath(t *testing.T) {
	if strings.Contains(piVoidCodexExtensionSource, `execFileSync("vc"`) {
		t.Fatal("extension uses PATH-resolved vc")
	}
	for _, want := range []string{"VC_BOOTSTRAP_EXECUTABLE", "path.isAbsolute(executable)", "execFileSync(executable, [\"pi-bootstrap\"]"} {
		if !strings.Contains(piVoidCodexExtensionSource, want) {
			t.Fatalf("extension missing %q", want)
		}
	}
	env := buildPiSpawnEnv(providerRelay(), []string{"VC_BOOTSTRAP_EXECUTABLE=/tmp/attacker", "ANTHROPIC_API_KEY=leak", "OPENAI_API_KEY=leak"}, "https", "relay.test", "secret", "/ca.pem")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "/tmp/attacker") || strings.Contains(joined, "ANTHROPIC_API_KEY=leak") || strings.Contains(joined, "OPENAI_API_KEY=leak") || !strings.Contains(joined, "VC_BOOTSTRAP_EXECUTABLE=") {
		t.Fatalf("unsafe Pi env: %s", joined)
	}
}
func providerRelay() provider.Provider { return provider.Provider{Kind: provider.Relay} }
func desktopFiles(t *testing.T) (string, string) {
	t.Helper()
	d := t.TempDir()
	node := filepath.Join(d, "node")
	if runtime.GOOS == "windows" {
		node += ".exe"
	}
	pi := filepath.Join(d, "pi.js")
	if err := os.WriteFile(node, []byte("x"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pi, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	return node, pi
}
func repairDesktopDeps() desktopSessionDeps {
	return desktopSessionDeps{loadToken: func() (string, error) { return "token", nil }, resolveConfig: func() config.Config {
		return config.Config{AuthHost: "http://invalid", RelayScheme: "https", RelayHost: "relay.invalid"}
	}, authGate: func(string, string, *http.Client) (auth.MeResult, bool, error) { return auth.MeResult{}, true, nil }, resolveCA: func(config.Config) (string, error) { return "/ca.pem", nil }, reconcilePi: func() (string, error) { return "/managed.ts", nil }, reconcileSearch: func(bool) (managedWebSearchState, error) { return managedWebSearchReady, nil }}
}
func TestDesktopSessionProtectsPiAuthorityAndRuntime(t *testing.T) {
	node, pi := desktopFiles(t)
	deps := repairDesktopDeps()
	for _, args := range [][]string{{"--model", "foreign"}, {"--provider=foreign"}, {"hello"}} {
		if _, err := prepareDesktopSession(node, pi, args, deps); err == nil || strings.Contains(err.Error(), "foreign") {
			t.Fatalf("args %q error = %v", args, err)
		}
	}
	plan, err := prepareDesktopSession(node, pi, []string{"--session-id", "chat-1", "--continue"}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if plan.nodePath != node || !strings.Contains(strings.Join(plan.args, " "), "--session-id chat-1") || strings.Contains(strings.Join(plan.args, " "), "--model") {
		t.Fatalf("bad desktop plan: %#v", plan)
	}
	if _, err := prepareDesktopSession("relative-node", pi, nil, deps); err == nil {
		t.Fatal("relative runtime accepted")
	}
	deps.run = func(context.Context, desktopSessionPlan, io.Reader, io.Writer, io.Writer) error {
		return desktopProcessExitError{code: 7}
	}
	cmd := newDesktopSessionCommand(deps)
	cmd.SetIn(bytes.NewBuffer(nil))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--node", node, "--pi-entry", pi})
	if err := cmd.Execute(); err == nil {
		t.Fatal("exit semantics lost")
	}
}
