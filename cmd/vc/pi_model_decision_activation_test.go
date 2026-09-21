package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/auth"
)

// The live bootstrap boundary is the input to the managed authority controller;
// accepting a V1 transport here would silently select the legacy extension path.
func TestCurrentPiBootstrapEmitsValidatedV2ModelDecisionDescriptor(t *testing.T) {
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
	if descriptor.ReadbackURL != wantReadbackURL {
		t.Errorf("readback URL = %q, want configured access-check authority %q", descriptor.ReadbackURL, wantReadbackURL)
	}
	readback, err := url.Parse(descriptor.ReadbackURL)
	if err != nil || (readback.Scheme != "http" && readback.Scheme != "https") || readback.Host == "" {
		t.Errorf("readback URL = %q, want an opaque absolute HTTP(S) authority URL", descriptor.ReadbackURL)
	}
	boundedDescriptorSeconds(t, "pollIntervalSeconds", descriptor.PollIntervalSeconds, 1, 300)
	ttl := boundedDescriptorSeconds(t, "catalogDecisionTtlSeconds", descriptor.CatalogDecisionTTLSeconds, 1, 2147483647)
	skew := boundedDescriptorSeconds(t, "catalogExpirySkewSeconds", descriptor.CatalogExpirySkewSeconds, 0, 2147483647)
	if skew >= ttl {
		t.Errorf("catalog expiry skew = %d, want less than decision TTL %d", skew, ttl)
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
