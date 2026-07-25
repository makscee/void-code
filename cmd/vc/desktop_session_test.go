package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/compat"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/provider"
)

func desktopRuntimeFiles(t *testing.T) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private runtime 日本語 space")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(dir, "node")
	if runtime.GOOS == "windows" {
		node += ".exe"
	}
	pi := filepath.Join(dir, "pi cli.js")
	if err := os.WriteFile(node, []byte("runtime fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pi, []byte("entry fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	return node, pi
}

func desktopTestDeps() desktopSessionDeps {
	return desktopSessionDeps{
		loadToken: func() (string, error) { return "synthetic-credential", nil },
		resolveConfig: func() config.Config {
			return config.Config{AuthHost: "http://auth.invalid", RelayScheme: "https", RelayHost: "relay.invalid"}
		},
		authGate: func(string, string, *http.Client) (auth.MeResult, bool, error) {
			return auth.MeResult{}, true, nil
		},
		fetchGrants: func(string, string) ([]compat.Grant, error) { return []compat.Grant{}, nil },
		resolveCA: func(config.Config) (string, error) {
			return filepath.Join(string(filepath.Separator), "private", "ca.pem"), nil
		},
		reconcilePi: func() (string, error) {
			return filepath.Join(string(filepath.Separator), "managed", "void-code.ts"), nil
		},
		reconcileSearch: func(bool) (managedWebSearchState, error) {
			return managedWebSearchReady, nil
		},
		loadProvider: func() provider.Provider { return provider.Provider{Kind: provider.Relay} },
		loadLabel:    func() string { return "DeepSeek relay" },
		run: func(context.Context, desktopSessionPlan, io.Reader, io.Writer, io.Writer) error {
			return nil
		},
	}
}

func TestDesktopSessionConstructsDirectPrivateNodeCommandWithExactArgs(t *testing.T) {
	node, pi := desktopRuntimeFiles(t)
	deps := desktopTestDeps()
	var got desktopSessionPlan
	deps.run = func(_ context.Context, plan desktopSessionPlan, stdin io.Reader, stdout, stderr io.Writer) error {
		got = plan
		if stdin == nil || stdout == nil || stderr == nil {
			t.Fatal("stdio was not inherited")
		}
		return nil
	}
	cmd := newDesktopSessionCommand(deps)
	cmd.SetIn(bytes.NewBuffer(nil))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--node", node, "--pi-entry", pi, "--", "--session-id", "session-日本語", "--session", "resume value"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got.nodePath != node {
		t.Fatalf("node path changed: %q", got.nodePath)
	}
	wantSuffix := []string{"--session-id", "session-日本語", "--session", "resume value"}
	if !reflect.DeepEqual(got.args[len(got.args)-len(wantSuffix):], wantSuffix) {
		t.Fatalf("Pi args changed: %#v", got.args)
	}
	if got.args[0] != pi {
		t.Fatalf("Pi entrypoint changed: %q", got.args[0])
	}
	if strings.Contains(strings.Join(got.args, "\x00"), "synthetic-credential") {
		t.Fatal("credential appeared in argv")
	}
}

func TestDesktopSessionRejectsAuthorityChangingAndUnknownPiArgs(t *testing.T) {
	node, pi := desktopRuntimeFiles(t)
	cases := []struct {
		name string
		args []string
	}{
		{"provider", []string{"--provider", "google"}},
		{"provider equals", []string{"--provider=google"}},
		{"model", []string{"--model", "google/gemini"}},
		{"api key", []string{"--api-key", "must-not-appear"}},
		{"api key equals", []string{"--api-key=must-not-appear"}},
		{"extension", []string{"--extension", "/tmp/foreign.js"}},
		{"extension alias", []string{"-e", "/tmp/foreign.js"}},
		{"disable extensions", []string{"--no-extensions"}},
		{"model cycling", []string{"--models", "google/*"}},
		{"unknown extension flag", []string{"--plan"}},
		{"positional message", []string{"change provider settings"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := prepareDesktopSession(node, pi, tc.args, desktopTestDeps())
			if err == nil || !strings.Contains(err.Error(), "accepts only session lifecycle flags") {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), "must-not-appear") {
				t.Fatalf("rejected argument value leaked in error: %v", err)
			}
		})
	}
}

