package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/auth"
)

// The live bootstrap boundary is the input to the managed authority controller;
// accepting a V1 transport here would silently select the legacy extension path.
func TestCurrentPiBootstrapEmitsValidatedV2ModelDecisionDescriptor(t *testing.T) {
	configureModelDecisionTimingFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/vc/providers" || r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Fatalf("unexpected provider request %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"providers": []map[string]string{
			{"id": "opaque-provider-grant", "name": "opaque", "type": "openai-codex-oauth"},
		}})
	}))
	defer server.Close()
	const accessCheckHost = "https://access-check.fixture.invalid:9444"
	t.Setenv("VC_AUTH_HOST", server.URL)
	t.Setenv("VC_ACCESS_CHECK_HOST", accessCheckHost)
	t.Setenv("VC_RELAY_HOST", "https://relay.fixture.invalid:9443")
	if err := auth.Save("fixture-token"); err != nil {
		t.Fatal(err)
	}

	got, err := currentPiBootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 {
		t.Errorf("live bootstrap version = %d, want PiBootstrapV2 for model-decision mode", got.Version)
	}
	if got.ModelDecision == nil {
		t.Fatal("live bootstrap omitted modelDecision; managed authority would be bypassed")
	}

	descriptor := got.ModelDecision
	const wantReadbackURL = accessCheckHost + "/v1/vc/me"
	wantDescriptor := &piModelDecisionDescriptor{
		SchemaVersion:             1,
		ReadbackURL:               wantReadbackURL,
		PollIntervalSeconds:       "19",
		CatalogDecisionTTLSeconds: "241",
		CatalogExpirySkewSeconds:  "7",
	}
	if !reflect.DeepEqual(descriptor, wantDescriptor) {
		t.Errorf("modelDecision = %#v, want exact configured V2 descriptor %#v", descriptor, wantDescriptor)
	}
	if descriptor.ReadbackURL != wantReadbackURL {
		t.Errorf("readback URL = %q, want configured access-check authority %q", descriptor.ReadbackURL, wantReadbackURL)
	}
	readback, err := url.Parse(descriptor.ReadbackURL)
	if err != nil || (readback.Scheme != "http" && readback.Scheme != "https") || readback.Host == "" {
		t.Errorf("readback URL = %q, want an opaque absolute HTTP(S) authority URL", descriptor.ReadbackURL)
	}
	for field, values := range map[string][2]string{
		"pollIntervalSeconds":       {descriptor.PollIntervalSeconds, "19"},
		"catalogDecisionTtlSeconds": {descriptor.CatalogDecisionTTLSeconds, "241"},
		"catalogExpirySkewSeconds":  {descriptor.CatalogExpirySkewSeconds, "7"},
	} {
		if got, want := values[0], values[1]; got != want {
			t.Errorf("%s = %q, want exactly configured fixture value %q", field, got, want)
		}
	}
	boundedDescriptorSeconds(t, "pollIntervalSeconds", descriptor.PollIntervalSeconds, 1, 300)
	ttl := boundedDescriptorSeconds(t, "catalogDecisionTtlSeconds", descriptor.CatalogDecisionTTLSeconds, 1, 2147483647)
	skew := boundedDescriptorSeconds(t, "catalogExpirySkewSeconds", descriptor.CatalogExpirySkewSeconds, 0, 2147483647)
	if skew >= ttl {
		t.Errorf("catalog expiry skew = %d, want less than decision TTL %d", skew, ttl)
	}
}

