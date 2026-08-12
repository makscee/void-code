package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/update"
)

func TestLaunchUpdateNudgeIsTruthfulAndNoninteractive(t *testing.T) {
	got := launchUpdateNudge(update.ProbeResult{HasUpdate: true, Latest: "v9.8.7"})
	if !strings.Contains(got, "v9.8.7") || !strings.Contains(got, "vc update") {
		t.Fatalf("nudge = %q, want version and explicit update command", got)
	}
	if got := launchUpdateNudge(update.ProbeResult{}); got != "" {
		t.Fatalf("no-update nudge = %q", got)
	}
	if got := launchUpdateNudge(update.ProbeResult{Err: errors.New("offline")}); got != "" {
		t.Fatalf("error nudge = %q", got)
	}
}
