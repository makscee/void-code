package main

import (
	"fmt"
	"github.com/makscee/void-code/internal/pibin"
	"strings"
	"testing"
)

func TestDoctorHelpAndMissingPiErrorAreUseful(t *testing.T) {
	old := piIsInstalled
	defer func() { piIsInstalled = old }()
	piIsInstalled = func() bool { return false }
	if err := ensurePiInstalledForTest(); err == nil || !strings.Contains(err.Error(), "pi CLI not found") {
		t.Fatalf("missing Pi error=%v", err)
	}
	if !strings.Contains(doctorCmd.Use, "doctor") || !strings.Contains(doctorCmd.Short, "Pi") {
		t.Fatalf("doctor command metadata lost: %#v", doctorCmd)
	}
}
func ensurePiInstalledForTest() error {
	if !piIsInstalled() {
		return fmt.Errorf("%s", pibin.MissingMessage())
	}
	return nil
}
