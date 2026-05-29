package main

import (
	"strings"
	"testing"
)

// Context segment: brainrot emoji meter (mirrors cv-statusline.sh thresholds)

func TestContextFace_Thresholds(t *testing.T) {
	cases := []struct {
		tokens int
		want   string
	}{
		{0, "🤓"},
		{59999, "🤓"},
		{60000, "😐"},
		{119999, "😐"},
		{120000, "😵‍💫"}, // 😵‍💫 zwj sequence
		{149999, "😵‍💫"},
		{150000, "🥴"},
		{179999, "🥴"},
		{180000, "💀"},
		{999999, "💀"},
	}
	for _, c := range cases {
		if got := contextFace(c.tokens); got != c.want {
			t.Errorf("contextFace(%d) = %q, want %q", c.tokens, got, c.want)
		}
	}
}

func TestContextTokensFmt(t *testing.T) {
	cases := []struct {
		tokens int
		want   string
	}{
		{0, "0"},
		{500, "500"},
		{1000, "1k"},
		{5000, "5k"},
		{143210, "143k"},
		{1200000, "1.2M"},
		{1000000, "1M"},
		{2000000, "2M"},
	}
	for _, c := range cases {
		if got := contextTokensFmt(c.tokens); got != c.want {
			t.Errorf("contextTokensFmt(%d) = %q, want %q", c.tokens, got, c.want)
		}
	}
}

func TestStatusLineContextSegment_ShowsFaceAndTokens(t *testing.T) {
	in := statusInput{}
	in.ContextWindow.TotalInputTokens = 5000
	in.ContextWindow.ContextWindowSize = 200000
	got := renderSegments(in, segData{budgetPct: -1, subDaysLeft: -2})
	// Expect brainrot face + compact tokens, not "ctx N%"
	if !strings.Contains(got, "🤓") {
		t.Fatalf("want emoji face 🤓, got %q", got)
	}
	if !strings.Contains(got, "5k") {
		t.Fatalf("want '5k', got %q", got)
	}
}

func TestStatusLineContextSegment_EscalatesAtThreshold(t *testing.T) {
	in := statusInput{}
	in.ContextWindow.TotalInputTokens = 143000
	in.ContextWindow.ContextWindowSize = 200000
	got := renderSegments(in, segData{budgetPct: -1, subDaysLeft: -2})
	if !strings.Contains(got, "143k") {
		t.Fatalf("want '143k', got %q", got)
	}
	// 143k is in 120-150k band → 😵‍💫
	if !strings.Contains(got, "😵") {
		t.Fatalf("want dizzy face emoji for 143k, got %q", got)
	}
}

func TestStatusLineContextSegment_AbsentRendersDash(t *testing.T) {
	in := statusInput{} // no context_window at all (fresh session, no API response)
	got := renderSegments(in, segData{budgetPct: -1, subDaysLeft: -2})
	if !strings.Contains(got, "—") {
		t.Fatalf("want '—' when context absent, got %q", got)
	}
}

// Task 4 tests: subscription days left segment
func TestStatusLineSubDays_Positive(t *testing.T) {
	in := statusInput{}
	in.ContextWindow.TotalInputTokens = 10000
	in.ContextWindow.ContextWindowSize = 200000
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
