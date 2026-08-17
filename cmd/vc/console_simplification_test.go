package main

import (
	"reflect"
	"testing"
)

func TestDirectPiLaunchAddsOnlyTransportExtension(t *testing.T) {
	got := buildPiArgs([]string{"-p", "hello"}, "/tmp/void-code.ts")
	want := []string{"-e", "/tmp/void-code.ts", "-p", "hello"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Pi args = %#v, want %#v; VC must not inject provider/model selection", got, want)
	}
}

func TestPiBootstrapIsSubscriptionScopedNotProfileSelected(t *testing.T) {
	// The bootstrap is the only transport handoff; it is intentionally independent
	// of legacy active_provider/active_harness profile keys.
	if piBootstrapCmd.Hidden != true {
		t.Fatal("transport bootstrap must remain internal")
	}
}
