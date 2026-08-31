package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/spf13/cobra"
)

// The desktop app has no terminal, so it must reach runDeviceLoginJSON through a
// flag instead of a picker keystroke. Nothing today exposes that flag or wires
// it to anything — vc login --json is not a real option yet.
//
// These tests assume two seams the implementer must add to cmd/vc/login.go and
// cmd/vc/login_json.go, since none exist to test against:
//
//  1. loginCmd gets a bool flag named "json" (default false).
//  2. runLogin dispatches through two package-level vars instead of calling
//     runDeviceFlow directly, so a test can swap the destination without
//     making a network call:
//
//     var deviceFlowRunner = runDeviceFlow
//     var deviceLoginJSONRunner = func(cfg config.Config, out io.Writer) error {
//     return runDeviceLoginJSON(newDeviceLoginDeps(cfg), out)
//     }
//
//     runLogin reads the "json" flag off cmd and calls deviceLoginJSONRunner
//     when set, deviceFlowRunner otherwise.
//  3. newDeviceLoginDeps(cfg config.Config) deviceLoginDeps in login_json.go,
//     building deps from the real auth.DeviceStart / auth.DevicePoll /
//     installDeviceCredential / deviceBrowserURL against cfg.AuthHost — the
//     same host and same credential path the interactive flow uses.

func TestLoginCommandExposesJSONFlag(t *testing.T) {
	flag := loginCmd.Flags().Lookup("json")
	if flag == nil {
		t.Fatal(`login command has no --json flag`)
	}
	if flag.Value.Type() != "bool" {
		t.Fatalf("--json type = %s, want bool", flag.Value.Type())
	}
	if flag.DefValue != "false" {
		t.Fatalf("--json default = %s, want false — a bare `vc login` must stay interactive", flag.DefValue)
	}
}

// Flipping --json must not accidentally run both flows, or the wrong one. A
// lazy dispatch (e.g. always calling the JSON runner, or ignoring the flag
// entirely) passes any test that only checks one direction, so this asserts
// both: json=true reaches only the JSON runner, json=false reaches only the
// interactive one.
func TestLoginJSONFlagSelectsRunnerExclusively(t *testing.T) {
	originalFlow, originalJSON := deviceFlowRunner, deviceLoginJSONRunner
	t.Cleanup(func() { deviceFlowRunner, deviceLoginJSONRunner = originalFlow, originalJSON })

	var flowCalls, jsonCalls int
	deviceFlowRunner = func(config.Config) error { flowCalls++; return nil }
	deviceLoginJSONRunner = func(config.Config, io.Writer) error { jsonCalls++; return nil }

	cmd := &cobra.Command{}
	cmd.Flags().Bool("json", false, "")

	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := runLogin(cmd, nil); err != nil {
		t.Fatalf("runLogin with --json: %v", err)
	}
	if jsonCalls != 1 || flowCalls != 0 {
		t.Fatalf("json=true: jsonCalls=%d flowCalls=%d, want 1 and 0", jsonCalls, flowCalls)
	}

	if err := cmd.Flags().Set("json", "false"); err != nil {
		t.Fatal(err)
	}
	if err := runLogin(cmd, nil); err != nil {
		t.Fatalf("runLogin without --json: %v", err)
	}
	if jsonCalls != 1 || flowCalls != 1 {
		t.Fatalf("json=false: jsonCalls=%d flowCalls=%d, want 1 and 1 — interactive flow must still run untouched", jsonCalls, flowCalls)
	}
}

// newDeviceLoginDeps is what the desktop actually depends on: a fake that hands
// back non-nil closures satisfies a weak "the fields aren't nil" check while
// still talking to nothing, or worse, to a different host than the one the
// operator configured. This drives every seam against a local fake identity
// server standing in for cfg.AuthHost, and fails if wiring is faked, missing,
// or pointed elsewhere.
func TestNewDeviceLoginDepsWiresToConfiguredAuthHost(t *testing.T) {
	withTempHome(t)

	const expiresIn = 600
	const interval = 5
	future := time.Now().Add(time.Hour).UnixMilli()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/public/device/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"deviceCode":       "dev-code-wiring",
			"userCode":         "WIRE1234",
			"verificationPath": "/device",
			"intervalSeconds":  interval,
		})
	})
	mux.HandleFunc("/v1/public/device/poll", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"token":     "wiring-session.wiring-secret",
			"audience":  "vc",
			"expiresAt": future,
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	cfg := config.Config{AuthHost: server.URL}
	deps := newDeviceLoginDeps(cfg)

	if deps.start == nil || deps.poll == nil || deps.install == nil || deps.sleep == nil || deps.now == nil || deps.browserURL == nil {
		t.Fatal("newDeviceLoginDeps left a seam nil")
	}

	// browserURL must compose the same way the interactive flow's device link
	// does — a wiring that builds its own URL scheme would silently diverge
	// the moment auth host formatting changes.
	if got, want := deps.browserURL("/device"), deviceBrowserURL(cfg.AuthHost, "/device"); got != want {
		t.Fatalf("browserURL = %q, want %q (same composition as deviceBrowserURL)", got, want)
	}

	// start/poll must actually reach cfg.AuthHost. A wiring that points at
	// config.DefaultAuthHost or returns a canned result instead of calling out
	// would either hang, error, or return the wrong device code — never this.
	start, err := deps.start()
	if err != nil {
		t.Fatalf("deps.start(): %v (not reaching the configured auth host)", err)
	}
	if start.UserCode != "WIRE1234" || start.DeviceCode != "dev-code-wiring" {
		t.Fatalf("start = %+v, want the fake server's response — deps.start is not hitting cfg.AuthHost", start)
	}

	res, err := deps.poll(start.DeviceCode)
	if err != nil {
		t.Fatalf("deps.poll(): %v (not reaching the configured auth host)", err)
	}
	if res.Token != "wiring-session.wiring-secret" {
		t.Fatalf("poll token = %q, want the fake server's token", res.Token)
	}

	// install must go through the same credential path the interactive flow
	// uses (installDeviceCredential -> auth.Save), not a private stash the
	// desktop's own login would never see.
	if err := deps.install(res.Token); err != nil {
		t.Fatalf("deps.install(): %v", err)
	}
	stored, _, err := auth.Load()
	if err != nil || stored != res.Token {
		t.Fatalf("auth.Load() = %q, %v, want the installed token durable via the real credential store", stored, err)
	}
}