// Invalid timing input must not be silently replaced by production defaults and emitted as V2 metadata.
func TestCurrentPiBootstrapRejectsInvalidModelDecisionTimingConfiguration(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		value   string
		missing bool
	}{
		{name: "poll zero", env: modelDecisionPollIntervalEnv, value: "0"},
		{name: "poll above maximum", env: modelDecisionPollIntervalEnv, value: "301"},
		{name: "poll noncanonical leading zero", env: modelDecisionPollIntervalEnv, value: "01"},
		{name: "poll negative", env: modelDecisionPollIntervalEnv, value: "-1"},
		{name: "poll missing", env: modelDecisionPollIntervalEnv, missing: true},
		{name: "ttl zero", env: modelDecisionTTLSecondsEnv, value: "0"},
		{name: "ttl above maximum", env: modelDecisionTTLSecondsEnv, value: "2147483648"},
		{name: "ttl noncanonical leading zero", env: modelDecisionTTLSecondsEnv, value: "01"},
		{name: "ttl negative", env: modelDecisionTTLSecondsEnv, value: "-1"},
		{name: "ttl missing", env: modelDecisionTTLSecondsEnv, missing: true},
		{name: "skew negative", env: modelDecisionExpirySkewSecondsEnv, value: "-1"},
		{name: "skew equal ttl", env: modelDecisionExpirySkewSecondsEnv, value: "241"},
		{name: "skew greater than ttl", env: modelDecisionExpirySkewSecondsEnv, value: "242"},
		{name: "skew noncanonical leading zero", env: modelDecisionExpirySkewSecondsEnv, value: "01"},
		{name: "skew missing", env: modelDecisionExpirySkewSecondsEnv, missing: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configureModelDecisionTimingFixture(t)
			if tc.missing {
				if err := os.Unsetenv(tc.env); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv(tc.env, tc.value)
			}
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/vc/providers" || r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Fatalf("unexpected provider request %s %q", r.URL.Path, r.Header.Get("Authorization"))
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"providers": []map[string]string{
					{"id": "opaque-provider-grant", "name": "opaque", "type": "openai-codex-oauth"},
				}})
			}))
			defer server.Close()
			t.Setenv("VC_AUTH_HOST", server.URL)
			t.Setenv("VC_ACCESS_CHECK_HOST", "https://access-check.fixture.invalid:9444")
			t.Setenv("VC_RELAY_HOST", "https://relay.fixture.invalid:9443")
			if err := auth.Save("fixture-token"); err != nil {
				t.Fatal(err)
			}

			if _, err := currentPiBootstrap(); err == nil {
				t.Fatal("currentPiBootstrap() succeeded with invalid model-decision timing configuration; want fail-closed error")
			}
		})
	}
}

func TestCurrentPiBootstrapRejectsMissingModelDecisionTiming(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/vc/providers" || r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Fatalf("unexpected provider request %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"providers": []map[string]string{
			{"id": "opaque-provider-grant", "name": "opaque", "type": "openai-codex-oauth"},
		}})
	}))
	defer server.Close()
	const accessCheckHost = "https://access-check.fixture.invalid:9444"
	t.Setenv("VC_AUTH_HOST", server.URL)
	t.Setenv("VC_ACCESS_CHECK_HOST", accessCheckHost)
	t.Setenv("VC_RELAY_HOST", "https://relay.fixture.invalid:9443")
	t.Setenv(modelDecisionPollIntervalEnv, "")
	t.Setenv(modelDecisionTTLSecondsEnv, "")
	t.Setenv(modelDecisionExpirySkewSecondsEnv, "")
	if err := auth.Save("fixture-token"); err != nil {
		t.Fatal(err)
	}

	if _, err := currentPiBootstrap(); err == nil {
		t.Fatal("currentPiBootstrap() succeeded without model-decision timing configuration; want fail-closed error")
	}
}

func boundedDescriptorSeconds(t *testing.T, field, value string, min, max int64) int64 {
	t.Helper()
	if strings.TrimSpace(value) != value || value == "" || (len(value) > 1 && value[0] == '0') {
		t.Fatalf("%s = %q, want a canonical decimal string", field, value)
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < min || n > max {
		t.Fatalf("%s = %q, want a bounded decimal in [%d,%d]", field, value, min, max)
	}
	return n
}
