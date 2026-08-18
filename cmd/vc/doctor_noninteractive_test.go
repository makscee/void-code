package main

import (
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/pibin"
)

func TestDoctorHelpAndMissingPiErrorAreUseful(t *testing.T) {
	if !strings.Contains(pibin.MissingMessage(), "managed Pi runtime") {
		t.Fatalf("missing Pi guidance = %q", pibin.MissingMessage())
	}
	if !strings.Contains(doctorCmd.Use, "doctor") || !strings.Contains(doctorCmd.Short, "Pi") {
		t.Fatalf("doctor command metadata lost: %#v", doctorCmd)
	}
}
