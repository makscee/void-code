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
	got := renderSegments(in, segData{balanceKnown: false})
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
	got := renderSegments(in, segData{balanceKnown: false})
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
	got := renderSegments(in, segData{balanceKnown: false})
	if !strings.Contains(got, "—") {
		t.Fatalf("want '—' when context absent, got %q", got)
	}
}

// VCD-56: $ balance segment tests (Option A — plain $X.XX, no sub-days).

func ptrF(v float64) *float64 { return &v }

func TestStatusLineBalance_Known(t *testing.T) {
	d := segData{balanceUsd: ptrF(12.4), balanceKnown: true}
	in := statusInput{}
	in.ContextWindow.TotalInputTokens = 1000
	in.ContextWindow.ContextWindowSize = 200000
	got := renderSegments(in, d)
	if !strings.Contains(got, "$12.40") {
		t.Errorf("renderSegments = %q, want it to contain $12.40", got)
	}
}

func TestStatusLineBalance_Unknown(t *testing.T) {
	d := segData{balanceKnown: false}
	in := statusInput{}
	in.ContextWindow.TotalInputTokens = 1000
	in.ContextWindow.ContextWindowSize = 200000
	got := renderSegments(in, d)
	if strings.Contains(got, "$") {
		t.Errorf("renderSegments = %q, must hide $ when balance unknown", got)
	}
}

func TestStatusLineNoSubDays(t *testing.T) {
	d := segData{balanceUsd: ptrF(5), balanceKnown: true}
	in := statusInput{}
	in.ContextWindow.TotalInputTokens = 1000
	in.ContextWindow.ContextWindowSize = 200000
	got := renderSegments(in, d)
	if strings.Contains(got, "sub ") {
		t.Errorf("renderSegments = %q, sub-days segment must be gone", got)
	}
}
