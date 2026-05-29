package main

import (
	"strings"
	"testing"
)

// Task 1 tests: context window segment
func TestStatusLineContextSegment_UsesUsedPercentage(t *testing.T) {
	in := statusInput{}
	in.ContextWindow.UsedPercentage = 8
	in.ContextWindow.ContextWindowSize = 200000
	got := renderSegments(in, segData{budgetPct: -1, subDaysLeft: -2})
	if !strings.Contains(got, "ctx 8%") {
		t.Fatalf("want context segment 'ctx 8%%', got %q", got)
	}
}

func TestStatusLineContextSegment_FallsBackToTokenRatio(t *testing.T) {
	in := statusInput{}
	in.ContextWindow.TotalInputTokens = 90000
	in.ContextWindow.TotalOutputTokens = 10000
	in.ContextWindow.ContextWindowSize = 200000
	// UsedPercentage zero/absent → derive 100000/200000 = 50%
	got := renderSegments(in, segData{budgetPct: -1, subDaysLeft: -2})
	if !strings.Contains(got, "ctx 50%") {
		t.Fatalf("want 'ctx 50%%', got %q", got)
	}
}

func TestStatusLineContextSegment_AbsentRendersDash(t *testing.T) {
	in := statusInput{} // no context_window at all
	got := renderSegments(in, segData{budgetPct: -1, subDaysLeft: -2})
	if !strings.Contains(got, "ctx —") {
		t.Fatalf("want 'ctx —' when context absent, got %q", got)
	}
}

// Task 4 tests: subscription days left segment
func TestStatusLineSubDays_Positive(t *testing.T) {
	in := statusInput{}
	in.ContextWindow.UsedPercentage = 10
	got := renderSegments(in, segData{budgetPct: -1, subDaysLeft: 12})
	if !strings.Contains(got, "sub 12d") {
		t.Fatalf("want 'sub 12d', got %q", got)
	}
}

func TestStatusLineSubDays_Unlimited(t *testing.T) {
	got := renderSegments(statusInput{}, segData{budgetPct: -1, subDaysLeft: -1})
	if !strings.Contains(got, "sub ∞") {
		t.Fatalf("want 'sub ∞', got %q", got)
	}
}

func TestStatusLineSubDays_Unknown(t *testing.T) {
	got := renderSegments(statusInput{}, segData{budgetPct: -1, subDaysLeft: -2})
	if strings.Contains(got, "sub") {
		t.Fatalf("sub segment must be HIDDEN when unknown, got %q", got)
	}
}

// Task 5 tests: budget spent % segment
func TestStatusLineBudget_Known(t *testing.T) {
	got := renderSegments(statusInput{}, segData{budgetPct: 85, subDaysLeft: -2})
	if !strings.Contains(got, "budget 85%") {
		t.Fatalf("want 'budget 85%%', got %q", got)
	}
}

func TestStatusLineBudget_Unknown(t *testing.T) {
	got := renderSegments(statusInput{}, segData{budgetPct: -1, subDaysLeft: -2})
	if strings.Contains(got, "budget") {
		t.Fatalf("budget segment must be HIDDEN when unknown (VCD-49 not landed), got %q", got)
	}
}
