// rails:pin-on-coverage the pinned Pi works today, so there is nothing to go red; strength shown by mutation instead -- a renamed provider id, an unreachable registerProvider, a bootstrap offering no allowed model, provisioning taken away, and a stray file in the pinned tree were each killed
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"
)

// The qualification the pinned-Pi step has been claiming to run.
//
// check-pinned-pi-smoke.mjs has always asked go for this test by name, and until now there was no
// such test: go answered an unmatched filter with `ok ... [no tests to run]` and exit 0, so every
// push and every release carried a green step that ran nothing.
//
// What it checks is the consequence, not a direct provider registration. The managed provider is
// intentionally absent until the real session_start/readback/controller effect path applies V2
// authority. The offline runtime matrix exercises that path with the real extension source from
// pi_extension.go, then checks request serialization and tool-call SSE parsing under aliases,
// native-child virtual modules, and desktop vendor layout.
// It uses only a local httptest readback responder and a fixture transport: it does not prove live
// relay or model availability.

// The models the extension is willing to publish for the codex provider (pi_extension.go filters
// whatever the bootstrap offers against this set).
var voidCodexSmokeModels = []string{"gpt-5.6-terra", "gpt-5.6-sol", "gpt-5.6-luna", "gpt-6-astra"}

func voidCodexSmokeDecision() map[string]any {
	return map[string]any{
		"schemaVersion": 1, "generation": "11", "outcome": "catalog",
		"evaluatedAt": "2026-09-16T12:00:00.000000000Z", "validUntil": "2026-09-16T12:02:00.000000000Z",
		"authority": map[string]any{
			"effectiveAssignmentRevision": "1", "assignmentHeadRevision": "2", "scheduledSuccessor": nil,
			"policyRevision": "3", "tierId": "fixture-tier", "tierModelSetDigest": "fixture-set", "calibrationRevision": "4",
			"providerGrantSetRevision": "5", "poolRevision": "6", "poolCollectionRevision": "7", "controlRevision": "8",
			"controlEpoch": "9", "quotaLatchRevision": "10", "quotaEpisode": nil, "inputFingerprint": "fixture-smoke-fingerprint",
			"controlMode": "active", "quotaState": "normal", "restrictionActive": false,
			"allowedCodexModelIds": voidCodexSmokeModels, "defaultCodexModelId": "gpt-5.6-terra",
			"fallbackCodexModelId": "gpt-5.6-terra", "effectiveCodexModelId": "gpt-5.6-terra",
		},
	}
}

func voidCodexSmokeBootstrap(t *testing.T, readbackURL, relayURL string) string {
	t.Helper()
	value := map[string]any{
		"version": 2, "relayUrl": relayURL, "authToken": "smoke",
		"providers": []map[string]any{{"kind": "codex", "relayProviderId": "smoke-provider", "models": voidCodexSmokeModels}},
		"modelDecision": map[string]any{
			"schemaVersion": 1, "readbackUrl": readbackURL, "pollIntervalSeconds": "30",
			"catalogDecisionTtlSeconds": "300", "catalogExpirySkewSeconds": "5",
		},
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestPiVoidCodexExtensionSmoke(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bootstrap stub is a POSIX shell script; the staged manifest this reads is darwin-arm64 anyway")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	prerequisites := requireOrSkipPinnedPiSmoke(t, root)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/vc/me" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer smoke" {
			http.Error(w, "fixture bearer mismatch", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(voidCodexSmokeDecision())
	}))
	defer server.Close()
	bootstrapJSON := voidCodexSmokeBootstrap(t, server.URL+"/v1/vc/me", server.URL)

	runPiResponsesRuntime(t, prerequisites.node, filepath.Dir(filepath.Dir(prerequisites.piEntry)), bootstrapJSON)
}