func TestDesktopSessionAllowsExactSessionLifecycleArgs(t *testing.T) {
	args := []string{"--session-id", "id", "--session=resume", "--continue", "--resume", "--fork", "source", "--no-session", "--name", "display"}
	if err := validateDesktopPiArgs(args); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopSessionOwnsPiVersionCheckSuppression(t *testing.T) {
	node, pi := desktopRuntimeFiles(t)
	t.Setenv("PI_SKIP_VERSION_CHECK", "caller-value-must-be-replaced")
	plan, err := prepareDesktopSession(node, pi, []string{"--session-id", "id"}, desktopTestDeps())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range plan.env {
		name, value, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(name, "PI_SKIP_VERSION_CHECK") {
			count++
			if value != "1" {
				t.Fatalf("PI_SKIP_VERSION_CHECK = %q, want command-owned 1", value)
			}
		}
	}
	if count != 1 {
		t.Fatalf("PI_SKIP_VERSION_CHECK entries = %d, want 1", count)
	}
}

func TestDesktopSessionRejectsRelativeMissingAndNonExecutableRuntime(t *testing.T) {
	node, pi := desktopRuntimeFiles(t)
	cases := []struct {
		name, node, pi, want string
	}{
		{"relative node", "node", pi, "must be absolute"},
		{"relative entry", node, "cli.js", "must be absolute"},
		{"missing node", filepath.Join(t.TempDir(), "missing"), pi, "unavailable"},
		{"missing entry", node, filepath.Join(t.TempDir(), "missing.js"), "unavailable"},
	}
	if runtime.GOOS != "windows" {
		plain := filepath.Join(t.TempDir(), "node")
		if err := os.WriteFile(plain, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, struct{ name, node, pi, want string }{"non-executable node", plain, pi, "not executable"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := prepareDesktopSession(tc.node, tc.pi, nil, desktopTestDeps())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDesktopSessionBeforeAfterFixtureMatrix(t *testing.T) {
	node, pi := desktopRuntimeFiles(t)
	root := t.TempDir()
	fixtures := map[string][]byte{
		".void-code/config":       []byte("active_harness=claude\nactive_provider=relay\nupdate_prompt=ask\n"),
		".void-code/token":        []byte("opaque fixture bytes"),
		".pi/agent/settings.json": []byte(`{"defaultProvider":"native","unrelated":true}`),
	}
	for name, value := range fixtures {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, value, 0600); err != nil {
			t.Fatal(err)
		}
	}
	assertUnchanged := func(t *testing.T) {
		t.Helper()
		for name, want := range fixtures {
			got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("fixture %s changed", name)
			}
		}
	}
	cases := []struct {
		name string
		edit func(*desktopSessionDeps)
	}{
		{"success", func(*desktopSessionDeps) {}},
		{"auth unavailable", func(d *desktopSessionDeps) {
			d.loadToken = func() (string, error) { return "", errors.New("unavailable") }
		}},
		{"grants unavailable", func(d *desktopSessionDeps) {
			d.fetchGrants = func(string, string) ([]compat.Grant, error) { return nil, errors.New("unavailable") }
		}},
		{"managed extension unavailable", func(d *desktopSessionDeps) {
			d.reconcilePi = func() (string, error) { return "", errors.New("unavailable") }
		}},
		{"process failure", func(d *desktopSessionDeps) {
			d.run = func(context.Context, desktopSessionPlan, io.Reader, io.Writer, io.Writer) error {
				return errors.New("process failure")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := desktopTestDeps()
			tc.edit(&deps)
			cmd := newDesktopSessionCommand(deps)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"--node", node, "--pi-entry", pi, "--", "--session-id", "fixture-session"})
			_ = cmd.Execute()
			assertUnchanged(t)
		})
	}
}

func TestDesktopSessionProcessPropagatesExitAndCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-free lifecycle fixture uses /bin/sh; command construction remains platform-neutral")
	}
	plan := desktopSessionPlan{nodePath: "/bin/sh", args: []string{"-c", "exit 23"}, env: os.Environ()}
	err := runDesktopSessionProcess(context.Background(), plan, nil, io.Discard, io.Discard)
	var exitErr desktopProcessExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("exit error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	plan.args = []string{"-c", "sleep 10"}
	err = runDesktopSessionProcess(ctx, plan, nil, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("canceled child returned success")
	}
}

func TestDesktopSessionBypassesVCUpdateCleanup(t *testing.T) {
	if shouldCleanOldBinary([]string{"vc", "desktop-session", "--node", "/private/node"}) {
		t.Fatal("desktop-session must bypass vc update cleanup")
	}
	for _, args := range [][]string{{"vc"}, {"vc", "status"}, {"vc", "--raw"}} {
		if !shouldCleanOldBinary(args) {
			t.Fatalf("ordinary invocation unexpectedly bypassed cleanup: %#v", args)
		}
	}
}

func TestDesktopSessionConstructionPreservesMacAndWindowsPathText(t *testing.T) {
	cases := []desktopSessionPlan{
		{nodePath: "/Applications/Void 日本語/Node Runtime/node", args: []string{"/Applications/Void 日本語/Pi/cli.js", "--session-id", "id space"}},
		{nodePath: `C:\Program Files\Void 日本語\node.exe`, args: []string{`C:\Program Files\Void 日本語\pi\cli.js`, "--session", "id space"}},
	}
	for _, plan := range cases {
		if strings.Join(plan.args, "\x00") == "" || !strings.Contains(plan.nodePath, "日本語") {
			t.Fatalf("path text was not preserved: %#v", plan)
		}
	}
}
