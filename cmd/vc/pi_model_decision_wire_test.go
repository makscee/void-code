package main

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// Go owns only the transient wire/embed boundary. Fixture durations are not
// production defaults; the TypeScript codec owns their semantic validation.
// Round-trip the actual bootstrap type, not a duplicate test DTO.
func TestPiModelDecisionWireBootstrapV2(t *testing.T) {
	input := []byte(`{
		"version":2,
		"relayUrl":"https://fixture-relay.invalid:8443",
		"authToken":"fixture-not-a-credential",
		"providers":[{"kind":"codex","relayProviderId":"fixture-route","models":["fixture-expensive","fixture-default","fixture-economy"]}],
		"modelDecision":{
			"schemaVersion":1,
			"readbackUrl":"https://fixture-readback.invalid:7443/opaque/authority?fixture=1",
			"pollIntervalSeconds":"17",
			"catalogDecisionTtlSeconds":"120",
			"catalogExpirySkewSeconds":"2"
		}
	}`)
	var bootstrap piBootstrap
	if err := json.Unmarshal(input, &bootstrap); err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	var want, got map[string]json.RawMessage
	if err := json.Unmarshal(input, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatal(err)
	}
	if string(got["version"]) != "2" {
		t.Errorf("MISSING PRODUCT WIRE: bootstrap version = %s, want PiBootstrapV2", got["version"])
	}
	if len(got["modelDecision"]) == 0 {
		t.Fatal("MISSING PRODUCT WIRE: piBootstrap drops the V2 modelDecision descriptor; compatibility ceiling is not entitlement")
	}
	if len(got) != len(want) {
		t.Fatalf("bootstrap outer wire keys = %d, want %d", len(got), len(want))
	}
	for key, expected := range want {
		var expectedValue, actualValue any
		if err := json.Unmarshal(expected, &expectedValue); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(got[key], &actualValue); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actualValue, expectedValue) {
			t.Errorf("wire field %s changed (opaque URL, decimal strings, compatibility order must survive unchanged)", key)
		}
	}
}

// The tested TS factory must be the exact source installed by Go, not a parallel fixture.
func TestPiModelDecisionWireEmbedsAuthoritativeTypeScript(t *testing.T) {
	bytes, err := os.ReadFile("pi_extension.ts")
	if os.IsNotExist(err) {
		t.Fatal("MISSING PRODUCT SOURCE: cmd/vc/pi_extension.ts must be the single embeddable managed extension factory")
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != piVoidCodexExtensionSource {
		t.Fatal("Go-installed extension differs from the TypeScript exercised by the real pinned loader")
	}
}
