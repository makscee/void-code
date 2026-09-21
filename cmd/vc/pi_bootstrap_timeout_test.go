package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
)

type piBootstrapTransportStep struct {
	err    error
	status int
	body   string
}

type piBootstrapScriptedTransport struct {
	steps    []piBootstrapTransportStep
	requests []*http.Request
}

func (s *piBootstrapScriptedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) > len(s.steps) {
		return nil, fmt.Errorf("unexpected provider attempt %d", len(s.requests))
	}
	step := s.steps[len(s.requests)-1]
	if step.err != nil {
		return nil, step.err
	}
	if step.status == 0 {
		step.status = http.StatusOK
	}
	return &http.Response{
		StatusCode: step.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(step.body)),
		Request:    req,
	}, nil
}

func piBootstrapCatalog(t *testing.T, ids ...string) string {
	t.Helper()
	providers := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		providers = append(providers, map[string]string{
			"id":   id,
			"name": id,
			"type": "openai-codex-oauth",
		})
	}
	data, err := json.Marshal(map[string]any{"providers": providers})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func piBootstrapClient(transport http.RoundTripper) *http.Client {
	return &http.Client{Transport: transport}
}

func TestFetchProvidersLiveRetriesOnlyTransportTimeoutAndReturnsFreshCatalog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	authHost := "https://auth.test.invalid"
	token := "protected-token"
	writeAuthCache("providers", authHost, token, []auth.ProviderInfo{{
		ID:   "stale-cached-grant",
		Type: "openai-codex-oauth",
	}}, time.Now())

	transport := &piBootstrapScriptedTransport{steps: []piBootstrapTransportStep{
		{err: context.DeadlineExceeded},
		{body: piBootstrapCatalog(t, "fresh-grant")},
	}}
	providers, err := fetchProvidersLive(authHost, token, piBootstrapClient(transport))
	if err != nil {
		t.Fatalf("fetchProvidersLive() error = %v, want the second fresh response", err)
	}
	if len(transport.requests) != 2 {
		t.Fatalf("provider attempts = %d, want exactly 2", len(transport.requests))
	}
	for i, req := range transport.requests {
		if got := req.URL.String(); got != authHost+"/v1/vc/providers" {
			t.Errorf("attempt %d URL = %q, want Auth provider URL", i+1, got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("attempt %d authorization = %q, want preserved bearer", i+1, got)
		}
	}
	want := []auth.ProviderInfo{{ID: "fresh-grant", Name: "fresh-grant", Type: "openai-codex-oauth"}}
	if !reflect.DeepEqual(providers, want) {
		t.Fatalf("providers = %#v, want only the fresh second catalog %#v", providers, want)
	}
}

func TestFetchProvidersLiveReturnsErrorAfterTwoTransportTimeoutsWithoutStaleFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	authHost := "https://auth.test.invalid"
	token := "protected-token"
	stale := []auth.ProviderInfo{{ID: "stale-cached-grant", Type: "openai-codex-oauth"}}
	writeAuthCache("providers", authHost, token, stale, time.Now())

	transport := &piBootstrapScriptedTransport{steps: []piBootstrapTransportStep{
		{err: context.DeadlineExceeded},
		{err: context.DeadlineExceeded},
	}}
	providers, err := fetchProvidersLive(authHost, token, piBootstrapClient(transport))
	if err == nil {
		t.Fatal("fetchProvidersLive() error = nil, want bounded timeout failure")
	}
	if len(transport.requests) != 2 {
		t.Fatalf("provider attempts = %d, want exactly 2", len(transport.requests))
	}
	if providers != nil {
		t.Fatalf("providers = %#v, want nil and no stale/cache fallback", providers)
	}
	if strings.Contains(err.Error(), "stale-cached-grant") {
		t.Fatalf("error exposed stale catalog: %v", err)
	}
}

func TestFetchProvidersLiveDoesNotRetryNonTimeoutFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		err    error
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"error":"unauthorized"}`},
		{name: "forbidden", status: http.StatusForbidden, body: `{"error":"forbidden"}`},
		{name: "server status", status: http.StatusBadGateway, body: `{"error":"upstream"}`},
		{name: "malformed success body", status: http.StatusOK, body: `{"providers":`},
		{name: "non-timeout transport error", err: errors.New("connection reset")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			authHost := "https://auth.test.invalid"
			token := "protected-token"
			writeAuthCache("providers", authHost, token, []auth.ProviderInfo{{
				ID:   "stale-cached-grant",
				Type: "openai-codex-oauth",
			}}, time.Now())

			transport := &piBootstrapScriptedTransport{steps: []piBootstrapTransportStep{{
				status: tc.status,
				body:   tc.body,
				err:    tc.err,
			}}}
			providers, err := fetchProvidersLive(authHost, token, piBootstrapClient(transport))
			if err == nil {
				t.Fatal("fetchProvidersLive() error = nil, want terminal failure")
			}
			if len(transport.requests) != 1 {
				t.Fatalf("provider attempts = %d, want exactly 1", len(transport.requests))
			}
			if providers != nil {
				t.Fatalf("providers = %#v, want no stale/cache fallback", providers)
			}
		})
	}
}

func TestFetchProvidersLiveAcceptsSuccessfulEmptyCatalogWithoutRetry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	authHost := "https://auth.test.invalid"
	token := "protected-token"
	writeAuthCache("providers", authHost, token, []auth.ProviderInfo{{
		ID:   "stale-cached-grant",
		Type: "openai-codex-oauth",
	}}, time.Now())

	transport := &piBootstrapScriptedTransport{steps: []piBootstrapTransportStep{{
		body: piBootstrapCatalog(t),
	}}}
	providers, err := fetchProvidersLive(authHost, token, piBootstrapClient(transport))
	if err != nil {
		t.Fatalf("fetchProvidersLive() error = %v, want valid empty catalog", err)
	}
	if len(transport.requests) != 1 {
		t.Fatalf("provider attempts = %d, want exactly 1", len(transport.requests))
	}
	if providers == nil {
		t.Fatal("providers = nil, want valid non-nil empty catalog")
	}
	if !reflect.DeepEqual(providers, []auth.ProviderInfo{}) {
		t.Fatalf("providers = %#v, want empty catalog", providers)
	}
}

func TestPiBootstrapTimeoutPolicyFitsPiChildBudget(t *testing.T) {
	const piChildTimeout = 15 * time.Second
	const wantPerAttempt = 4 * time.Second
	const maxAttempts = 2

	if authProbeTimeout != wantPerAttempt {
		t.Fatalf("provider attempt timeout = %s, want dedicated %s timeout", authProbeTimeout, wantPerAttempt)
	}
	if maxAttempts*authProbeTimeout >= piChildTimeout {
		t.Fatalf("provider retry budget = %s, must remain below Pi child timeout %s", maxAttempts*authProbeTimeout, piChildTimeout)
	}
}
