package main

import (
	"strings"
	"testing"
)

// VCD-49: budgetGate tests — mirrors subscription_test.go style.

func ptr(f float64) *float64 { return &f }

func TestBudgetGate_NilPct(t *testing.T) {
	d := budgetGate(nil, nil)
	if d.Block || d.Warn {
		t.Errorf("nil pct must be clean (no budget), got Block=%v Warn=%v", d.Block, d.Warn)
	}
}

func TestBudgetGate_BelowThreshold(t *testing.T) {
	for _, p := range []float64{0.0, 50.0, 79.9} {
		d := budgetGate(ptr(p), nil)
		if d.Block || d.Warn {
			t.Errorf("pct=%.1f must be clean, got Block=%v Warn=%v", p, d.Block, d.Warn)
		}
	}
}

func TestBudgetGate_WarnBand(t *testing.T) {
	for _, p := range []float64{80.0, 85.0, 95.0, 99.9} {
		d := budgetGate(ptr(p), nil)
		if d.Block {
			t.Errorf("pct=%.1f must not block (only warn)", p)
		}
		if !d.Warn {
			t.Errorf("pct=%.1f must warn", p)
		}
		if !strings.Contains(d.Message, "@makscee") {
			t.Errorf("pct=%.1f warn %q must name @makscee", p, d.Message)
		}
	}
}

func TestBudgetGate_BlockAt100(t *testing.T) {
	for _, p := range []float64{100.0, 110.0, 150.0} {
		d := budgetGate(ptr(p), nil)
		if !d.Block {
			t.Errorf("pct=%.1f must block", p)
		}
		if d.Warn {
			t.Errorf("pct=%.1f must not warn when blocking", p)
		}
		if !strings.Contains(d.Message, "@makscee") {
			t.Errorf("pct=%.1f block %q must name @makscee", p, d.Message)
		}
		if !strings.Contains(d.Message, "reached") {
			t.Errorf("pct=%.1f block %q must say 'reached'", p, d.Message)
		}
	}
}

func TestBudgetGate_BlockMessageNoDollarSign(t *testing.T) {
	// Operator constraint 2026-05-30: user-facing budget copy must NEVER show dollar values.
	budget := 45.0
	d := budgetGate(ptr(100.0), &budget)
	if strings.Contains(d.Message, "$") {
		t.Errorf("block message %q must NOT contain dollar sign (percentages only)", d.Message)
	}
	if !strings.Contains(d.Message, "@makscee") {
		t.Errorf("block message %q must name @makscee", d.Message)
	}
	if !strings.Contains(d.Message, "reached") {
		t.Errorf("block message %q must say 'reached'", d.Message)
	}
}

func TestBudgetGate_BlockWithNilBudgetUsd(t *testing.T) {
	// budgetUsd nil → generic copy, no panic, no dollar sign.
	d := budgetGate(ptr(100.0), nil)
	if !d.Block {
		t.Error("must block even when budgetUsd nil")
	}
	if !strings.Contains(d.Message, "@makscee") {
		t.Errorf("block message %q must name @makscee", d.Message)
	}
	if strings.Contains(d.Message, "$") {
		t.Errorf("block message %q must NOT contain dollar sign", d.Message)
	}
}
